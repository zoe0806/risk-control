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

// Orchestrate 通用多引擎编排：预分析 → 规则/图/轻量 → 仲裁 → 可选深度 → 影子。
func (e *RiskEngine) Orchestrate(ctx context.Context, profile DomainProfile, req any, opts ...compose.Option) (tools.ScreeningResult, error) {
	if profile == nil {
		return tools.ScreeningResult{}, fmt.Errorf("nil domain profile")
	}
	domain := profile.ID()
	pack := e.policyReg.Primary(domain)
	if pack == nil {
		return tools.ScreeningResult{}, fmt.Errorf("policy pack missing for %s", domain)
	}
	orch := pack.Orchestrator

	pre, err := profile.PreAnalyze(pack, req)
	if err != nil {
		return tools.ScreeningResult{}, err
	}
	switch pre.Bucket {
	case tools.BucketFast:
		e.metrics.PreFast.Add(1)
	case tools.BucketLight:
		e.metrics.PreLight.Add(1)
	default:
		e.metrics.PreDeep.Add(1)
	}

	ruleRes := profile.RunRules(ctx, e, pack, req, pre)
	graphRes := profile.RunGraph(e, pack, req)
	lightRes := profile.RunLight(pack, pre, graphRes.Score)

	local := []EngineResult{ruleRes, graphRes, lightRes}
	dec, score, pols, conflict, arbRationale := Arbitrate(local)
	traces := []tools.EngineTrace{ruleRes.Trace(), graphRes.Trace(), lightRes.Trace()}

	if ruleRes.EarlyExit || dec == tools.DecisionReject || !profile.NeedDeep(pre, ruleRes, dec) {
		e.metrics.LocalOnly.Add(1)
		res := e.finalizeLocalGeneric(ctx, profile, req, ruleRes, dec, score, pols, pre, traces, pack, arbRationale, conflict, false)
		e.runShadowGeneric(profile, req, res, pack)
		return res, nil
	}

	if !e.breaker.Allow() {
		e.metrics.DeepSkip.Add(1)
		pols = appendUnique(pols, tools.PolicyCircuitOpen)
		res := e.finalizeLocalGeneric(ctx, profile, req, ruleRes, tools.DecisionReview, maxFloat(score, 0.45), pols, pre, traces, pack, "circuit_open_fallback", conflict, true)
		e.runShadowGeneric(profile, req, res, pack)
		return res, nil
	}

	deepCtx, cancel := context.WithTimeout(ctx, time.Duration(orch.DeepTimeoutMs)*time.Millisecond)
	defer cancel()
	deepRes, err := profile.InvokeDeep(deepCtx, e, req, opts)
	if err != nil {
		e.breaker.Fail()
		e.metrics.DeepFail.Add(1)
		pols = appendUnique(pols, tools.PolicyDeepTimeout)
		traces = append(traces, tools.EngineTrace{
			Engine: tools.EngineDeep, Decision: tools.DecisionReview, Score: score, Degraded: true, Rationale: err.Error(),
		})
		res := e.finalizeLocalGeneric(ctx, profile, req, ruleRes, tools.DecisionReview, maxFloat(score, 0.5), pols, pre, traces, pack, "deep_degraded:"+err.Error(), conflict, true)
		e.runShadowGeneric(profile, req, res, pack)
		return res, nil
	}
	e.breaker.Success()
	e.metrics.DeepOK.Add(1)

	deepRes.RouteBucket = pre.Bucket
	deepRes.PackVersion = pack.Version
	deepRes.Engines = append(traces, tools.EngineTrace{
		Engine: tools.EngineDeep, Decision: deepRes.Decision, Score: deepRes.FinalRiskScore,
		PolicyIDs: deepRes.PolicyIDs, Rationale: domain + "_deep_graph",
	})
	deepRes.PolicyIDs = appendUnique(deepRes.PolicyIDs, pols...)
	if deepRes.Decision == "" {
		if deepRes.Blocked {
			deepRes.Decision = tools.DecisionReject
		} else {
			deepRes.Decision = tools.DecisionFromScore(deepRes.FinalRiskScore)
		}
	}
	if orch.AsyncCaseDraft && deepRes.Decision == tools.DecisionReview && deepRes.CaseID != "" {
		e.enqueueCaseDraftGeneric(profile, req, deepRes.CaseID, deepRes)
	}
	e.runShadowGeneric(profile, req, deepRes, pack)
	return deepRes, nil
}

// OrchestrateCrossBorder 兼容旧入口。
func (e *RiskEngine) OrchestrateCrossBorder(ctx context.Context, txn tools.CrossBorderTransaction, opts ...compose.Option) (tools.ScreeningResult, error) {
	return e.Orchestrate(ctx, crossBorderProfile{}, txn, opts...)
}

