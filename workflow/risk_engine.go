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

// RiskEngine 多业务风控：通用编排器 + 域 Profile + 深度图。
type RiskEngine struct {
	stockGraph compose.Runnable[tools.StockOrder, tools.ScreeningResult]
	cbGraph    compose.Runnable[tools.CrossBorderTransaction, tools.ScreeningResult]
	store      store.Store
	router     *llm.Router
	cfg        config.Config
	velocity   *VelocityTracker
	policies   *PolicyHub
	policyReg  *PolicyRegistry
	profiles   map[string]DomainProfile
	graph      *EntityGraph
	breaker    *CircuitBreaker
	metrics    *EngineMetrics
}

// NewRiskEngine 编译各域深度图并注册 DomainProfile。
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
	reg := deps.Policies.Registry()
	orch := reg.Primary(tools.BusinessCrossBorder).Orchestrator

	stockGraph, err := BuildStockRiskGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("stock risk graph: %w", err)
	}
	cbGraph, err := BuildCrossBorderRiskGraph(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("cross border risk graph: %w", err)
	}
	eng := &RiskEngine{
		stockGraph: stockGraph,
		cbGraph:    cbGraph,
		store:      deps.Store,
		router:     deps.Router,
		cfg:        deps.Cfg,
		velocity:   deps.Velocity,
		policies:   deps.Policies,
		policyReg:  reg,
		profiles: map[string]DomainProfile{
			tools.BusinessCrossBorder: crossBorderProfile{},
			tools.BusinessStock:       stockProfile{},
		},
		graph:   deps.Graph,
		breaker: NewCircuitBreaker(orch.CircuitFailureThreshold, time.Duration(orch.CircuitOpenSec)*time.Second),
		metrics: &EngineMetrics{},
	}
	return eng, nil
}

func (e *RiskEngine) Store() store.Store {
	if e == nil {
		return nil
	}
	return e.store
}

func (e *RiskEngine) Policies() *PolicyHub {
	if e == nil {
		return nil
	}
	return e.policies
}

func (e *RiskEngine) PolicyRegistry() *PolicyRegistry {
	if e == nil {
		return nil
	}
	return e.policyReg
}

func (e *RiskEngine) Metrics() map[string]any {
	if e == nil {
		return nil
	}
	out := map[string]any{
		"engines": e.metrics.Snapshot(),
		"circuit": e.breaker.Stats(),
	}
	if e.policyReg != nil {
		out["policies"] = e.policyReg.Snapshot()
	}
	return out
}

// EvaluateScreeningRequest 统一入口：按 business_type 选择 DomainProfile 走通用编排。
func (e *RiskEngine) EvaluateScreeningRequest(ctx context.Context, req tools.ScreeningRequest, opts ...compose.Option) (tools.ScreeningResult, error) {
	kind, err := req.ResolveBusinessType()
	if err != nil {
		return tools.ScreeningResult{}, err
	}
	if err := req.ValidatePayload(kind); err != nil {
		return tools.ScreeningResult{}, err
	}
	profile, ok := e.profiles[kind]
	if !ok {
		return tools.ScreeningResult{}, fmt.Errorf("no domain profile for %q", kind)
	}
	switch kind {
	case tools.BusinessStock:
		return e.Orchestrate(ctx, profile, req.StockOrder, opts...)
	case tools.BusinessCrossBorder:
		return e.Orchestrate(ctx, profile, req.Transaction, opts...)
	default:
		return tools.ScreeningResult{}, fmt.Errorf("unreachable business kind %q", kind)
	}
}
