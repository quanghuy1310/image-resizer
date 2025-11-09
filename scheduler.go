package main

import (
	"context"
	"time"
)

func RunScheduler(ctx context.Context, cfg Config) {
	if !cfg.EnableScheduler {
		logger.Println("[INFO] Scheduler disabled.")
		return
	}

	for {
		now := time.Now()
		nextRun := time.Date(
			now.Year(), now.Month(), now.Day(),
			cfg.ScheduleHour, cfg.ScheduleMin, 0, 0, now.Location(),
		)
		if nextRun.Before(now) {
			nextRun = nextRun.Add(24 * time.Hour)
		}

		wait := nextRun.Sub(now)
		logger.Printf("[INFO] Next run: %s (%v)", nextRun.Format("2006-01-02 15:04:05"), wait)

		select {
		case <-time.After(wait):
			ProcessFolderTree(cfg)
			RotateLogIfNeeded(cfg)
		case <-ctx.Done():
			return
		}
	}
}
