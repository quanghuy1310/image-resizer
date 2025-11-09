package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

var stopFlag int32

func main() {
	// Load config
	cfg := LoadConfig()

	// Setup logger
	setupLogger(cfg)

	// Init history logger (resize_history.log)
	if err := os.MkdirAll(filepath.Dir(cfg.ResizeHistoryLog), 0755); err != nil {
		logger.Fatalf("[FATAL] Cannot create history log directory: %v", err)
	}

	hf, err := os.OpenFile(cfg.ResizeHistoryLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Fatalf("[FATAL] Cannot open history log file: %v", err)
	}
	historyLogger = log.New(hf, "", log.LstdFlags|log.Lmicroseconds)
	logger.Printf("[INFO] History logger initialized: %s", cfg.ResizeHistoryLog)
	defer func() {
		_ = hf.Close()
		logger.Println("[INFO] History logger closed.")
	}()
	defer logger.Println("[INFO] Logger closed.")

	// Context with cancel
	ctx, cancel := context.WithCancel(context.Background())

	// OS signal watcher
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Printf("[INFO] Caught signal: %v — shutting down...", sig)
		atomic.StoreInt32(&stopFlag, 1)
		cancel()
	}()

	// stop.flag watcher
	go func() {
		flagPath := filepath.Join(cfg.BaseDir, "stop.flag")
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(flagPath); err == nil {
					logger.Println("[INFO] Detected stop.flag, shutting down...")
					atomic.StoreInt32(&stopFlag, 1)
					cancel()
					return
				}
			}
		}
	}()

	// Scheduler check
	if cfg.EnableScheduler {
		logger.Println("[INFO] Scheduler ENABLED. Will run at configured time.")
		go RunScheduler(ctx, cfg)
	} else {
		logger.Println("[INFO] Scheduler DISABLED. Running immediately using PROCESS_MODE =", cfg.ProcessMode)
		if err := ProcessFolderTree(cfg); err != nil {
			if err == ErrStop {
				logger.Println("[INFO] Stopped by request.")
			} else {
				logger.Printf("[ERROR] Processing failed: %v", err)
			}
		}
	}

	<-ctx.Done()
	logger.Println("[INFO] Service exited cleanly.")
}
