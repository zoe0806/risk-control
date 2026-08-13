package workflow

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"risk_control/tools"
)

type einoRuntime struct {
	cb    compose.Runnable[*tools.PipelineState, *tools.PipelineState]
	stock compose.Runnable[*tools.StockPipelineState, *tools.StockPipelineState]
}

func newEinoRuntime(ctx context.Context, deps *GraphDeps) (DeepRuntime, error) {
	cb, err := BuildCrossBorderDeepGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("cb deep graph: %w", err)
	}
	stock, err := BuildStockDeepGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("stock deep graph: %w", err)
	}
	return &einoRuntime{cb: cb, stock: stock}, nil
}

func (r *einoRuntime) Name() string { return DeepRuntimeEino }

func (r *einoRuntime) Invoke(ctx context.Context, in DeepInput) (DeepOutput, error) {
	invokeCtx, _ := WithRunTrace(ctx)
	opts := InvokeScreeningOptions()
	switch in.Domain {
	case tools.BusinessCrossBorder:
		st := hydrateCBState(in)
		out, err := r.cb.Invoke(invokeCtx, st, opts...)
		if err != nil {
			return DeepOutput{}, err
		}
		return deepOutputFromCB(out, r.Name()), nil
	case tools.BusinessStock:
		st := hydrateStockState(in)
		out, err := r.stock.Invoke(invokeCtx, st, opts...)
		if err != nil {
			return DeepOutput{}, err
		}
		return deepOutputFromStock(out, r.Name()), nil
	default:
		return DeepOutput{}, fmt.Errorf("eino runtime: unknown domain %q", in.Domain)
	}
}
