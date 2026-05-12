package workflow

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"risk_control/config"
	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
)

// GraphDeps 注入存储与模型路由，便于单测替换。
type GraphDeps struct {
	Store  store.Store
	Router *llm.Router
	Cfg    config.Config
}

// RiskEngine 多业务风控：各域独立 Runnable，通过类型安全的方法对外暴露。
type RiskEngine struct {
	stockGraph compose.Runnable[tools.StockOrder, tools.ScreeningResult]
	cbGraph    compose.Runnable[tools.ScreeningRequest, tools.ScreeningResult]
}

// NewRiskEngine 基于共享依赖编译股票图与跨境图。
func NewRiskEngine(ctx context.Context, deps *GraphDeps) (*RiskEngine, error) {
	if deps == nil {
		return nil, fmt.Errorf("graph deps is nil")
	}
	stockGraph, err := BuildStockRiskGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("stock risk graph: %w", err)
	}
	cbGraph, err := BuildCrossBorderRiskGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("cross border risk graph: %w", err)
	}
	return &RiskEngine{stockGraph: stockGraph, cbGraph: cbGraph}, nil
}

// EvaluateStockOrder 股票域执行入口（对外与跨境一致为 ScreeningResult）。
func (e *RiskEngine) EvaluateStockOrder(ctx context.Context, order tools.StockOrder, opts ...compose.Option) (tools.ScreeningResult, error) {
	return e.stockGraph.Invoke(ctx, order, opts...)
}

// EvaluateCrossBorderTransaction 跨境域执行入口（单笔交易体，内部包装为 ScreeningRequest）。
func (e *RiskEngine) EvaluateCrossBorderTransaction(ctx context.Context, txn tools.CrossBorderTransaction, opts ...compose.Option) (tools.ScreeningResult, error) {
	return e.cbGraph.Invoke(ctx, tools.NewCrossBorderScreeningRequest(txn), opts...)
}

// EvaluateScreeningRequest 统一入口：解析 business_type、校验负载后分发到股票或跨境图，直接返回 ScreeningResult。
func (e *RiskEngine) EvaluateScreeningRequest(ctx context.Context, req tools.ScreeningRequest, opts ...compose.Option) (tools.ScreeningResult, error) {
	kind, err := req.ResolveBusinessType()
	if err != nil {
		return tools.ScreeningResult{}, err
	}
	if err := req.ValidatePayload(kind); err != nil {
		return tools.ScreeningResult{}, err
	}
	switch kind {
	case tools.BusinessStock:
		return e.EvaluateStockOrder(ctx, req.StockOrder, opts...)
	case tools.BusinessCrossBorder:
		return e.cbGraph.Invoke(ctx, req.ForCrossBorderGraph(), opts...)
	default:
		return tools.ScreeningResult{}, fmt.Errorf("unreachable business kind %q", kind)
	}
}

// CrossBorderRunnable 供批处理等需要 compose.Runnable 的场景。
func (e *RiskEngine) CrossBorderRunnable() compose.Runnable[tools.ScreeningRequest, tools.ScreeningResult] {
	return e.cbGraph
}
