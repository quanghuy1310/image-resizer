package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// RunScheduler chạy theo giờ trong config, chạy multi-process workflow cho N ngày gần nhất
func RunScheduler(ctx context.Context, cfg Config) {
	if !cfg.EnableScheduler {
		return
	}

	logger.Printf("[SCHEDULER] Starting at %02d:%02d daily | DRY_RUN=%v", cfg.ScheduleHour, cfg.ScheduleMin, cfg.DryRun)
	if cfg.DryRun {
		logger.Println("[SCHEDULER] *** DRY-RUN MODE ENABLED ***")
	}

	hour := cfg.ScheduleHour
	min := cfg.ScheduleMin
	var lastRunDate time.Time

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Println("[SCHEDULER] exiting")
			return
		case now := <-ticker.C:
			if now.Hour() == hour && now.Minute() == min && !sameDay(now, lastRunDate) {
				lastRunDate = now
				logger.Printf("[SCHEDULER] trigger multi-process workflow at %02d:%02d", hour, min)
				// FIX 1: Truyền context vào runMultiForLastNDays
				runMultiForLastNDays(ctx, cfg, cfg.SchedulerLastNDays)
			}
		}
	}
}

// runMultiForLastNDays quét BaseDir, chọn N ngày gần nhất, spawn child process
// FIX 2: Thêm tham số context.Context
func runMultiForLastNDays(ctx context.Context, cfg Config, n int) {
	var folders []FolderDate

	_ = filepath.WalkDir(cfg.BaseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() || path == cfg.BaseDir {
			return nil
		}
		rel, _ := filepath.Rel(cfg.BaseDir, path)
		if fd, ok := parseFolderDate(rel); ok {
			fd.Path = path
			folders = append(folders, fd)
		}
		return nil
	})

	if len(folders) == 0 {
		logger.Println("[SCHEDULER] không tìm thấy folder nào")
		return
	}

	// Sắp xếp giảm dần theo ngày
	sort.Slice(folders, func(i, j int) bool {
		if folders[i].Year != folders[j].Year {
			return folders[i].Year > folders[j].Year
		}
		if folders[i].Month != folders[j].Month {
			return folders[i].Month > folders[j].Month
		}
		return folders[i].Day > folders[j].Day
	})

	if len(folders) > n {
		folders = folders[:n]
	}

	logger.Printf("[SCHEDULER] running multi-process for %d most recent days", len(folders))

	type FolderResult struct {
		Path    string
		Success bool
		Error   error
	}

	var results []FolderResult
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.Processes)
	var wg sync.WaitGroup

	// create job object so children spawned by the scheduler are tied to this
	// process and will be killed if this parent exits unexpectedly.
	jobHandle, err := createJobObject()
	if err != nil {
		logger.Printf("[SCHEDULER] warning: cannot create job object: %v", err)
		jobHandle = 0
	} else {
		defer closeJob(jobHandle)
	}

	parentPID := os.Getpid()

	for _, f := range folders {
		// Thêm kiểm tra context ở đây để dừng spawn process nếu Ctrl+C
		select {
		case <-ctx.Done():
			logger.Println("[SCHEDULER] context cancelled, stopping spawn loop.")
			return
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(folder string) {
			defer wg.Done()
			defer func() { <-sem }()

			// Kiểm tra context dừng ngay trước khi spawn
			if ctx.Err() != nil {
				return
			}

			logger.Printf("[SCHEDULER] spawn child for %s", folder)

			if cfg.DryRun {
				mu.Lock()
				logger.Printf("[SCHEDULER][DRYRUN] would spawn child for %s", folder)
				results = append(results, FolderResult{Path: folder, Success: true})
				mu.Unlock()
				return
			}

			cmd := exec.CommandContext(ctx, os.Args[0], "--child", "--folder="+folder, fmt.Sprintf("--parent-pid=%d", parentPID))
			cmd.Env = os.Environ()
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			if err := cmd.Start(); err != nil {
				mu.Lock()
				logger.Printf("[SCHEDULER] child start failed for %s: %v", folder, err)
				results = append(results, FolderResult{Path: folder, Success: false, Error: err})
				mu.Unlock()
				return
			}

			if err := assignProcessToJob(jobHandle, cmd.Process); err != nil {
				logger.Printf("[SCHEDULER] assign to job failed for %s: %v", folder, err)
			}

			err := cmd.Wait()

			mu.Lock()
			if err != nil {
				if ctx.Err() == context.Canceled {
					logger.Printf("[SCHEDULER] child %s stopped by signal (Context Cancelled)", filepath.Base(folder))
				} else {
					logger.Printf("[SCHEDULER] child for %s failed: %v", folder, err)
					results = append(results, FolderResult{Path: folder, Success: false, Error: err})
				}
			} else {
				logger.Printf("[SCHEDULER] child completed %s", folder)
				results = append(results, FolderResult{Path: folder, Success: true})
			}
			mu.Unlock()
		}(f.Path)
	}

	wg.Wait()
	logger.Println("[SCHEDULER] all scheduled children completed")

	// --- Summary report ---
	totalFolders := len(results)
	successCount := 0
	failCount := 0
	var failedFolders []string
	for _, r := range results {
		if r.Success {
			successCount++
		} else {
			failCount++
			failedFolders = append(failedFolders, r.Path)
		}
	}

	logger.Println("========== SCHEDULER SUMMARY ==========")
	logger.Printf("Total folders processed: %d", totalFolders)
	logger.Printf("Successful: %d", successCount)
	logger.Printf("Failed: %d", failCount)
	if failCount > 0 {
		logger.Println("Failed folders:")
		for _, f := range failedFolders {
			logger.Printf(" - %s", f)
		}
	}
	logger.Println("=======================================")
}

// sameDay kiểm tra 2 thời điểm có cùng ngày không
func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}
