package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	BaseDir          string
	ResizeWidth      int
	ResizeHeight     int
	LogFile          string
	LogMaxMB         int64
	Threads          int
	ScheduleHour     int
	ScheduleMin      int
	Quality          int
	Verbose          bool
	BatchSize        int
	ResizeHistoryLog string
	ImageTimeoutSec  int
	ImageRetry       int
	ProcessMode      string
	EnableScheduler  bool
}

func LoadConfig() Config {
	_ = godotenv.Load(".env") // ignore error if no .env

	cfg := Config{
		BaseDir:          getEnv("BASE_DIR", "D:\\File01\\DATA"),
		ResizeWidth:      getEnvInt("RESIZE_WIDTH", 1824),
		ResizeHeight:     getEnvInt("RESIZE_HEIGHT", 1560),
		LogFile:          getEnv("LOG_FILE", "D:\\ImageResize\\resize.log"),
		LogMaxMB:         getEnvInt64("LOG_MAX_MB", 100),
		Threads:          getEnvInt("THREADS", 16),
		ScheduleHour:     getEnvInt("SCHEDULE_HOUR", 2),
		ScheduleMin:      getEnvInt("SCHEDULE_MINUTE", 0),
		Quality:          getEnvInt("QUALITY", 90),
		Verbose:          getEnvBool("VERBOSE", false),
		BatchSize:        getEnvInt("BATCH_SIZE", 16),
		ResizeHistoryLog: getEnv("RESIZE_HISTORY_LOG", "D:\\ImageResize\\resize_history.log"),
		ImageTimeoutSec:  getEnvInt("IMAGE_TIMEOUT_SEC", 20),
		ImageRetry:       getEnvInt("IMAGE_RETRY", 1),
		ProcessMode:      getEnv("PROCESS_MODE", "ordered"),
		EnableScheduler:  getEnvBool("ENABLE_SCHEDULER", false),
	}

	// Debug log to confirm values
	log.Printf("[CONFIG] Loaded configuration: %+v", cfg)
	return cfg
}

func getEnv(key, defaultVal string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	val := strings.TrimSpace(os.Getenv(key))
	if val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			return b
		}
	}
	return defaultVal
}
