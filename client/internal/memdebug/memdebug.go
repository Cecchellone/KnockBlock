// Package memdebug periodically logs memory statistics, to check the RAM
// budget of CLAUDE.md §2.1 (≤ 15 MB private working set when idle).
package memdebug

import (
	"context"
	"log/slog"
	"runtime"
	"time"
)

// Run logs Go heap statistics and, where the OS provides them, process
// memory figures every interval until ctx is done.
func Run(ctx context.Context, log *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		Log(log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Log writes one line of memory statistics.
func Log(log *slog.Logger) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	attrs := []any{
		"heap_alloc_kib", ms.HeapAlloc >> 10,
		"heap_inuse_kib", ms.HeapInuse >> 10,
		"sys_kib", ms.Sys >> 10,
		"num_gc", ms.NumGC,
		"goroutines", runtime.NumGoroutine(),
	}
	if p, ok := processMemory(); ok {
		attrs = append(attrs, p.attrs()...)
	}
	log.Info("memory", attrs...)
}

// process holds OS-level memory figures in bytes; zero means unknown.
type process struct {
	workingSet        uint64 // resident set
	privateWorkingSet uint64 // Task Manager "Memory" column on Windows
	commit            uint64
}

func (p process) attrs() []any {
	var a []any
	if p.workingSet != 0 {
		a = append(a, "working_set_kib", p.workingSet>>10)
	}
	if p.privateWorkingSet != 0 {
		a = append(a, "private_working_set_kib", p.privateWorkingSet>>10)
	}
	if p.commit != 0 {
		a = append(a, "commit_kib", p.commit>>10)
	}
	return a
}
