package main

import (
	"errors"
	"io/fs"
	"log"
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

// ------------------------------------------------------------
// STRUCT & GLOBALS
// ------------------------------------------------------------
type FolderDate struct {
	Path  string
	Year  int
	Month int
	Day   int
}

var ErrStop = errors.New("stop requested")

var historyLogger *log.Logger
var historyMu sync.Mutex

// Serialize all bimg calls (libvips thread-unsafe workaround)
var bimgMutex sync.Mutex

// ------------------------------------------------------------
// THREAD-SAFE HISTORY WRITE
// ------------------------------------------------------------
func historyWrite(line string) {
	if historyLogger == nil {
		return
	}
	historyMu.Lock()
	historyLogger.Println(line)
	historyMu.Unlock()
}

// ------------------------------------------------------------
// STOP FLAG CHECK
// ------------------------------------------------------------
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

// ------------------------------------------------------------
// PARSE DATE FOLDER
// ------------------------------------------------------------
func parseFolderDate(rel string) (FolderDate, bool) {
	parts := strings.Split(rel, string(os.PathSeparator))
	for i := 0; i < len(parts)-2; i++ {
		y, errY := strconv.Atoi(parts[i])
		m, errM := strconv.Atoi(parts[i+1])
		d, errD := strconv.Atoi(parts[i+2])
		if errY == nil && errM == nil && errD == nil {
			return FolderDate{
				Path: filepath.Join(parts[:i+3]...),
				Year: y, Month: m, Day: d,
			}, true
		}
	}
	return FolderDate{}, false
}

// ------------------------------------------------------------
// SKIP IF IMAGE ALREADY AT TARGET RESOLUTION
// ------------------------------------------------------------
func isImageSkip(path string, cfg Config) bool {
	buf, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	size, err := bimg.NewImage(buf).Size()
	if err != nil {
		return false
	}
	return size.Width == cfg.ResizeWidth && size.Height == cfg.ResizeHeight
}

// ------------------------------------------------------------
// RESIZE SINGLE IMAGE (SERIALIZED WITH MUTEX)
// ------------------------------------------------------------
func ResizeImage(path string, cfg Config) error {
	if !strings.HasPrefix(filepath.Base(path), "Full_") {
		return nil
	}

	if isImageSkip(path, cfg) {
		logger.Printf("[SKIP] Resolution OK: %s", path)
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
			logger.Printf("[ERROR] Read failed (%s): %v", path, err)
			historyWrite("[ERROR:READ] " + path + " | " + err.Error())
			continue
		}
		if len(buf) == 0 {
			lastErr = errors.New("empty file")
			logger.Printf("[ERROR] Empty file: %s", path)
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

		var out []byte
		func() {
			bimgMutex.Lock()
			defer bimgMutex.Unlock()
			newImg, err := bimg.NewImage(buf).Process(opts)
			if err != nil {
				lastErr = err
				return
			}
			out = newImg
		}()

		if out != nil {
			if err := os.WriteFile(path, out, 0644); err != nil {
				lastErr = err
				logger.Printf("[ERROR] Write failed (%s): %v", path, err)
				historyWrite("[ERROR:WRITE] " + path + " | " + err.Error())
				continue
			}
			historyWrite("[OK] " + path)
			return nil
		}

		if lastErr != nil {
			logger.Printf("[WARN] Retry %d failed for %s: %v", attempt, path, lastErr)
			historyWrite("[ERROR:PROCESS] " + path + " | " + lastErr.Error())
			time.Sleep(500 * time.Millisecond)
		}
	}

	historyWrite("[FAILED] " + path + " | " + lastErr.Error())
	return lastErr
}

// ------------------------------------------------------------
// SPLIT FILES INTO CHUNKS
// ------------------------------------------------------------
func chunkFiles(files []string, batchSize int) [][]string {
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

// ------------------------------------------------------------
// PROCESS SINGLE FOLDER (THREAD-SAFE + ETA)
// ------------------------------------------------------------
func processFolder(folder string, cfg Config) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Printf("[PANIC] Folder %s: %v\n%s", folder, r, debug.Stack())
		}
	}()

	files, _ := filepath.Glob(filepath.Join(folder, "Full_*.jpg"))
	total := int64(len(files))
	if total == 0 {
		return nil
	}

	logger.Printf("[FOLDER] Start: %s | %d images", folder, total)
	start := time.Now()
	var processed int64

	filesCh := make(chan []string, cfg.Threads)
	var wg sync.WaitGroup

	stopETA := make(chan struct{})
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p := atomic.LoadInt64(&processed)
				if p == 0 {
					logger.Printf("[PROGRESS] %s | 0/%d ...", folder, total)
					continue
				}
				elapsed := time.Since(start).Seconds()
				speed := float64(p) / elapsed
				eta := time.Duration(float64(total-p)/speed) * time.Second

				logger.Printf(
					"[PROGRESS] %s | %d/%d | %.2f img/s | ETA %v",
					folder, p, total, speed, eta,
				)

			case <-stopETA:
				return
			}
		}
	}()

	for i := 0; i < cfg.Threads; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer logger.Printf("[WORKER] %d exit", id)

			for batch := range filesCh {
				if checkStopRequested(cfg) {
					return
				}
				for _, f := range batch {
					if checkStopRequested(cfg) {
						return
					}
					if err := ResizeImage(f, cfg); err == nil {
						atomic.AddInt64(&processed, 1)
					}
				}
			}
		}(i)
	}

	for _, batch := range chunkFiles(files, cfg.BatchSize) {
		if checkStopRequested(cfg) {
			break
		}
		filesCh <- batch
	}
	close(filesCh)
	wg.Wait()
	close(stopETA)

	logger.Printf("[FOLDER] Completed: %s | %v", folder, time.Since(start))
	return nil
}

// ------------------------------------------------------------
// PROCESS FOLDER TREE (ORDERED OR STREAM)
// ------------------------------------------------------------
func ProcessFolderTree(cfg Config) error {
	logger.Printf("[SCAN] Scanning: %s", cfg.BaseDir)

	var folderDates []FolderDate

	filepath.WalkDir(cfg.BaseDir, func(path string, d fs.DirEntry, err error) error {
		if checkStopRequested(cfg) {
			return ErrStop
		}
		if path == cfg.BaseDir || !d.IsDir() {
			return nil
		}

		rel, _ := filepath.Rel(cfg.BaseDir, path)
		if cfg.ProcessMode == "stream" {
			return processFolder(path, cfg)
		}

		if fd, ok := parseFolderDate(rel); ok {
			fd.Path = path
			folderDates = append(folderDates, fd)
		}

		return nil
	})

	if cfg.ProcessMode == "ordered" {
		sort.Slice(folderDates, func(i, j int) bool {
			if folderDates[i].Year != folderDates[j].Year {
				return folderDates[i].Year < folderDates[j].Year
			}
			if folderDates[i].Month != folderDates[j].Month {
				return folderDates[i].Month < folderDates[j].Month
			}
			return folderDates[i].Day < folderDates[j].Day
		})
	}

	for _, fd := range folderDates {
		if checkStopRequested(cfg) {
			return ErrStop
		}
		processFolder(fd.Path, cfg)
	}

	return nil
}
