package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	logger   *log.Logger
	logMutex sync.Mutex
	logFile  *os.File
)

func setupLogger(cfg Config) {
	var err error
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: cannot create log directory: %v\n", err)
		os.Exit(1)
	}
	logFile, err = os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: cannot open log file: %v\n", err)
		os.Exit(1)
	}
	logger = log.New(logFile, "", log.LstdFlags|log.Lmicroseconds)
}

func RotateLogIfNeeded(cfg Config) {
	logMutex.Lock()
	defer logMutex.Unlock()

	info, err := os.Stat(cfg.LogFile)
	if err != nil {
		return
	}

	sizeMB := info.Size() / (1024 * 1024)
	if sizeMB < cfg.LogMaxMB {
		return
	}

	backupName := fmt.Sprintf("%s.%s.bak", cfg.LogFile, time.Now().Format("20060102_150405"))
	if err := os.Rename(cfg.LogFile, backupName); err != nil {
		logger.Printf("[WARN] Cannot rotate log: %v", err)
		return
	}

	_ = logFile.Close()
	logFile, err = os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: cannot reopen log file: %v\n", err)
		os.Exit(1)
	}
	logger.SetOutput(logFile)
	logger.Printf("[INFO] Rotated log — old file: %s", filepath.Base(backupName))
}
