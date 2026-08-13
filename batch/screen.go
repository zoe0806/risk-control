package batch

import (
	"context"
	"sync"

	"risk_control/config"
	"risk_control/tools"
	"risk_control/workflow"
)

// ScreenConcurrent 批处理：限制并发以降低 API 突发成本。
func ScreenConcurrent(ctx context.Context, eng *workflow.RiskEngine, reqs []tools.ScreeningRequest) ([]tools.ScreeningResult, []error) {
	workers := config.Load().Workers
	if workers < 1 {
		workers = 1
	}
	out := make([]tools.ScreeningResult, len(reqs))
	errs := make([]error, len(reqs))
	ch := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range ch {
				r, err := eng.EvaluateScreeningRequest(ctx, reqs[idx])
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
