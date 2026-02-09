package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds runtime configuration loaded from .env/env vars
type Config struct {
	BaseDir            string
	ProcessMode        string
	Processes          int
	ThreadsPerProcess  int
	BatchSize          int
	ResizeWidth        int
	ResizeHeight       int
	Quality            int
	SkipSameResolution bool
	Verbose            bool
	// ImagePrefixes is a list of filename prefixes to consider for resizing (case-insensitive)
	ImagePrefixes []string
	// DryRun when true will not perform any writes; actions are logged only
	DryRun bool

	LogFile          string
	ResizeHistoryLog string
	LogMaxMB         int64

	ImageTimeoutSec int
	ImageRetry      int

	EnableScheduler    bool
	ScheduleHour       int
	ScheduleMin        int
	SchedulerLastNDays int
}

// LoadConfig reads .env and environment variables
func LoadConfig() Config {
	_ = godotenv.Load(".env")

	get := func(key, def string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		return def
	}
	getInt := func(key string, def int) int {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
		return def
	}
	getInt64 := func(key string, def int64) int64 {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return i
			}
		}
		return def
	}
	getBool := func(key string, def bool) bool {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			l := strings.ToLower(v)
			if l == "1" || l == "true" || l == "yes" {
				return true
			}
			if l == "0" || l == "false" || l == "no" {
				return false
			}
		}
		return def
	}

	cfg := Config{
		BaseDir:            get("BASE_DIR", filepath.Join(".", "data")),
		ProcessMode:        strings.ToLower(get("PROCESS_MODE", "ordered")),
		Processes:          getInt("PROCESSES", 4),
		ThreadsPerProcess:  getInt("THREADS_PER_PROCESS", 1),
		BatchSize:          getInt("BATCH_SIZE", 16),
		ResizeWidth:        getInt("RESIZE_WIDTH", 1824),
		ResizeHeight:       getInt("RESIZE_HEIGHT", 1560),
		Quality:            getInt("QUALITY", 90),
		SkipSameResolution: getBool("SKIP_SAME_RESOLUTION", false),
		Verbose:            getBool("VERBOSE", false),
		ImagePrefixes:      parseCSVLower(get("IMAGE_PREFIXES", "Full_,Vehicle_")),
		DryRun:             getBool("DRY_RUN", false),
		LogFile:            get("LOG_FILE", filepath.Join(".", "resize.log")),
		ResizeHistoryLog:   get("RESIZE_HISTORY_LOG", filepath.Join(".", "resize_history.log")),
		LogMaxMB:           getInt64("LOG_MAX_MB", 100),
		ImageTimeoutSec:    getInt("IMAGE_TIMEOUT_SEC", 20),
		ImageRetry:         getInt("IMAGE_RETRY", 1),
		EnableScheduler:    getBool("ENABLE_SCHEDULER", false),
		ScheduleHour:       getInt("SCHEDULE_HOUR", 2),
		ScheduleMin:        getInt("SCHEDULE_MINUTE", 0),
		SchedulerLastNDays: getInt("SCHEDULER_LAST_N_DAYS", 4),
	}

	return cfg
}

// parseCSVLower splits comma-separated values and trims/lowercases each item
func parseCSVLower(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		out = append(out, strings.ToLower(t))
	}
	return out
}
