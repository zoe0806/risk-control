package batch

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/compose"

	"risk_control/config"
	"risk_control/domain"
	"risk_control/workflow"
)

// ScreenConcurrent 批处理推理：限制并发以降低 API 突发成本。
func ScreenConcurrent(ctx context.Context, run compose.Runnable[domain.ScreeningRequest, domain.ScreeningResult], reqs []domain.ScreeningRequest, opts ...compose.Option) ([]domain.ScreeningResult, []error) {
	workers := config.Load().Workers
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
				invokeCtx, _ := workflow.WithRunTrace(ctx)
				r, err := run.Invoke(invokeCtx, reqs[idx], opts...)
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
