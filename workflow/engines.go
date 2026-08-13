package workflow

import (
	"context"
	"fmt"
	"time"

	"risk_control/store"
	"risk_control/tools"
)

// EngineResult 单引擎输出（业务无关）。
type EngineResult struct {
	Engine         string
	Decision       string
	Score          float64
	PolicyIDs      []string
	Rationale      string
	LatencyMs      int64
	Degraded       bool
	EarlyExit      bool
	TraceID        string
	ListVersion    string
	Primary        *tools.PrimaryAssessment
	ReportMarkdown string
	Audit          *tools.AuditBuffer
	Blocked        bool
	BlockReason    string
	// 跨境深度路由辅助
	SkipAI bool
}

func (r EngineResult) Trace() tools.EngineTrace {
	return tools.EngineTrace{
		Engine:    r.Engine,
		Decision:  r.Decision,
		Score:     r.Score,
		PolicyIDs: r.PolicyIDs,
		LatencyMs: r.LatencyMs,
		Degraded:  r.Degraded,
		Rationale: r.Rationale,
	}
}

func engineFromPipeline(ps *tools.PipelineState, early bool, rationale string, t0 time.Time) EngineResult {
	return EngineResult{
		Engine:         tools.EngineRule,
		Decision:       ps.Decision,
		Score:          scoreFromState(ps),
		PolicyIDs:      append([]string{}, ps.PolicyIDs...),
		Rationale:      rationale,
		LatencyMs:      time.Since(t0).Milliseconds(),
		EarlyExit:      early,
		TraceID:        ps.TraceID,
		ListVersion:    ps.ListVersion,
		Primary:        ps.Primary,
		ReportMarkdown: ps.ReportMarkdown,
		Audit:          ps.Audit,
		SkipAI:         ps.Gate != nil && ps.Gate.SkipAI,
	}
}

// RunRuleEngine 跨境确定性规则。
func RunRuleEngine(ctx context.Context, txn tools.CrossBorderTransaction, pack *PolicyPack, vel *VelocityTracker, st store.Store, withSanctions bool) EngineResult {
	t0 := time.Now()
	rules := pack.CrossBorder
	party := tools.NormalizePartyName(txn.Counterparty, txn.Country)
	ps := &tools.PipelineState{
		TraceID:     tools.GetUUID(),
		Transaction: txn,
		Party:       party,
		Gate:        &tools.CBLocalGate{},
		Audit:       &tools.AuditBuffer{},
		StepTimings: map[string]time.Duration{},
	}
	if ver, err := st.ActiveListVersion(ctx); err == nil {
		ps.ListVersion = ver
	}
	ApplyFastFilter(ps, rules, vel)
	if ps.Gate.EarlyExit {
		ApplyRuleDecision(ps)
		return engineFromPipeline(ps, true, "rule_early_exit", t0)
	}
	if withSanctions {
		hits, err := st.SearchSanctions(ctx, party, rules.CandidateLimit*3)
		if err != nil {
			return EngineResult{
				Engine:    tools.EngineRule,
				Decision:  tools.DecisionReview,
				Score:     0.5,
				PolicyIDs: []string{tools.PolicyPreAnalyze},
				Rationale: "sanctions_search_error: " + err.Error(),
				LatencyMs: time.Since(t0).Milliseconds(),
				Degraded:  true,
				TraceID:   ps.TraceID,
				Audit:     ps.Audit,
			}
		}
		ps.Candidates = tools.RankCandidates(party, hits, rules.FuzzyMatchMinScore, rules.CandidateLimit)
		ApplyMatchRouting(ps, rules)
		if ps.Gate.SkipAI || ps.Gate.AutoReject {
			ApplyRuleDecision(ps)
			early := ps.Decision == tools.DecisionApprove || ps.Decision == tools.DecisionReject
			return engineFromPipeline(ps, early, "rule_match_route", t0)
		}
	}
	return EngineResult{
		Engine:    tools.EngineRule,
		Decision:  tools.DecisionReview,
		Score:     maxFloat(ps.Gate.LocalRiskScore, 0.35),
		PolicyIDs: append([]string{}, ps.PolicyIDs...),
		Rationale: "rule_needs_deeper",
		LatencyMs: time.Since(t0).Milliseconds(),
		TraceID:   ps.TraceID,
		ListVersion: ps.ListVersion,
		Audit:     ps.Audit,
		SkipAI:    false,
	}
}