func (e *RiskEngine) finalizeLocalGeneric(
	ctx context.Context,
	profile DomainProfile,
	req any,
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
	traceID := ruleRes.TraceID
	if traceID == "" {
		traceID = tools.GetUUID()
	}
	report := ruleRes.ReportMarkdown
	if report == "" {
		report = fmt.Sprintf("## 多引擎本地裁决\n- **域**: %s\n- **决策**: %s\n- **路由**: %s\n- **仲裁**: %s\n",
			profile.ID(), dec, pre.Bucket, arbRationale)
	} else {
		report = report + fmt.Sprintf("\n## 编排仲裁\n- %s\n", arbRationale)
	}
	persisted := 0
	if ruleRes.Audit != nil {
		ruleRes.Audit.AddStep("orchestrator_arbiter", store.LogJSON(map[string]any{
			"domain": profile.ID(), "decision": dec, "score": score, "conflict": conflict, "bucket": pre.Bucket, "engines": traces,
		}), 0)
		_ = e.store.FlushAudit(ctx, traceID, ruleRes.Audit)
		persisted = len(ruleRes.Audit.Steps) + len(ruleRes.Audit.Decisions)
	}
	primary := ruleRes.Primary
	if primary == nil {
		primary = &tools.PrimaryAssessment{RiskScore: score, Rationale: arbRationale}
	}
	res := tools.ScreeningResult{
		BusinessType:       profile.ID(),
		TraceID:            traceID,
		TransactionID:      profile.TransactionID(req),
		Decision:           dec,
		PolicyIDs:          pols,
		ListVersion:        ruleRes.ListVersion,
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
		Blocked:            ruleRes.Blocked || dec == tools.DecisionReject && profile.ID() == tools.BusinessStock,
		BlockReason:        ruleRes.BlockReason,
	}
	if dec == tools.DecisionReject {
		res.Level = "HIGH"
		if profile.ID() == tools.BusinessStock && ruleRes.Blocked {
			res.Level = "BLOCKED"
		}
	}
	if dec == tools.DecisionReview {
		caseID := "CASE-" + tools.GetUUID()
		_ = e.store.CreateReviewCase(ctx, &tools.ReviewCase{
			CaseID: caseID, TraceID: traceID, TransactionID: profile.TransactionID(req),
			Status: "OPEN", DecisionCode: tools.DecisionReview, PolicyIDs: pols, ListVersion: ruleRes.ListVersion,
			PayloadJSON: store.LogJSON(map[string]any{"domain": profile.ID(), "req": req, "pre": pre, "engines": traces}),
		})
		res.CaseID = caseID
		if pack.Orchestrator.AsyncCaseDraft {
			e.enqueueCaseDraftGeneric(profile, req, caseID, res)
		}
	}
	return res
}

func (e *RiskEngine) runShadowGeneric(profile DomainProfile, req any, primary tools.ScreeningResult, mainPack *PolicyPack) {
	if !mainPack.Orchestrator.ShadowEnabled {
		return
	}
	shadow := e.policyReg.Shadow(profile.ID())
	if shadow == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		dec, detail := profile.ShadowLocal(ctx, e, shadow, req)
		if dec != primary.Decision {
			e.metrics.ShadowDiff.Add(1)
			log.Printf("shadow_diff domain=%s trace=%s primary=%s shadow=%s pack=%s detail=%s",
				profile.ID(), primary.TraceID, primary.Decision, dec, shadow.Version, detail)
		}
	}()
}

func (e *RiskEngine) enqueueCaseDraftGeneric(profile DomainProfile, req any, caseID string, res tools.ScreeningResult) {
	if e == nil || e.store == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		draft := fmt.Sprintf("## 案例草稿（自动）\n- Domain: %s\n- Case: %s\n- Subject: %s\n- 决策: %s\n- 分数: %.3f\n- 策略: %v\n",
			profile.ID(), caseID, profile.SubjectLabel(req), res.Decision, res.FinalRiskScore, res.PolicyIDs)
		if e.router != nil {
			if txn, ok := req.(tools.CrossBorderTransaction); ok {
				st := &tools.PipelineState{Transaction: txn, Primary: res.Primary, Secondary: res.Secondary}
				out, err := llm.GenerateWithRetry(ctx, e.router.For(tools.TaskReport), llm.ReportMessages(st, e.cfg), llm.DefaultRetryConfig())
				if err == nil && out != nil && out.Content != "" {
					draft = out.Content
				}
			}
		}
		_ = e.store.UpdateReviewCaseDraft(ctx, caseID, draft)
	}()
}
