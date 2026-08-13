package workflow

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cloudwego/eino/compose"

	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
)

// OrchestrateCrossBorder 阶段3/4 主路径：预分析 → 本地引擎 →（可选）深度图 → 仲裁/降级/影子。
func (e *RiskEngine) OrchestrateCrossBorder(ctx context.Context, txn tools.CrossBorderTransaction, opts ...compose.Option) (tools.ScreeningResult, error) {
	pack := e.policies.Primary()
	if pack == nil {
		return tools.ScreeningResult{}, fmt.Errorf("policy pack missing")
	}
	orch := pack.Orchestrator
	party := tools.NormalizePartyName(txn.Counterparty, txn.Country)
	pre := PreAnalyze(txn, party, pack.CrossBorder, orch)
	switch pre.Bucket {
	case tools.BucketFast:
		e.metrics.PreFast.Add(1)
	case tools.BucketLight:
		e.metrics.PreLight.Add(1)
	default:
		e.metrics.PreDeep.Add(1)
	}

	withSanctions := pre.Bucket != tools.BucketFast
	ruleRes := RunRuleEngine(ctx, txn, pack, e.velocity, e.store, withSanctions)

	var graphRes EngineResult
	if ruleRes.State != nil && ruleRes.State.Party != nil {
		graphRes = RunGraphEngine(txn, ruleRes.State.Party, e.graph, orch)
	} else {
		graphRes = RunGraphEngine(txn, party, e.graph, orch)
	}
	lightRes := RunLightEngine(pre.Features, pack, graphRes.Score)

	local := []EngineResult{ruleRes, graphRes, lightRes}
	dec, score, pols, conflict, arbRationale := Arbitrate(local)
	traces := []tools.EngineTrace{ruleRes.Trace(), graphRes.Trace(), lightRes.Trace()}

	if ruleRes.EarlyExit || dec == tools.DecisionReject {
		e.metrics.LocalOnly.Add(1)
		res := e.finalizeLocal(ctx, txn, ruleRes, dec, score, pols, pre, traces, pack, arbRationale, conflict, false)
		e.runShadowAsync(txn, res, pack)
		return res, nil
	}

	needDeep := pre.Bucket == tools.BucketDeep || dec == tools.DecisionReview || (ruleRes.Gate != nil && !ruleRes.Gate.SkipAI && !ruleRes.EarlyExit)
	if !needDeep && pre.Bucket == tools.BucketFast && dec == tools.DecisionApprove {
		e.metrics.LocalOnly.Add(1)
		res := e.finalizeLocal(ctx, txn, ruleRes, dec, score, pols, pre, traces, pack, arbRationale, conflict, false)
		e.runShadowAsync(txn, res, pack)
		return res, nil
	}

	if !e.breaker.Allow() {
		e.metrics.DeepSkip.Add(1)
		pols = appendUnique(pols, tools.PolicyCircuitOpen)
		res := e.finalizeLocal(ctx, txn, ruleRes, tools.DecisionReview, maxFloat(score, 0.45), pols, pre, traces, pack, "circuit_open_fallback", conflict, true)
		e.runShadowAsync(txn, res, pack)
		return res, nil
	}

	deepCtx, cancel := context.WithTimeout(ctx, time.Duration(orch.DeepTimeoutMs)*time.Millisecond)
	defer cancel()
	deepRes, err := e.cbGraph.Invoke(deepCtx, txn, opts...)
	if err != nil {
		e.breaker.Fail()
		e.metrics.DeepFail.Add(1)
		pols = appendUnique(pols, tools.PolicyDeepTimeout)
		traces = append(traces, tools.EngineTrace{
			Engine:    tools.EngineDeep,
			Decision:  tools.DecisionReview,
			Score:     score,
			Degraded:  true,
			Rationale: err.Error(),
		})
		res := e.finalizeLocal(ctx, txn, ruleRes, tools.DecisionReview, maxFloat(score, 0.5), pols, pre, traces, pack, "deep_degraded:"+err.Error(), conflict, true)
		e.runShadowAsync(txn, res, pack)
		return res, nil
	}
	e.breaker.Success()
	e.metrics.DeepOK.Add(1)

	deepRes.RouteBucket = pre.Bucket
	deepRes.PackVersion = pack.Version
	deepRes.Engines = append(traces, tools.EngineTrace{
		Engine:    tools.EngineDeep,
		Decision:  deepRes.Decision,
		Score:     deepRes.FinalRiskScore,
		PolicyIDs: deepRes.PolicyIDs,
		Rationale: "cb_graph",
	})
	deepRes.PolicyIDs = appendUnique(deepRes.PolicyIDs, pols...)
	if conflict {
		deepRes.PolicyIDs = appendUnique(deepRes.PolicyIDs, tools.PolicyPreAnalyze)
	}

	if orch.AsyncCaseDraft && deepRes.Decision == tools.DecisionReview && deepRes.CaseID != "" {
		e.enqueueCaseDraft(deepRes.CaseID, txn, deepRes)
	}
	e.runShadowAsync(txn, deepRes, pack)
	return deepRes, nil
}

