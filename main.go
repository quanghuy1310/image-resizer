package main

import (
	"context"
	"flag"
	"fmt" // Thêm: Dùng để truyền PID qua CLI flag
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	// Đã XÓA: "unsafe"
	// Đã XÓA: "golang.org/x/sys/windows"
)

var (
	stopFlag      int32
	logger        *log.Logger
	historyLogger *log.Logger
	historyMu     sync.Mutex
	// Đã XÓA: job windows.Handle
)

// Đã XÓA: func createJobObject() windows.Handle {...}

// THÊM: Flag mới để nhận PID của tiến trình cha
var parentPIDArg = flag.Int("parent-pid", 0, "parent process ID for monitoring")

// THÊM: Hàm giám sát tiến trình cha (chỉ chạy trong Child Process)
func watchParent(ctx context.Context, cancel context.CancelFunc, parentPID int) {
	if parentPID == 0 {
		return // Không cần giám sát
	}

	// Find the parent process handle
	proc, err := os.FindProcess(parentPID)
	if err != nil {
		logger.Printf("[CHILD] WARNING: Cannot find parent process %d: %v. Proceeding without monitoring.", parentPID, err)
		return
	}

	// Check process existence by sending signal 0 (không làm gì, chỉ kiểm tra)
	checkProcessAlive := func() bool {
		// Signal(0) là cách cross-platform để kiểm tra sự tồn tại của tiến trình.
		err := proc.Signal(syscall.Signal(0))
		return err == nil
	}

	ticker := time.NewTicker(5 * time.Second) // Check mỗi 5 giây
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return // Parent/Child đã ra tín hiệu thoát
		case <-ticker.C:
			if !checkProcessAlive() {
				// Parent đã chết, hủy context để child thoát
				logger.Printf("[CHILD] Parent PID %d died. Exiting child process gracefully.", parentPID)
				cancel()
				return
			}
		}
	}
}

func main() {
	// CLI flags
	child := flag.Bool("child", false, "run as child process for one folder")
	folderArg := flag.String("folder", "", "folder to process when in child mode")
	flag.Parse()

	cfg := LoadConfig()
	// Setup main logger
	setupLogger(cfg)
	if logger == nil {
		logger = log.New(os.Stdout, "[ImageResize] ", log.LstdFlags|log.Lmicroseconds)
	}

	// Setup history logger
	if err := os.MkdirAll(filepath.Dir(cfg.ResizeHistoryLog), 0o755); err != nil {
		logger.Fatalf("[FATAL] Cannot create history log dir: %v", err)
	}
	hf, err := os.OpenFile(cfg.ResizeHistoryLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logger.Fatalf("[FATAL] Cannot open history log file: %v", err)
	}
	// Wire the package-level historyFile and atomic writer/logger
	historyFile = hf
	if historyAtomicWriter == nil {
		historyAtomicWriter = NewAtomicWriter()
	}
	historyAtomicWriter.Store(hf)
	historyLogger = log.New(historyAtomicWriter, "", log.LstdFlags|log.Lmicroseconds)
	defer hf.Close()

	// Debug Config Log
	logConfig(cfg, *child, *folderArg)

	// Tạo Context và Handle OS Signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		logger.Println("[PARENT] signal received, stopping...")
		atomic.StoreInt32(&stopFlag, 1)
		cancel()
	}()

	// THÊM: Khởi động giám sát PID của Parent nếu là Child Process
	if *child && *parentPIDArg != 0 {
		go watchParent(ctx, cancel, *parentPIDArg)
	}

	// --- LOG ROTATION ---
	if !*child {
		go func() {
			rotateTicker := time.NewTicker(10 * time.Second)
			cleanupTicker := time.NewTicker(1 * time.Hour)
			defer rotateTicker.Stop()
			defer cleanupTicker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-rotateTicker.C:
					RotateLogIfNeeded(cfg)
					RotateHistoryLogIfNeeded(cfg)
				case <-cleanupTicker.C:
					CleanUpOldLogs(cfg)
				}
			}
		}()
	}

	// --- CHILD PROCESS MODE ---
	if *child {
		if *folderArg == "" {
			logger.Fatal("[FATAL] child mode requires --folder argument")
		}
		if cfg.DryRun {
			logger.Println("[CHILD] *** DRY-RUN MODE ENABLED ***")
		}
		err := processFolder(*folderArg, cfg)
		if err != nil {
			// Thêm kiểm tra context bị hủy (do Parent chết)
			if err == ErrStop || ctx.Err() == context.Canceled {
				logger.Printf("[CHILD] stopped by request or parent exit")
				os.Exit(0)
			}
			logger.Printf("[CHILD] failed: %v", err)
			os.Exit(1)
		}
		os.Exit(0)
		return
	}

	// --- PARENT PROCESS MODE ---
	logger.Printf("[PARENT] Starting... Mode=%s | DRY_RUN=%v", cfg.ProcessMode, cfg.DryRun)
	if cfg.DryRun {
		logger.Println("[PARENT] *** DRY-RUN MODE ENABLED - No files will be written or children spawned ***")
	}

	if cfg.EnableScheduler {
		RunScheduler(ctx, cfg)
		return
	}

	if cfg.ProcessMode == "multi" {
		// XÓA HẾT LOGIC KHỞI TẠO JOB OBJECT Ở ĐÂY
		spawnChildrenForFolders(ctx, cfg)
	} else {
		if err := ProcessFolderTree(cfg); err != nil {
			logger.Printf("[PARENT] ProcessFolderTree result: %v", err)
		}
	}

	// Graceful shutdown with timeout (10 seconds max)
	shutdownDone := make(chan struct{})
	go func() {
		logger.Println("[PARENT] waiting for goroutines to finish...")
		// Note: All goroutines should be finishing now due to context cancellation
		shutdownDone <- struct{}{}
	}()

	select {
	case <-shutdownDone:
		// Clean exit
		logger.Println("[PARENT] finished.")
	case <-time.After(10 * time.Second):
		logger.Println("[PARENT] shutdown timeout reached, forcing exit...")
		// Force flush any remaining logs
		if logFile != nil {
			logFile.Sync()
		}
		if historyFile != nil {
			historyFile.Sync()
		}
		// Hard exit to avoid hanging console
		os.Exit(0)
	}
}

