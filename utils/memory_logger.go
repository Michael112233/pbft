package utils

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const memoryLogTimestampFormat = "2006-01-02 15:04:05.000000"

func StartMemoryLogger(path string, component string, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer func() {
		if done != nil {
			close(done)
		}
	}()
	if path == "" {
		return
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return
	}
	defer file.Close()

	memLog := log.New(file, "[MEM] ", 0)
	logMemoryStats(memLog, component)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			logMemoryStats(memLog, component)
		case <-stop:
			return
		}
	}
}

func logMemoryStats(log *log.Logger, component string) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	log.Printf(
		"%s component=%s alloc_mb=%.2f total_alloc_mb=%.2f sys_mb=%.2f heap_alloc_mb=%.2f heap_inuse_mb=%.2f heap_idle_mb=%.2f heap_released_mb=%.2f heap_objects=%d num_gc=%d pause_total_ms=%.2f goroutines=%d",
		time.Now().Format(memoryLogTimestampFormat),
		component,
		bytesToMiB(stats.Alloc),
		bytesToMiB(stats.TotalAlloc),
		bytesToMiB(stats.Sys),
		bytesToMiB(stats.HeapAlloc),
		bytesToMiB(stats.HeapInuse),
		bytesToMiB(stats.HeapIdle),
		bytesToMiB(stats.HeapReleased),
		stats.HeapObjects,
		stats.NumGC,
		float64(stats.PauseTotalNs)/1e6,
		runtime.NumGoroutine(),
	)
}

func bytesToMiB(v uint64) float64 {
	return float64(v) / 1024 / 1024
}
