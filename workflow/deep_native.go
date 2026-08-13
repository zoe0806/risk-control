package workflow

import (
	"context"
	"fmt"

	"risk_control/tools"
)

type nativeRuntime struct {
	deps *GraphDeps
}

func newNativeRuntime(deps *GraphDeps) (DeepRuntime, error) {
	if deps == nil || deps.Router == nil {
		return nil, fmt.Errorf("native runtime requires llm router")
	}
	return &nativeRuntime{deps: deps}, nil
}

func (r *nativeRuntime) Name() string { return DeepRuntimeNative }

func (r *nativeRuntime) Invoke(ctx context.Context, in DeepInput) (DeepOutput, error) {
	switch in.Domain {
	case tools.BusinessCrossBorder:
		st := hydrateCBState(in)
		if err := runCBDeepAI(ctx, r.deps, st); err != nil {
			return DeepOutput{}, err
		}
		return deepOutputFromCB(st, r.Name()), nil
	case tools.BusinessStock:
		st := hydrateStockState(in)
		if err := runStockDeepAI(ctx, r.deps, st); err != nil {
			return DeepOutput{}, err
		}
		return deepOutputFromStock(st, r.Name()), nil
	default:
		return DeepOutput{}, fmt.Errorf("native runtime: unknown domain %q", in.Domain)
	}
}
