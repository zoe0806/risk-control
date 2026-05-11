package batch

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/compose"

	"risk_control/domain"
)

// ScreenConcurrent 批处理推理：限制并发以降低 API 突发成本（演示级 worker pool）。
func ScreenConcurrent(ctx context.Context, run compose.Runnable[domain.ScreeningRequest, domain.ScreeningResult], reqs []domain.ScreeningRequest, workers int) ([]domain.ScreeningResult, []error) {
	if workers < 1 {
		workers = 1
	}
	out := make([]domain.ScreeningResult, len(reqs))
	errs := make([]error, len(reqs))
	ch := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range ch {
				r, err := run.Invoke(ctx, reqs[idx])
				if err != nil {
					errs[idx] = err
					continue
				}
				out[idx] = r
			}
		}()
	}
	for i := range reqs {
		ch <- i
	}
	close(ch)
	wg.Wait()
	return out, errs
}
