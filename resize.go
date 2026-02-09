package main

import (
	"errors"
	"image"
	_ "image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/h2non/bimg"
)

type FolderDate struct {
	Path  string
	Year  int
	Month int
	Day   int
}

var (
	ErrStop = errors.New("stop requested")
)

func historyWrite(line string) {
	if historyLogger == nil {
		return
	}
	historyMu.Lock()
	historyLogger.Println(line)
	historyMu.Unlock()
}

func checkStopRequested(cfg Config) bool {
	if atomic.LoadInt32(&stopFlag) == 1 {
		return true
	}
	flagPath := filepath.Join(cfg.BaseDir, "stop.flag")
	if _, err := os.Stat(flagPath); err == nil {
		atomic.StoreInt32(&stopFlag, 1)
		return true
	}
	return false
}

func parseFolderDate(rel string) (FolderDate, bool) {
	parts := strings.Split(rel, string(os.PathSeparator))
	for i := 0; i < len(parts)-2; i++ {
		y, errY := strconv.Atoi(parts[i])
		m, errM := strconv.Atoi(parts[i+1])
		d, errD := strconv.Atoi(parts[i+2])
		if errY == nil && errM == nil && errD == nil {
			return FolderDate{Path: filepath.Join(parts[:i+3]...), Year: y, Month: m, Day: d}, true
		}
	}
	return FolderDate{}, false
}

func isImageSkip(path string, cfg Config) bool {
	// 1. Nếu config không bật skip thì trả về false luôn (để resize lại)
	if !cfg.SkipSameResolution {
		return false
	}

	// 2. Mở file (chỉ mở stream, chưa đọc nội dung vào RAM)
	file, err := os.Open(path)
	if err != nil {
		// Nếu lỗi đọc file (ví dụ file đang bị khóa), trả về false để quy trình chính xử lý/retry sau
		return false
	}
	defer file.Close()

	// 3. Chỉ đọc Header để lấy kích thước (Cực nhanh)
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		// Nếu file lỗi header hoặc không phải ảnh, trả về false để quy trình chính xử lý
		return false
	}

	// 4. So sánh đúng logic config của bạn
	return config.Width == cfg.ResizeWidth && config.Height == cfg.ResizeHeight
}