// logConfig prints config for debugging
func logConfig(cfg Config, isChild bool, folder string) {
	if isChild {
		logger.Printf("[CONFIG-CHILD] Folder=%s ThreadsPerProcess=%d BatchSize=%d", folder, cfg.ThreadsPerProcess, cfg.BatchSize)
	} else {
		logger.Printf("[CONFIG-PARENT] BaseDir=%s Processes=%d Scheduler=%v", cfg.BaseDir, cfg.Processes, cfg.EnableScheduler)
	}
}

// spawnChildrenForFolders (Đã sửa để truyền Parent PID)
func spawnChildrenForFolders(ctx context.Context, cfg Config) {
	logger.Printf("[PARENT] Scanning folders in: %s", cfg.BaseDir)
	var folders []FolderDate

	// THÊM: Lấy PID của tiến trình Parent hiện tại
	parentPID := os.Getpid()

	_ = filepath.WalkDir(cfg.BaseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == cfg.BaseDir {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(cfg.BaseDir, path)
			if fd, ok := parseFolderDate(rel); ok {
				fd.Path = path
				folders = append(folders, fd)
			} else if cfg.ProcessMode == "stream" {
				folders = append(folders, FolderDate{Path: path})
			}
		}
		return nil
	})

	sort.Slice(folders, func(i, j int) bool {
		if folders[i].Year != folders[j].Year {
			return folders[i].Year < folders[j].Year
		}
		if folders[i].Month != folders[j].Month {
			return folders[i].Month < folders[j].Month
		}
		return folders[i].Day < folders[j].Day
	})

	logger.Printf("[PARENT] Found %d folders, spawning %d concurrent children", len(folders), cfg.Processes)

	// Create an OS job object on platforms that support it so that child
	// processes are terminated automatically when the parent exits.
	jobHandle, err := createJobObject()
	if err != nil {
		logger.Printf("[PARENT] warning: cannot create job object: %v", err)
		jobHandle = 0
	} else {
		logger.Printf("[PARENT] job object created")
		defer closeJob(jobHandle)
	}

	sem := make(chan struct{}, cfg.Processes)
	var wg sync.WaitGroup

	for _, fd := range folders {
		f := fd.Path
		select {
		case <-ctx.Done():
			return
		default:
		}
		if atomic.LoadInt32(&stopFlag) == 1 {
			break
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}

		wg.Add(1)
		go func(folder string) {
			defer wg.Done()
			defer func() { <-sem }()

			if atomic.LoadInt32(&stopFlag) == 1 {
				return
			}

			// Start child and assign it to the job object when possible so
			// the OS will kill it if the parent process dies abruptly.
			cmd := exec.CommandContext(ctx, os.Args[0],
				"--child",
				"--folder="+folder,
				fmt.Sprintf("--parent-pid=%d", parentPID),
			)
			cmd.Env = os.Environ()
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			// Respect dry-run mode: don't actually spawn children
			if cfg.DryRun {
				logger.Printf("[PARENT][DRYRUN] would spawn child for %s", filepath.Base(folder))
				return
			}

			logger.Printf("[PARENT] Spawn: %s", filepath.Base(folder))
			if err := cmd.Start(); err != nil {
				if ctx.Err() != context.Canceled {
					logger.Printf("[PARENT] Child start error %s: %v", filepath.Base(folder), err)
				}
				return
			}

			// Try to assign the started process to the job (no-op on unsupported OS).
			if err := assignProcessToJob(jobHandle, cmd.Process); err != nil {
				logger.Printf("[PARENT] assign to job failed for %s: %v", filepath.Base(folder), err)
			}

			// Wait for child to finish, but be responsive to context cancellation
			waitDone := make(chan error, 1)
			go func() {
				waitDone <- cmd.Wait()
			}()

			select {
			case err := <-waitDone:
				if err != nil && ctx.Err() != context.Canceled {
					logger.Printf("[PARENT] Child error %s: %v", filepath.Base(folder), err)
				}
			case <-ctx.Done():
				// Context cancelled, try to kill the child
				logger.Printf("[PARENT] Killing child %s due to context cancellation", filepath.Base(folder))
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				// Wait a bit for it to die
				select {
				case <-waitDone:
				case <-time.After(2 * time.Second):
					logger.Printf("[PARENT] Child %s did not exit in time after kill signal", filepath.Base(folder))
				}
			}
		}(f)
	}
	wg.Wait()
}
