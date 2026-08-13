package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"

	"risk_control/config"
	"risk_control/tools"
)

const (
	cbGraphName = "cb_deep_v1"

	nodeAIPrimary     = "ai_primary"
	nodeAISecondary   = "ai_secondary"
	nodeSkipSecondary = "skip_secondary"
	nodeAIReport      = "ai_report"
)

func primaryRiskThreshold(cfg config.Config) float64 {
	if cfg.PrimaryRiskScore > 0 {
		return cfg.PrimaryRiskScore
	}
	return 0.55
}

func cacheKeyForParty(party *tools.NormalizedParty, listVer string) string {
	if party == nil {
		return ""
	}
	return fmt.Sprintf("cb:%s:%s:%s", listVer, party.NormalizedKey, party.CountryNormalized)
}

// BuildCrossBorderDeepGraph 仅深度 AI：初筛 → 可选二验 → 报告。本地闸门由内核完成。
func BuildCrossBorderDeepGraph(ctx context.Context, deps *GraphDeps) (compose.Runnable[*tools.PipelineState, *tools.PipelineState], error) {
	if deps == nil || deps.Router == nil {
		return nil, fmt.Errorf("workflow deps incomplete")
	}
	thr := primaryRiskThreshold(deps.Cfg)
	g := compose.NewGraph[*tools.PipelineState, *tools.PipelineState]()

	if err := g.AddLambdaNode(nodeAIPrimary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		if err := applyCBPrimary(ctx, deps, st); err != nil {
			return nil, err
		}
		return st, nil
	}), compose.WithNodeName(nodeAIPrimary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeAISecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		applyCBSecondary(ctx, deps, st)
		return st, nil
	}), compose.WithNodeName(nodeAISecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeSkipSecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		applyCBSkipSecondary(st)
		return st, nil
	}), compose.WithNodeName(nodeSkipSecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeAIReport, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		if err := applyCBReport(ctx, deps, st); err != nil {
			return nil, err
		}
		return st, nil
	}), compose.WithNodeName(nodeAIReport)); err != nil {
		return nil, err
	}

	primaryBranch := compose.NewGraphBranch(func(ctx context.Context, st *tools.PipelineState) (string, error) {
		if cbNeedsSecondary(st, thr) {
			return nodeAISecondary, nil
		}
		return nodeSkipSecondary, nil
	}, map[string]bool{nodeAISecondary: true, nodeSkipSecondary: true})

	for _, step := range []struct {
		fn func() error
	}{
		{func() error { return g.AddEdge(compose.START, nodeAIPrimary) }},
		{func() error { return g.AddBranch(nodeAIPrimary, primaryBranch) }},
		{func() error { return g.AddEdge(nodeAISecondary, nodeAIReport) }},
		{func() error { return g.AddEdge(nodeSkipSecondary, nodeAIReport) }},
		{func() error { return g.AddEdge(nodeAIReport, compose.END) }},
	} {
		if err := step.fn(); err != nil {
			return nil, err
		}
	}

	return g.Compile(ctx, compose.WithGraphName(cbGraphName))
}

func degradedSecondary(st *tools.PipelineState, cause error) *tools.SecondaryAssessment {
	base := 0.0
	if st.Primary != nil {
		base = st.Primary.RiskScore
	}
	_ = cause
	return &tools.SecondaryAssessment{
		Skipped:           true,
		Confirmed:         false,
		FinalRiskScore:    base,
		Rationale:         "因技术原因，未经 AI 二次验证；已降级为仅初筛结果，请人工复核。",
		TechnicalDegraded: true,
		RawModelOutput:    "",
	}
}

func recordStep(st *tools.PipelineState, name string, t0 time.Time) {
	if st.StepTimings == nil {
		st.StepTimings = map[string]time.Duration{}
	}
	st.StepTimings[name] = time.Since(t0)
}
