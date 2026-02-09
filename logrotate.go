package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	logMutex    sync.Mutex
	logFile     *os.File
	historyFile *os.File
	// atomic-backed writers used by loggers so we can swap underlying file safely
	mainAtomicWriter    *AtomicWriter
	historyAtomicWriter *AtomicWriter
)

// AtomicWriter forwards Write calls to an underlying io.Writer stored in an atomic.Value.
// The underlying writer can be swapped at runtime via Store without introducing races
// between writer swaps and concurrent writes.
type AtomicWriter struct {
	v atomic.Value // holds io.Writer
}

func NewAtomicWriter() *AtomicWriter {
	return &AtomicWriter{}
}

func (aw *AtomicWriter) Write(p []byte) (int, error) {
	wi := aw.v.Load()
	if wi == nil {
		return 0, nil
	}
	w := wi.(io.Writer)
	return w.Write(p)
}

func (aw *AtomicWriter) Store(w io.Writer) {
	aw.v.Store(w)
}

func (aw *AtomicWriter) Get() io.Writer {
	wi := aw.v.Load()
	if wi == nil {
		return nil
	}
	return wi.(io.Writer)
}

// Setup logger + history logger
func setupLogger(cfg Config) {
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o755); err != nil {
		// Fallback to stderr if log file fails
		log.SetOutput(os.Stderr)
		return
	}
	f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	// Initialize atomic writer(s) without pre-storing (to avoid type mismatch panic)
	mainAtomicWriter = NewAtomicWriter()
	historyAtomicWriter = NewAtomicWriter()

	if err == nil {
		logFile = f
		mainAtomicWriter.Store(f)
	}
	logger = log.New(mainAtomicWriter, "", log.LstdFlags|log.Lmicroseconds)
	// History log setup is handled in main() specifically now
}

// Rotate main log
func RotateLogIfNeeded(cfg Config) {
	logMutex.Lock()
	defer logMutex.Unlock()

	if logFile == nil {
		return
	}

	// Check handle stats first (fast)
	info, err := logFile.Stat()
	if err != nil {
		return
	}
	limitBytes := int64(cfg.LogMaxMB) * 1024 * 1024
	if info.Size() < limitBytes {
		return
	}

	// --- FIX: Double check physical file path ---
	// Ngăn chặn trường hợp handle trỏ vào file cũ nhưng file trên đĩa đã bị process khác xoay
	statPath, errPath := os.Stat(cfg.LogFile)
	if errPath == nil && statPath.Size() < limitBytes {
		// File trên đĩa thực tế nhỏ hơn limit (đã được xoay bởi ai đó), refresh handle
		_ = logFile.Close()
		logFile, _ = os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		logger.SetOutput(logFile)
		return
	}

	// Rotate: close current file, rename, and open new file, then atomically swap writer
	backup := fmt.Sprintf("%s.%s.bak", cfg.LogFile, time.Now().Format("20060102_150405"))
	if logFile != nil {
		_ = logFile.Close()
	}
	_ = os.Rename(cfg.LogFile, backup)

	newF, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		logFile = newF
		mainAtomicWriter.Store(newF)
	}
	logger.Printf("[INFO] Rotated main log -> %s", filepath.Base(backup))
}

// Rotate history log
func RotateHistoryLogIfNeeded(cfg Config) {
	logMutex.Lock()
	defer logMutex.Unlock()

	if historyFile == nil {
		return
	}

	info, err := historyFile.Stat()
	if err != nil {
		return
	}
	limitBytes := int64(cfg.LogMaxMB) * 1024 * 1024

	if info.Size() < limitBytes {
		return
	}

	// Double-check file on disk
	fileOnDisk, errDisk := os.Stat(cfg.ResizeHistoryLog)
	if errDisk == nil {
		if fileOnDisk.Size() < limitBytes {
			// reopen and point writer to current file
			_ = historyFile.Close()
			hf, _ := os.OpenFile(cfg.ResizeHistoryLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if hf != nil {
				historyFile = hf
				historyAtomicWriter.Store(hf)
			}
			return
		}
	}

	backup := fmt.Sprintf("%s.%s.bak", cfg.ResizeHistoryLog, time.Now().Format("20060102_150405"))
	if historyFile != nil {
		_ = historyFile.Close()
	}
	err = os.Rename(cfg.ResizeHistoryLog, backup)
	if err != nil {
		logger.Printf("[WARN] Rename history failed: %v", err)
	}
	hf, err := os.OpenFile(cfg.ResizeHistoryLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logger.Printf("[WARN] cannot reopen history log: %v", err)
		return
	}
	historyFile = hf
	historyAtomicWriter.Store(hf)

	logger.Printf("[INFO] Rotated history log -> %s", filepath.Base(backup))
}

// CleanUpOldLogs (Giữ nguyên)
func CleanUpOldLogs(cfg Config) {
	const retentionDays = 7
	dir := filepath.Dir(cfg.LogFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	threshold := time.Now().AddDate(0, 0, -retentionDays)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".bak") && (strings.Contains(name, "resize") || strings.Contains(name, filepath.Base(cfg.LogFile))) {
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(threshold) {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}
