package workflow

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"risk_control/config"
	"risk_control/tools"
)

const (
	stockGraphName = "stock_deep_v1"

	stAIPrimary     = "st_ai_primary"
	stAISecondary   = "st_ai_secondary"
	stSkipSecondary = "st_skip_secondary"
	stAIReport      = "st_ai_report"
)

// BuildStockDeepGraph 仅深度 AI。本地闸门由 ApplyStockLocalRules 在内核完成。
func BuildStockDeepGraph(ctx context.Context, deps *GraphDeps) (compose.Runnable[*tools.StockPipelineState, *tools.StockPipelineState], error) {
	if deps == nil || deps.Router == nil {
		return nil, fmt.Errorf("workflow deps incomplete")
	}
	thr := primaryStockRiskThreshold(deps.Cfg)
	g := compose.NewGraph[*tools.StockPipelineState, *tools.StockPipelineState]()

	if err := g.AddLambdaNode(stAIPrimary, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		if err := applyStockPrimary(ctx, deps, st); err != nil {
			return nil, err
		}
		return st, nil
	}), compose.WithNodeName(stAIPrimary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stAISecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		applyStockSecondary(ctx, deps, st)
		return st, nil
	}), compose.WithNodeName(stAISecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stSkipSecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		applyStockSkipSecondary(st)
		return st, nil
	}), compose.WithNodeName(stSkipSecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stAIReport, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		if err := applyStockReport(ctx, deps, st); err != nil {
			return nil, err
		}
		return st, nil
	}), compose.WithNodeName(stAIReport)); err != nil {
		return nil, err
	}

	branchSecondary := compose.NewGraphBranch(func(ctx context.Context, st *tools.StockPipelineState) (string, error) {
		if stockNeedsSecondary(st, thr) {
			return stAISecondary, nil
		}
		return stSkipSecondary, nil
	}, map[string]bool{stAISecondary: true, stSkipSecondary: true})

	for _, step := range []struct {
		fn func() error
	}{
		{func() error { return g.AddEdge(compose.START, stAIPrimary) }},
		{func() error { return g.AddBranch(stAIPrimary, branchSecondary) }},
		{func() error { return g.AddEdge(stAISecondary, stAIReport) }},
		{func() error { return g.AddEdge(stSkipSecondary, stAIReport) }},
		{func() error { return g.AddEdge(stAIReport, compose.END) }},
	} {
		if err := step.fn(); err != nil {
			return nil, err
		}
	}

	return g.Compile(ctx, compose.WithGraphName(stockGraphName))
}

func primaryStockRiskThreshold(cfg config.Config) float64 {
	if cfg.PrimaryStockRiskScore > 0 {
		return cfg.PrimaryStockRiskScore
	}
	return 0.45
}

func stockNeedsSecondary(st *tools.StockPipelineState, thr float64) bool {
	if st.Primary == nil {
		return false
	}
	p := st.Primary
	if st.Gate != nil && st.Gate.ForceAIReview && p.RiskScore >= 0.45 {
		return true
	}
	return p.NeedsSecondaryReview && p.RiskScore >= thr
}

func degradedStockSecondary(st *tools.StockPipelineState, cause error) *tools.SecondaryAssessment {
	base := 0.0
	if st.Primary != nil {
		base = st.Primary.RiskScore
	}
	_ = cause
	return &tools.SecondaryAssessment{
		Skipped:           true,
		Confirmed:         false,
		FinalRiskScore:    base,
		Rationale:         "因技术原因，未经 AI 二次验证；以初筛为准。",
		TechnicalDegraded: true,
	}
}

func finalizeStockScreeningResult(st *tools.StockPipelineState) tools.ScreeningResult {
	res := tools.ScreeningResult{
		BusinessType:   tools.BusinessStock,
		TraceID:        st.TraceID,
		TransactionID:  st.Order.OrderID,
		Primary:        st.Primary,
		Secondary:      st.Secondary,
		ReportMarkdown: st.ReportMarkdown,
	}
	if st.Gate != nil && st.Gate.HardBlock {
		res.Blocked = true
		res.BlockReason = st.Gate.BlockReason
		res.Level = "BLOCKED"
		res.Decision = tools.DecisionReject
		res.FinalRiskScore = 1.0
		if st.Gate.LocalRiskScore > 0 {
			res.FinalRiskScore = st.Gate.LocalRiskScore
		}
		return res
	}
	score := 0.0
	if st.Primary != nil {
		score = st.Primary.RiskScore
	}
	if st.Secondary != nil {
		if st.Secondary.TechnicalDegraded {
			if st.Primary != nil {
				score = st.Primary.RiskScore
			}
		} else if !st.Secondary.Skipped {
			score = st.Secondary.FinalRiskScore
		}
	}
	res.FinalRiskScore = score
	res.Decision = tools.DecisionFromScore(score)
	res.Level = tools.LevelFromScore(score)
	return res
}