// isTargetFile determines whether the file should be considered for resizing
// based on configured prefixes and extension.
func isTargetFile(path string, cfg Config) bool {
	base := strings.ToLower(filepath.Base(path))
	// check ext
	if !(strings.HasSuffix(base, ".jpg") || strings.HasSuffix(base, ".jpeg")) {
		return false
	}
	// if no prefixes configured, treat all images as targets
	if len(cfg.ImagePrefixes) == 0 {
		return true
	}
	for _, p := range cfg.ImagePrefixes {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return false
}

func ResizeImage(path string, cfg Config) error {
	base := filepath.Base(path)
	if !isTargetFile(path, cfg) {
		return nil
	}

	// If dry-run enabled, report and skip actual write/processing
	if cfg.DryRun {
		logger.Printf("[DRYRUN] would process: %s -> %dx%d", path, cfg.ResizeWidth, cfg.ResizeHeight)
		historyWrite("[DRYRUN] " + path)
		return nil
	}

	if isImageSkip(path, cfg) {
		logger.Printf("[SKIP] %s", path)
		historyWrite("[SKIP] " + path)
		return nil
	}

	var lastErr error
	for attempt := 0; attempt <= cfg.ImageRetry; attempt++ {
		if checkStopRequested(cfg) {
			historyWrite("[STOPPED] " + path)
			return ErrStop
		}

		buf, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			logger.Printf("[ERROR] read %s: %v", path, err)
			historyWrite("[ERROR:READ] " + path + " | " + err.Error())
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if len(buf) == 0 {
			lastErr = errors.New("empty file")
			logger.Printf("[ERROR] empty file: %s", path)
			historyWrite("[ERROR:EMPTY] " + path)
			return lastErr
		}

		opts := bimg.Options{
			Width:         cfg.ResizeWidth,
			Height:        cfg.ResizeHeight,
			Quality:       cfg.Quality,
			StripMetadata: true,
			Interlace:     true,
			Type:          bimg.JPEG,
		}

		out, err := bimg.NewImage(buf).Process(opts)
		if err != nil {
			lastErr = err
			logger.Printf("[WARN] process %s attempt %d: %v", path, attempt+1, err)
			historyWrite("[ERROR:PROCESS] " + path + " | " + err.Error())
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Tối ưu: Dùng Atomic Write (Ghi ra .tmp -> Rename) để đảm bảo file luôn toàn vẹn
		tmpPath := path + ".tmp"
		if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
			lastErr = err
			logger.Printf("[ERROR] write tmp %s: %v", tmpPath, err)
			historyWrite("[ERROR:WRITE_TMP] " + path + " | " + err.Error())
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if err := os.Rename(tmpPath, path); err != nil {
			os.Remove(tmpPath) // Clean up
			lastErr = err
			logger.Printf("[ERROR] rename %s: %v", path, err)
			historyWrite("[ERROR:RENAME] " + path + " | " + err.Error())
			continue
		}

		// Tối ưu log: Chuyển log OK chi tiết sang chế độ Verbose.
		if cfg.Verbose {
			logger.Printf("[INFO] Processed OK: %s -> %dx%d (%d bytes)", base, cfg.ResizeWidth, cfg.ResizeHeight, len(out))
		}

		// Luôn ghi log OK vào file lịch sử (history)
		historyWrite("[OK] " + path)

		return nil
	}

	if lastErr == nil {
		lastErr = errors.New("unknown")
	}
	historyWrite("[FAILED] " + path + " | " + lastErr.Error())
	return lastErr
}

// Tối ưu: Tự động dọn dẹp các file .tmp còn sót lại từ lần chạy trước bị crash

func chunkFiles(files []string, batchSize int) [][]string {
	if batchSize <= 0 {
		batchSize = 1
	}
	var chunks [][]string
	for i := 0; i < len(files); i += batchSize {
		end := i + batchSize
		if end > len(files) {
			end = len(files)
		}
		chunks = append(chunks, files[i:end])
	}
	return chunks
}

func processFolder(folder string, cfg Config) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Printf("[PANIC] folder %s recovered: %v\n%s", folder, r, debug.Stack())
		}
	}()

	// Tối ưu: Chỉ dùng ReadDir 1 lần cho cả việc tìm ảnh và dọn rác
	entries, err := os.ReadDir(folder)
	if err != nil {
		logger.Printf("[ERROR] read dir %s: %v", folder, err)
		return err
	}

	var files []string
	staleThreshold := 1 * time.Hour
	now := time.Now()

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()

		// --- Tích hợp dọn dẹp .tmp tại đây ---
		if strings.HasSuffix(name, ".tmp") {
			info, err := e.Info()
			if err == nil && now.Sub(info.ModTime()) > staleThreshold {
				_ = os.Remove(filepath.Join(folder, name))
				// Không log để giảm spam IO
			}
			continue // Skip .tmp file
		}

		lowerName := strings.ToLower(name)
		// quick extension filter
		if !(strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg")) {
			continue
		}
		fullPath := filepath.Join(folder, name)
		if isTargetFile(fullPath, cfg) {
			files = append(files, fullPath)
		}
	}

	total := int64(len(files))
	if total == 0 {
		return nil
	}

	// ... (Phần còn lại giữ nguyên: workCh, worker pool...) ...
	logger.Printf("[FOLDER] Start %s | %d images", folder, total)
	start := time.Now()
	var done int64

	workCh := make(chan []string, cfg.ThreadsPerProcess)
	var wg sync.WaitGroup

	stopETA := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second) // Tăng lên 5s để giảm log spam
		defer t.Stop()
		for {
			select {
			case <-t.C:
				p := atomic.LoadInt64(&done)
				elapsed := time.Since(start).Seconds()
				if p == 0 || elapsed < 0.001 {
					continue
				}
				speed := float64(p) / elapsed
				eta := time.Duration(float64(total-p)/speed) * time.Second
				logger.Printf("[PROGRESS] %s | %d/%d | %.2f img/s | ETA %v", folder, p, total, speed, eta)
			case <-stopETA:
				return
			}
		}
	}()

	// Worker Pool logic giữ nguyên
	for i := 0; i < cfg.ThreadsPerProcess; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for batch := range workCh {
				if checkStopRequested(cfg) {
					return
				}
				for _, f := range batch {
					if checkStopRequested(cfg) {
						return
					}
					if err := ResizeImage(f, cfg); err == nil {
						atomic.AddInt64(&done, 1)
					}
				}
			}
		}(i)
	}

	for _, batch := range chunkFiles(files, cfg.BatchSize) {
		if checkStopRequested(cfg) {
			break
		}
		workCh <- batch
	}
	close(workCh)
	wg.Wait()
	close(stopETA)

	logger.Printf("[FOLDER] Completed %s | elapsed %v | done %d/%d", folder, time.Since(start), atomic.LoadInt64(&done), total)
	return nil
}

func ProcessFolderTree(cfg Config) error {
	logger.Printf("[SCAN] Scanning: %s", cfg.BaseDir)
	var folders []FolderDate

	_ = filepath.WalkDir(cfg.BaseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == cfg.BaseDir {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(cfg.BaseDir, path)
		if fd, ok := parseFolderDate(rel); ok {
			fd.Path = path
			folders = append(folders, fd)
			return nil
		}
		if cfg.ProcessMode == "stream" {
			folders = append(folders, FolderDate{Path: path})
		}
		return nil
	})

	if cfg.ProcessMode == "ordered" {
		sort.Slice(folders, func(i, j int) bool {
			if folders[i].Year != folders[j].Year {
				return folders[i].Year < folders[j].Year
			}
			if folders[i].Month != folders[j].Month {
				return folders[i].Month < folders[j].Month
			}
			return folders[i].Day < folders[j].Day
		})
	}

	for _, fd := range folders {
		if checkStopRequested(cfg) {
			return ErrStop
		}
		if err := processFolder(fd.Path, cfg); err != nil {
			if err == ErrStop {
				return ErrStop
			}
			logger.Printf("[ERROR] folder %s failed: %v", fd.Path, err)
		}
	}
	return nil
}
