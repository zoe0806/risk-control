package workflow

import (
	"sync"
	"time"
)

// CircuitBreaker 深度引擎熔断（连续失败后短时开路，回退本地引擎）。
type CircuitBreaker struct {
	mu        sync.Mutex
	failures  int
	threshold int
	openUntil time.Time
	openFor   time.Duration
	totalOpen int64
}

func NewCircuitBreaker(threshold int, openFor time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 5
	}
	if openFor <= 0 {
		openFor = 30 * time.Second
	}
	return &CircuitBreaker{threshold: threshold, openFor: openFor}
}

func (c *CircuitBreaker) Allow() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.openUntil) {
		return false
	}
	return true
}

func (c *CircuitBreaker) Success() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
}

func (c *CircuitBreaker) Fail() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= c.threshold {
		c.openUntil = time.Now().Add(c.openFor)
		c.failures = 0
		c.totalOpen++
	}
}

func (c *CircuitBreaker) Stats() map[string]any {
	if c == nil {
		return map[string]any{"enabled": false}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{
		"enabled":     true,
		"failures":    c.failures,
		"threshold":   c.threshold,
		"open":        time.Now().Before(c.openUntil),
		"open_until":  c.openUntil,
		"total_opens": c.totalOpen,
	}
}
