package workflow

import (
	"sync"
	"time"
)

// VelocityTracker 进程内滑动窗口计数（阶段1：无 Redis 时的演示实现）。
type VelocityTracker struct {
	mu      sync.Mutex
	windows map[string]*velWindow
}

type velWindow struct {
	times []time.Time
}

func NewVelocityTracker() *VelocityTracker {
	return &VelocityTracker{windows: make(map[string]*velWindow)}
}

// Hit 记录一次并返回窗口内次数（含本次）。
func (v *VelocityTracker) Hit(key string, window time.Duration) int {
	if v == nil || key == "" {
		return 0
	}
	now := time.Now()
	cutoff := now.Add(-window)
	v.mu.Lock()
	defer v.mu.Unlock()
	w, ok := v.windows[key]
	if !ok {
		w = &velWindow{}
		v.windows[key] = w
	}
	kept := w.times[:0]
	for _, t := range w.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	w.times = kept
	return len(w.times)
}
