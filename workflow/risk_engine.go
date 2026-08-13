package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"

	"risk_control/config"
	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
)

// GraphDeps 注入存储与模型路由，便于单测替换。
type GraphDeps struct {
	Store    store.Store
	Router   *llm.Router
	Cfg      config.Config
	Velocity *VelocityTracker
	Policies *PolicyHub
	Graph    *EntityGraph
}

// RiskEngine 多业务风控：编排器 + 各域 Runnable。
type RiskEngine struct {
	stockGraph compose.Runnable[tools.StockOrder, tools.ScreeningResult]
	cbGraph    compose.Runnable[tools.CrossBorderTransaction, tools.ScreeningResult]
	store      store.Store
	router     *llm.Router
	cfg        config.Config
	velocity   *VelocityTracker
	policies   *PolicyHub
	graph      *EntityGraph
	breaker    *CircuitBreaker
	metrics    *EngineMetrics
}

// NewRiskEngine 基于共享依赖编译股票图与跨境图，并启动编排组件。
func NewRiskEngine(ctx context.Context, deps *GraphDeps) (*RiskEngine, error) {
	if deps == nil {
		return nil, fmt.Errorf("graph deps is nil")
	}
	if deps.Velocity == nil {
		deps.Velocity = NewVelocityTracker()
	}
	if deps.Policies == nil {
		deps.Policies = NewPolicyHub(deps.Cfg)
	}
	if deps.Graph == nil {
		deps.Graph = NewEntityGraph()
	}
	// 若配置了主策略包路径，启动时加载覆盖
	if deps.Cfg.PolicyPackPath != "" {
		if _, err := deps.Policies.ReloadPrimary(deps.Cfg.PolicyPackPath); err != nil {
			return nil, fmt.Errorf("load policy pack: %w", err)
		}
	}
	if deps.Cfg.ShadowPackPath != "" {
		if _, err := deps.Policies.ReloadShadow(deps.Cfg.ShadowPackPath); err != nil {
			// 影子失败不阻断启动
			fmt.Printf("warn: shadow pack: %v\n", err)
		}
	}
	orch := deps.Policies.Primary().Orchestrator
	stockGraph, err := BuildStockRiskGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("stock risk graph: %w", err)
	}
	cbGraph, err := BuildCrossBorderRiskGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("cross border risk graph: %w", err)
	}
	return &RiskEngine{
		stockGraph: stockGraph,
		cbGraph:    cbGraph,
		store:      deps.Store,
		router:     deps.Router,
		cfg:        deps.Cfg,
		velocity:   deps.Velocity,
		policies:   deps.Policies,
		graph:      deps.Graph,
		breaker:    NewCircuitBreaker(orch.CircuitFailureThreshold, time.Duration(orch.CircuitOpenSec)*time.Second),
		metrics:    &EngineMetrics{},
	}, nil
}

// Store 暴露案例工作流等读写。
func (e *RiskEngine) Store() store.Store {
	if e == nil {
		return nil
	}
	return e.store
}

// Policies 策略热更新入口。
func (e *RiskEngine) Policies() *PolicyHub {
	if e == nil {
		return nil
	}
	return e.policies
}

// Metrics 引擎计数。
func (e *RiskEngine) Metrics() map[string]any {
	if e == nil {
		return nil
	}
	out := map[string]any{
		"engines": e.metrics.Snapshot(),
		"circuit": e.breaker.Stats(),
	}
	if e.policies != nil {
		if p := e.policies.Primary(); p != nil {
			out["pack_version"] = p.Version
		}
	}
	return out
}

// EvaluateScreeningRequest 统一入口：股票走原图；跨境走多引擎编排。
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
		return e.stockGraph.Invoke(ctx, req.StockOrder, opts...)
	case tools.BusinessCrossBorder:
		return e.OrchestrateCrossBorder(ctx, req.Transaction, opts...)
	default:
		return tools.ScreeningResult{}, fmt.Errorf("unreachable business kind %q", kind)
	}
}
