package workflow

import (
	"context"
	"fmt"
	"time"

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

// RiskEngine 多业务风控：通用编排器 + 域 Profile + 可插拔深度 runtime。
type RiskEngine struct {
	store     store.Store
	router    *llm.Router
	cfg       config.Config
	velocity  *VelocityTracker
	policies  *PolicyHub
	policyReg *PolicyRegistry
	profiles  map[string]DomainProfile
	graph     *EntityGraph
	breaker   *CircuitBreaker
	metrics   *EngineMetrics
	deep      DeepRuntime
}

// NewRiskEngine 注册 DomainProfile 并按配置装配深度 runtime（默认 native，不再编译本地闸门图）。
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
	if deps.Store == nil {
		deps.Store = store.Noop{}
	}
	reg := deps.Policies.Registry()
	orch := reg.Primary(tools.BusinessCrossBorder).Orchestrator

	kind := NormalizeDeepKind(deps.Cfg.DeepRuntime.Kind)
	if deepRuntimeNeedsLLM(kind) && deps.Router == nil {
		return nil, fmt.Errorf("llm router required for deep runtime %q", kind)
	}

	deep, err := NewDeepRuntime(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("deep runtime: %w", err)
	}

	eng := &RiskEngine{
		store:     deps.Store,
		router:    deps.Router,
		cfg:       deps.Cfg,
		velocity:  deps.Velocity,
		policies:  deps.Policies,
		policyReg: reg,
		profiles: map[string]DomainProfile{
			tools.BusinessCrossBorder: crossBorderProfile{},
			tools.BusinessStock:       stockProfile{},
		},
		graph:   deps.Graph,
		breaker: NewCircuitBreaker(orch.CircuitFailureThreshold, time.Duration(orch.CircuitOpenSec)*time.Second),
		metrics: &EngineMetrics{},
		deep:    deep,
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

func (e *RiskEngine) DeepRuntimeName() string {
	if e == nil || e.deep == nil {
		return DeepRuntimeOff
	}
	return e.deep.Name()
}

func (e *RiskEngine) Metrics() map[string]any {
	if e == nil {
		return nil
	}
	out := map[string]any{
		"engines":      e.metrics.Snapshot(),
		"circuit":      e.breaker.Stats(),
		"deep_runtime": e.DeepRuntimeName(),
	}
	if e.policyReg != nil {
		out["policies"] = e.policyReg.Snapshot()
	}
	return out
}

// EvaluateScreeningRequest 统一入口：按 business_type 选择 DomainProfile 走通用编排。
func (e *RiskEngine) EvaluateScreeningRequest(ctx context.Context, req tools.ScreeningRequest) (tools.ScreeningResult, error) {
	return e.evaluate(ctx, req, false)
}

// EvaluateLocal 仅本地引擎（规则/图/轻量/仲裁），供 riskctl eval 与策略 golden。
func (e *RiskEngine) EvaluateLocal(ctx context.Context, req tools.ScreeningRequest) (tools.ScreeningResult, error) {
	return e.evaluate(ctx, req, true)
}

func (e *RiskEngine) evaluate(ctx context.Context, req tools.ScreeningRequest, skipDeep bool) (tools.ScreeningResult, error) {
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
		return e.Orchestrate(ctx, profile, req.StockOrder, skipDeep)
	case tools.BusinessCrossBorder:
		return e.Orchestrate(ctx, profile, req.Transaction, skipDeep)
	default:
		return tools.ScreeningResult{}, fmt.Errorf("unreachable business kind %q", kind)
	}
}