func (e *RiskEngine) finalizeLocal(
	ctx context.Context,
	txn tools.CrossBorderTransaction,
	ruleRes EngineResult,
	dec string,
	score float64,
	pols []string,
	pre PreAnalysis,
	traces []tools.EngineTrace,
	pack *PolicyPack,
	arbRationale string,
	conflict bool,
	degraded bool,
) tools.ScreeningResult {
	traceID := tools.GetUUID()
	listVer := ""
	var primary *tools.PrimaryAssessment
	report := fmt.Sprintf("## 多引擎本地裁决\n- **决策**: %s\n- **路由**: %s\n- **仲裁**: %s\n", dec, pre.Bucket, arbRationale)
	persisted := 0
	if ruleRes.State != nil {
		if ruleRes.State.TraceID != "" {
			traceID = ruleRes.State.TraceID
		}
		listVer = ruleRes.State.ListVersion
		if ruleRes.State.Primary != nil {
			primary = ruleRes.State.Primary
		}
		if ruleRes.State.ReportMarkdown != "" {
			report = ruleRes.State.ReportMarkdown + "\n" + report
		}
		if ruleRes.State.Audit != nil {
			ruleRes.State.Audit.AddStep("orchestrator_arbiter", store.LogJSON(map[string]any{
				"decision": dec,
				"score":    score,
				"conflict": conflict,
				"bucket":   pre.Bucket,
				"engines":  traces,
			}), 0)
			_ = e.store.FlushAudit(ctx, traceID, ruleRes.State.Audit)
			persisted = len(ruleRes.State.Audit.Steps) + len(ruleRes.State.Audit.Decisions)
		}
	}
	if primary == nil {
		primary = &tools.PrimaryAssessment{RiskScore: score, Rationale: arbRationale}
	}
	res := tools.ScreeningResult{
		BusinessType:       tools.BusinessCrossBorder,
		TraceID:            traceID,
		TransactionID:      txn.TransactionID,
		Decision:           dec,
		PolicyIDs:          pols,
		ListVersion:        listVer,
		SkippedAI:          true,
		FinalRiskScore:     score,
		Level:              tools.LevelFromScore(score),
		Primary:            primary,
		Secondary:          &tools.SecondaryAssessment{Skipped: true, FinalRiskScore: score, Rationale: "orchestrator_local"},
		ReportMarkdown:     report,
		RouteBucket:        pre.Bucket,
		Engines:            traces,
		Degraded:           degraded,
		PackVersion:        pack.Version,
		PersistedAuditRows: persisted,
	}
	if dec == tools.DecisionReject {
		res.Level = "HIGH"
	}
	if dec == tools.DecisionReview {
		caseID := "CASE-" + tools.GetUUID()
		_ = e.store.CreateReviewCase(ctx, &tools.ReviewCase{
			CaseID:        caseID,
			TraceID:       traceID,
			TransactionID: txn.TransactionID,
			Status:        "OPEN",
			DecisionCode:  tools.DecisionReview,
			PolicyIDs:     pols,
			ListVersion:   listVer,
			PayloadJSON:   store.LogJSON(map[string]any{"txn": txn, "pre": pre, "engines": traces}),
		})
		res.CaseID = caseID
		if pack.Orchestrator.AsyncCaseDraft {
			e.enqueueCaseDraft(caseID, txn, res)
		}
	}
	return res
}

func (e *RiskEngine) runShadowAsync(txn tools.CrossBorderTransaction, primary tools.ScreeningResult, mainPack *PolicyPack) {
	orch := mainPack.Orchestrator
	shadow := e.policies.Shadow()
	if !orch.ShadowEnabled || shadow == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		party := tools.NormalizePartyName(txn.Counterparty, txn.Country)
		pre := PreAnalyze(txn, party, shadow.CrossBorder, shadow.Orchestrator)
		ruleRes := RunRuleEngine(ctx, txn, shadow, e.velocity, e.store, true)
		lightRes := RunLightEngine(pre.Features, shadow, 0)
		dec, _, _, _, detail := Arbitrate([]EngineResult{ruleRes, lightRes})
		if dec != primary.Decision {
			e.metrics.ShadowDiff.Add(1)
			log.Printf("shadow_diff trace=%s primary=%s shadow=%s pack=%s detail=%s",
				primary.TraceID, primary.Decision, dec, shadow.Version, detail)
		}
	}()
}

func (e *RiskEngine) enqueueCaseDraft(caseID string, txn tools.CrossBorderTransaction, res tools.ScreeningResult) {
	if e == nil || e.store == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		draft := fmt.Sprintf("## 案例草稿（自动）\n- Case: %s\n- 对手方: %s\n- 决策: %s\n- 分数: %.3f\n- 策略: %v\n\n请人工核实后提交 SAR/结案。\n",
			caseID, txn.Counterparty, res.Decision, res.FinalRiskScore, res.PolicyIDs)
		if e.router != nil {
			st := &tools.PipelineState{
				Transaction: txn,
				Primary:     res.Primary,
				Secondary:   res.Secondary,
			}
			out, err := llm.GenerateWithRetry(ctx, e.router.For(tools.TaskReport), llm.ReportMessages(st, e.cfg), llm.DefaultRetryConfig())
			if err == nil && out != nil && out.Content != "" {
				draft = out.Content
			}
		}
		_ = e.store.UpdateReviewCaseDraft(ctx, caseID, draft)
	}()
}
