package workflow

import (
	"context"
	"fmt"
	"log"
	"time"

	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
)

// Orchestrate 通用多引擎编排：预分析 → 规则/图/轻量 → 仲裁 → 可选深度 runtime → 影子。
func (e *RiskEngine) Orchestrate(ctx context.Context, profile DomainProfile, req any, skipDeep bool) (tools.ScreeningResult, error) {
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

	useDeep := !skipDeep && e.deep != nil && e.deep.Name() != DeepRuntimeOff &&
		!ruleRes.EarlyExit && dec != tools.DecisionReject && profile.NeedDeep(pre, ruleRes, dec)

	if !useDeep {
		e.metrics.LocalOnly.Add(1)
		res := e.finalizeLocalGeneric(ctx, profile, req, ruleRes, dec, score, pols, pre, traces, pack, arbRationale, conflict, false)
		e.runShadowGeneric(profile, req, res, pack)
		return res, nil
	}

	if cached := e.lookupCBCache(ctx, ruleRes); cached != nil {
		cached.RouteBucket = pre.Bucket
		cached.PackVersion = pack.Version
		cached.Engines = traces
		cached.DeepRuntime = e.DeepRuntimeName()
		e.runShadowGeneric(profile, req, *cached, pack)
		return *cached, nil
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
	deepIn := e.buildDeepInput(profile, req, pack, pre, ruleRes, dec, score)
	deepOut, err := e.deep.Invoke(deepCtx, deepIn)
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

	res := e.finalizeDeep(ctx, profile, req, ruleRes, pols, pre, traces, pack, arbRationale, conflict, deepOut)
	e.maybePutCBCache(ctx, ruleRes, res, pack)
	e.runShadowGeneric(profile, req, res, pack)
	return res, nil
}

// OrchestrateCrossBorder 兼容旧入口。
func (e *RiskEngine) OrchestrateCrossBorder(ctx context.Context, txn tools.CrossBorderTransaction) (tools.ScreeningResult, error) {
	return e.Orchestrate(ctx, crossBorderProfile{}, txn, false)
}

func (e *RiskEngine) buildDeepInput(profile DomainProfile, req any, pack *PolicyPack, pre PreAnalysis, rule EngineResult, arbDec string, arbScore float64) DeepInput {
	in := DeepInput{
		ProtocolVersion: DeepProtocolV1,
		Domain:          profile.ID(),
		TraceID:         rule.TraceID,
		PackVersion:     pack.Version,
		ListVersion:     rule.ListVersion,
		TransactionID:   profile.TransactionID(req),
		ArbDecision:     arbDec,
		ArbScore:        arbScore,
		RouteBucket:     pre.Bucket,
		Tasks:           []string{"primary", "secondary", "report"},
		CB:              rule.CB,
		Stock:           rule.Stock,
	}
	if in.TraceID == "" {
		in.TraceID = tools.GetUUID()
	}
	switch profile.ID() {
	case tools.BusinessCrossBorder:
		if txn, err := asCB(req); err == nil {
			t := txn
			in.Transaction = &t
		}
	case tools.BusinessStock:
		if o, err := asStock(req); err == nil {
			x := o
			in.StockOrder = &x
		}
	}
	fillDeepPrompts(&in, e.cfg)
	return in
}

func (e *RiskEngine) lookupCBCache(ctx context.Context, ruleRes EngineResult) *tools.ScreeningResult {
	if e.store == nil || ruleRes.CB == nil || ruleRes.CB.Party == nil {
		return nil
	}
	key := cacheKeyForParty(ruleRes.CB.Party, ruleRes.CB.ListVersion)
	if key == "" {
		return nil
	}
	cached, err := e.store.GetScreeningCache(ctx, key)
	if err != nil || cached == nil {
		return nil
	}
	cached.PolicyIDs = appendUnique(append([]string{}, cached.PolicyIDs...), tools.PolicyCacheHit)
	cached.SkippedAI = true
	return cached
}

func (e *RiskEngine) maybePutCBCache(ctx context.Context, ruleRes EngineResult, res tools.ScreeningResult, pack *PolicyPack) {
	if e.store == nil || ruleRes.CB == nil || ruleRes.CB.Party == nil {
		return
	}
	if res.Decision != tools.DecisionApprove && res.Decision != tools.DecisionReject {
		return
	}
	key := cacheKeyForParty(ruleRes.CB.Party, ruleRes.CB.ListVersion)
	if key == "" {
		return
	}
	ttl := time.Duration(pack.CrossBorder.CacheTTLSec) * time.Second
	cacheRes := res
	cacheRes.TraceID = ""
	cacheRes.CaseID = ""
	cacheRes.TotalDurationMs = 0
	cacheRes.PersistedAuditRows = 0
	cacheRes.Engines = nil
	_ = e.store.PutScreeningCache(ctx, key, &cacheRes, ttl)
}

func (e *RiskEngine) finalizeDeep(
	ctx context.Context,
	profile DomainProfile,
	req any,
	ruleRes EngineResult,
	pols []string,
	pre PreAnalysis,
	traces []tools.EngineTrace,
	pack *PolicyPack,
	arbRationale string,
	conflict bool,
	deepOut DeepOutput,
) tools.ScreeningResult {
	traceID := ruleRes.TraceID
	if traceID == "" {
		traceID = deepOut.TraceID
	}
	if traceID == "" {
		traceID = tools.GetUUID()
	}
	score := deepOut.Score
	dec := deepOut.Decision
	if dec == "" {
		dec = tools.DecisionFromScore(score)
	}
	pols = appendUnique(pols, deepOut.PolicyIDs...)
	if dec == tools.DecisionApprove && profile.ID() == tools.BusinessCrossBorder {
		appendPolicyIDs := []string{tools.PolicySanctionsAI}
		pols = appendUnique(pols, appendPolicyIDs...)
	}

	traces = append(traces, tools.EngineTrace{
		Engine:    tools.EngineDeep,
		Decision:  dec,
		Score:     score,
		PolicyIDs: deepOut.PolicyIDs,
		Degraded:  deepOut.Degraded,
		Rationale: profile.ID() + "_deep_" + e.DeepRuntimeName(),
	})

	report := deepOut.ReportMarkdown
	if report == "" {
		report = fmt.Sprintf("## 深度裁决\n- **域**: %s\n- **runtime**: %s\n- **决策**: %s\n- **仲裁**: %s\n",
			profile.ID(), e.DeepRuntimeName(), dec, arbRationale)
	}

	audit := ruleRes.Audit
	if audit == nil && ruleRes.CB != nil {
		audit = ruleRes.CB.Audit
	}
	if audit == nil && ruleRes.Stock != nil {
		audit = ruleRes.Stock.Audit
	}
	if audit == nil {
		audit = &tools.AuditBuffer{}
	}
	audit.AddStep("deep_runtime", store.LogJSON(map[string]any{
		"runtime": e.DeepRuntimeName(), "decision": dec, "score": score, "degraded": deepOut.Degraded,
	}), 0)

	persisted := 0
	if err := e.store.FlushAudit(ctx, traceID, audit); err == nil {
		persisted = len(audit.Steps) + len(audit.Decisions)
	}

	primary := deepOut.Primary
	if primary == nil {
		primary = ruleRes.Primary
	}
	if primary == nil {
		primary = &tools.PrimaryAssessment{RiskScore: score, Rationale: arbRationale}
	}
	secondary := deepOut.Secondary
	if secondary == nil {
		secondary = &tools.SecondaryAssessment{Skipped: false, FinalRiskScore: score, Rationale: "deep_runtime"}
	}

	res := tools.ScreeningResult{
		BusinessType:       profile.ID(),
		TraceID:            traceID,
		TransactionID:      profile.TransactionID(req),
		Decision:           dec,
		PolicyIDs:          pols,
		ListVersion:        ruleRes.ListVersion,
		SkippedAI:          false,
		FinalRiskScore:     score,
		Level:              tools.LevelFromScore(score),
		Primary:            primary,
		Secondary:          secondary,
		ReportMarkdown:     report,
		RouteBucket:        pre.Bucket,
		Engines:            traces,
		Degraded:           deepOut.Degraded,
		PackVersion:        pack.Version,
		PersistedAuditRows: persisted,
		DeepRuntime:        e.DeepRuntimeName(),
		Blocked:            ruleRes.Blocked || (dec == tools.DecisionReject && profile.ID() == tools.BusinessStock),
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
			PayloadJSON: store.LogJSON(map[string]any{"domain": profile.ID(), "req": req, "pre": pre, "engines": traces, "deep": deepOut}),
		})
		res.CaseID = caseID
		if pack.Orchestrator.AsyncCaseDraft {
			e.enqueueCaseDraftGeneric(profile, req, caseID, res)
		}
	}
	_ = conflict
	return res
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
		DeepRuntime:        e.DeepRuntimeName(),
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
