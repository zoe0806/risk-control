package llm

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// RetryConfig LLM 调用重试（指数退避 + 少量抖动）。
type RetryConfig struct {
	MaxAttempts int           // 含首次，例如 4 表示 1 次首次 + 3 次重试
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryConfig 演示默认值。
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 4,
		BaseDelay:   400 * time.Millisecond,
		MaxDelay:    8 * time.Second,
	}
}

// GenerateWithRetry 包装 BaseChatModel.Generate。
func GenerateWithRetry(ctx context.Context, m model.BaseChatModel, msgs []*schema.Message, cfg RetryConfig) (*schema.Message, error) {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 300 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 10 * time.Second
	}
	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		out, err := m.Generate(ctx, msgs)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt == cfg.MaxAttempts-1 {
			break
		}
		if !isRetriable(err) {
			return nil, err
		}
		d := backoffDelay(attempt, cfg.BaseDelay, cfg.MaxDelay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
	return nil, fmt.Errorf("llm retry exhausted after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

func isRetriable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, frag := range []string{
		"timeout", "deadline", "429", "503", "502", "connection reset",
		"eof", "temporarily", "rate limit", "too many requests",
	} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

func backoffDelay(attempt int, base, max time.Duration) time.Duration {
	d := base
	for i := 0; i < attempt; i++ {
		next := d * 2
		if next > max {
			d = max
			break
		}
		d = next
	}
	if d < base {
		d = base
	}
	jitterN := int64(d / 5)
	if jitterN < 1 {
		jitterN = 1
	}
	return d + time.Duration(rand.Int63n(jitterN))
}
