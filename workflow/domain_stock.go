package workflow

import (
	"context"
	"fmt"
	"time"

	"risk_control/tools"
)

type stockProfile struct{}

func (stockProfile) ID() string { return tools.BusinessStock }

func (stockProfile) PreAnalyze(pack *PolicyPack, req any) (PreAnalysis, error) {
	order, err := asStock(req)
	if err != nil {
		return PreAnalysis{}, err
	}
	norm, err := tools.NormalizeStockOrder(order)
	if err != nil {
		return PreAnalysis{}, err
	}
	return PreAnalyzeStock(order, norm, pack.Stock, pack.Orchestrator), nil
}

func (stockProfile) RunRules(ctx context.Context, e *RiskEngine, pack *PolicyPack, req any, pre PreAnalysis) EngineResult {
	_ = ctx
	_ = pre
	t0 := time.Now()
	order, _ := asStock(req)
	norm, err := tools.NormalizeStockOrder(order)
	if err != nil {
		return EngineResult{Engine: tools.EngineRule, Decision: tools.DecisionReview, Score: 0.5, Degraded: true, Rationale: err.Error(), LatencyMs: time.Since(t0).Milliseconds()}
	}
	st := &tools.StockPipelineState{
		TraceID: tools.GetUUID(),
		Order:   order,
		Norm:    norm,
		Gate:    &tools.StockLocalGate{},
		Audit:   &tools.AuditBuffer{},
	}
	ApplyStockLocalRules(st, pack.Stock, e.velocity)
	dec, early, report := StockRuleDecision(st, pack.Stock)
	var pols []string
	if st.Gate != nil && st.Gate.HardBlock {
		pols = append(pols, st.Gate.BlockReason)
	}
	primary := &tools.PrimaryAssessment{RiskScore: st.Gate.LocalRiskScore, Rationale: "stock_local_rules", NeedsSecondaryReview: !early && dec == tools.DecisionReview}
	return EngineResult{
		Engine:         tools.EngineRule,
		Decision:       dec,
		Score:          st.Gate.LocalRiskScore,
		PolicyIDs:      pols,
		Rationale:      "stock_local_gate",
		LatencyMs:      time.Since(t0).Milliseconds(),
		EarlyExit:      early,
		TraceID:        st.TraceID,
		Primary:        primary,
		ReportMarkdown: report,
		Audit:          st.Audit,
		Blocked:        st.Gate.HardBlock,
		BlockReason:    st.Gate.BlockReason,
		SkipAI:         early && dec == tools.DecisionApprove,
		Stock:          st,
	}
}

func (stockProfile) RunGraph(e *RiskEngine, pack *PolicyPack, req any) EngineResult {
	t0 := time.Now()
	order, _ := asStock(req)
	norm, _ := tools.NormalizeStockOrder(order)
	sym := ""
	if norm != nil {
		sym = norm.SymbolKey
	}
	nodes := []string{
		prefixNode("sym", sym),
		prefixNode("acct", order.AccountID),
	}
	score, dec, detail, pols := e.graph.ObserveLinks(nodes, order.AccountID, sym, pack.Orchestrator)
	// 股票用 account 当「设备」维度：同账户多标的聚集
	return EngineResult{
		Engine:    tools.EngineGraph,
		Decision:  dec,
		Score:     score,
		PolicyIDs: pols,
		Rationale: detail,
		LatencyMs: time.Since(t0).Milliseconds(),
	}
}

func (stockProfile) RunLight(pack *PolicyPack, pre PreAnalysis, graphScore float64) EngineResult {
	return RunLightEngine(pre.Features, pack, graphScore)
}

func (stockProfile) NeedDeep(pre PreAnalysis, rule EngineResult, arbDecision string) bool {
	if rule.EarlyExit || arbDecision == tools.DecisionReject {
		return false
	}
	if pre.Bucket == tools.BucketFast && arbDecision == tools.DecisionApprove {
		return false
	}
	return pre.Bucket == tools.BucketDeep || arbDecision == tools.DecisionReview || rule.Decision == tools.DecisionReview
}

func (stockProfile) ShadowLocal(ctx context.Context, e *RiskEngine, shadow *PolicyPack, req any) (string, string) {
	order, _ := asStock(req)
	pre, _ := (stockProfile{}).PreAnalyze(shadow, order)
	rule := (stockProfile{}).RunRules(ctx, e, shadow, order, pre)
	light := (stockProfile{}).RunLight(shadow, pre, 0)
	dec, _, _, _, detail := Arbitrate([]EngineResult{rule, light})
	return dec, detail
}

func (stockProfile) SubjectLabel(req any) string {
	order, _ := asStock(req)
	return order.Symbol
}

func (stockProfile) TransactionID(req any) string {
	order, _ := asStock(req)
	return order.OrderID
}

func asStock(req any) (tools.StockOrder, error) {
	switch v := req.(type) {
	case tools.StockOrder:
		return v, nil
	case *tools.StockOrder:
		return *v, nil
	default:
		return tools.StockOrder{}, fmt.Errorf("invalid stock payload %T", req)
	}
}