func scoreFromState(ps *tools.PipelineState) float64 {
	if ps.Primary != nil {
		return ps.Primary.RiskScore
	}
	if ps.Gate != nil {
		return ps.Gate.LocalRiskScore
	}
	return 0
}

// RunLightEngine 轻量线性模型。
func RunLightEngine(fv tools.FeatureVector, pack *PolicyPack, graphScore float64) EngineResult {
	t0 := time.Now()
	orch := pack.Orchestrator
	score := ScoreLightML(fv, pack.LightWeights, graphScore)
	dec := tools.DecisionApprove
	rationale := "light_ml_low"
	var pols []string
	if score >= orch.LightRejectThreshold {
		dec = tools.DecisionReject
		rationale = fmt.Sprintf("light_ml_reject score=%.3f", score)
		pols = []string{tools.PolicyLightML}
	} else if score >= orch.LightReviewThreshold {
		dec = tools.DecisionReview
		rationale = fmt.Sprintf("light_ml_review score=%.3f", score)
		pols = []string{tools.PolicyLightML}
	}
	return EngineResult{
		Engine:    tools.EngineLight,
		Decision:  dec,
		Score:     score,
		PolicyIDs: pols,
		Rationale: rationale,
		LatencyMs: time.Since(t0).Milliseconds(),
	}
}

// RunGraphEngineCB 跨境关联图。
func RunGraphEngineCB(txn tools.CrossBorderTransaction, party *tools.NormalizedParty, g *EntityGraph, pack *PolicyPack) EngineResult {
	t0 := time.Now()
	score, dec, detail, pols := g.ObserveCB(txn, party, pack.Orchestrator)
	return EngineResult{
		Engine:    tools.EngineGraph,
		Decision:  dec,
		Score:     score,
		PolicyIDs: pols,
		Rationale: detail,
		LatencyMs: time.Since(t0).Milliseconds(),
	}
}

// Arbitrate 仲裁：一票 REJECT。
func Arbitrate(results []EngineResult) (decision string, score float64, policies []string, conflict bool, rationale string) {
	if len(results) == 0 {
		return tools.DecisionReview, 0.5, nil, false, "no_engine_results"
	}
	decision = tools.DecisionApprove
	score = 0
	var reasons []string
	approveN, reviewN, rejectN := 0, 0, 0
	for _, r := range results {
		policies = appendUnique(policies, r.PolicyIDs...)
		if r.Score > score {
			score = r.Score
		}
		switch r.Decision {
		case tools.DecisionReject:
			rejectN++
			decision = tools.DecisionReject
			reasons = append(reasons, r.Engine+":REJECT")
		case tools.DecisionReview:
			reviewN++
			if decision != tools.DecisionReject {
				decision = tools.DecisionReview
			}
			reasons = append(reasons, r.Engine+":REVIEW")
		default:
			approveN++
			reasons = append(reasons, r.Engine+":APPROVE")
		}
	}
	conflict = (rejectN > 0 && (reviewN > 0 || approveN > 0)) || (reviewN > 0 && approveN > 0 && rejectN == 0)
	if decision == tools.DecisionReject && score < 0.65 {
		score = 0.85
	}
	rationale = fmt.Sprintf("arbiter reject=%d review=%d approve=%d | %v", rejectN, reviewN, approveN, reasons)
	return decision, score, policies, conflict, rationale
}

func appendUnique(dst []string, ids ...string) []string {
	for _, id := range ids {
		found := false
		for _, x := range dst {
			if x == id {
				found = true
				break
			}
		}
		if !found && id != "" {
			dst = append(dst, id)
		}
	}
	return dst
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
