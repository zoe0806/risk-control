package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"

	"risk_control/config"
	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
)

const (
	cbGraphName = "cb_risk_v1"

	nodeIngest          = "ingest"
	nodeNormalize       = "normalize"
	nodeFastFilter      = "fast_filter"
	nodeEarlyDecide     = "early_decide"
	nodeCacheLookup     = "cache_lookup"
	nodeLocalCandidates = "local_candidates"
	nodeMatchRoute      = "match_route"
	nodeRuleDecide      = "rule_decide"
	nodeAIPrimary       = "ai_primary"
	nodeAISecondary     = "ai_secondary"
	nodeSkipSecondary   = "skip_secondary"
	nodeAIReport        = "ai_report"
	nodePersist         = "persist"
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

// BuildCrossBorderRiskGraph 制裁筛查：本地漏斗 → 可选 AI → 持久化/案例。
func BuildCrossBorderRiskGraph(ctx context.Context, deps *GraphDeps) (compose.Runnable[tools.CrossBorderTransaction, tools.ScreeningResult], error) {
	if deps == nil || deps.Router == nil || deps.Store == nil {
		return nil, fmt.Errorf("workflow deps incomplete")
	}
	if deps.Velocity == nil {
		deps.Velocity = NewVelocityTracker()
	}
	retryCfg := llm.DefaultRetryConfig()
	thr := primaryRiskThreshold(deps.Cfg)
	liveRules := func() config.CrossBorderRules {
		if deps.Policies != nil {
			if p := deps.Policies.Primary(); p != nil {
				return p.CrossBorder
			}
		}
		return deps.Cfg.CBRules()
	}

	g := compose.NewGraph[tools.CrossBorderTransaction, tools.ScreeningResult]()

	if err := g.AddLambdaNode(nodeIngest, compose.InvokableLambda(func(ctx context.Context, in tools.CrossBorderTransaction) (*tools.PipelineState, error) {
		return &tools.PipelineState{
			TraceID:     tools.GetUUID(),
			Transaction: in,
			Gate:        &tools.CBLocalGate{},
			StepTimings: map[string]time.Duration{},
			Audit:       &tools.AuditBuffer{},
		}, nil
	}), compose.WithNodeName(nodeIngest)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeNormalize, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		st.Party = tools.NormalizePartyName(st.Transaction.Counterparty, st.Transaction.Country)
		if ver, err := deps.Store.ActiveListVersion(ctx); err == nil {
			st.ListVersion = ver
		}
		recordStep(st, nodeNormalize, t0)
		return st, nil
	}), compose.WithNodeName(nodeNormalize)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeFastFilter, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		ApplyFastFilter(st, liveRules(), deps.Velocity)
		recordStep(st, nodeFastFilter, t0)
		st.Audit.AddStep(nodeFastFilter, store.LogJSON(st.Gate), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeFastFilter)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeEarlyDecide, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		ApplyRuleDecision(st)
		recordStep(st, nodeEarlyDecide, t0)
		st.Audit.AddStep(nodeEarlyDecide, store.LogJSON(map[string]any{
			"decision": st.Decision,
			"policies": st.PolicyIDs,
		}), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeEarlyDecide)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeCacheLookup, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		key := cacheKeyForParty(st.Party, st.ListVersion)
		cached, err := deps.Store.GetScreeningCache(ctx, key)
		if err != nil {
			st.Audit.AddStep(nodeCacheLookup, store.LogJSON(map[string]any{"error": err.Error()}), time.Since(t0).Milliseconds())
			recordStep(st, nodeCacheLookup, t0)
			return st, nil
		}
		if cached != nil {
			st.FromCache = true
			st.SkippedAI = true
			st.Decision = cached.Decision
			st.PolicyIDs = append([]string{tools.PolicyCacheHit}, cached.PolicyIDs...)
			st.FinalScoreHint = cached.FinalRiskScore
			st.Primary = cached.Primary
			st.Secondary = cached.Secondary
			st.ReportMarkdown = cached.ReportMarkdown
			if st.ReportMarkdown == "" {
				st.ReportMarkdown = "## 缓存命中\n- 复用近期同对手方筛查结果\n"
			}
			appendPolicy(st, tools.PolicyCacheHit)
			st.Audit.AddStep(nodeCacheLookup, store.LogJSON(map[string]any{"hit": true, "key": key}), time.Since(t0).Milliseconds())
		} else {
			st.Audit.AddStep(nodeCacheLookup, store.LogJSON(map[string]any{"hit": false, "key": key}), time.Since(t0).Milliseconds())
		}
		recordStep(st, nodeCacheLookup, t0)
		return st, nil
	}), compose.WithNodeName(nodeCacheLookup)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeLocalCandidates, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		rules := liveRules()
		hits, err := deps.Store.SearchSanctions(ctx, st.Party, rules.CandidateLimit*3)
		if err != nil {
			return nil, err
		}
		st.Candidates = tools.RankCandidates(st.Party, hits, rules.FuzzyMatchMinScore, rules.CandidateLimit)
		if len(st.Candidates) > 0 && st.Candidates[0].ListVersion != "" {
			st.ListVersion = st.Candidates[0].ListVersion
		}
		recordStep(st, nodeLocalCandidates, t0)
		st.Audit.AddStep(nodeLocalCandidates, store.LogJSON(map[string]any{
			"candidate_count": len(st.Candidates),
			"normalized_key":  st.Party.NormalizedKey,
			"list_version":    st.ListVersion,
			"top_score": func() float64 {
				if len(st.Candidates) == 0 {
					return 0
				}
				return st.Candidates[0].MatchScore
			}(),
		}), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeLocalCandidates)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeMatchRoute, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		ApplyMatchRouting(st, liveRules())
		recordStep(st, nodeMatchRoute, t0)
		st.Audit.AddStep(nodeMatchRoute, store.LogJSON(st.Gate), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeMatchRoute)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeRuleDecide, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		ApplyRuleDecision(st)
		recordStep(st, nodeRuleDecide, t0)
		st.Audit.AddStep(nodeRuleDecide, store.LogJSON(map[string]any{
			"decision": st.Decision,
			"policies": st.PolicyIDs,
		}), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeRuleDecide)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeAIPrimary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		msgs := llm.PrimaryMessages(st, deps.Cfg)
		out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskSanctionsPrimary), msgs, retryCfg)
		if err != nil {
			return nil, err
		}
		raw := out.Content
		var pr tools.PrimaryAssessment
		if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &pr); err != nil {
			return nil, fmt.Errorf("primary json: %w", err)
		}
		pr.RawModelOutput = raw
		st.Primary = &pr
		recordStep(st, nodeAIPrimary, t0)
		st.Audit.AddDecision(string(tools.TaskSanctionsPrimary), deps.Router.ModelName(tools.TaskSanctionsPrimary),
			tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAIPrimary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeAISecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		msgs := llm.VerifyMessages(st, deps.Cfg)
		out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskSanctionsVerify), msgs, retryCfg)
		if err != nil {
			st.Secondary = degradedSecondary(st, err)
			recordStep(st, nodeAISecondary, t0)
			st.Audit.AddStep("ai_secondary_degraded", store.LogJSON(map[string]any{
				"error": err.Error(),
			}), time.Since(t0).Milliseconds())
			return st, nil
		}
		raw := out.Content
		var sec tools.SecondaryAssessment
		if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &sec); err != nil {
			st.Secondary = degradedSecondary(st, err)
			recordStep(st, nodeAISecondary, t0)
			st.Audit.AddStep("ai_secondary_degraded", store.LogJSON(map[string]any{
				"error": fmt.Sprintf("json: %v", err),
			}), time.Since(t0).Milliseconds())
			return st, nil
		}
		sec.Skipped = false
		sec.TechnicalDegraded = false
		sec.RawModelOutput = raw
		st.Secondary = &sec
		recordStep(st, nodeAISecondary, t0)
		st.Audit.AddDecision(string(tools.TaskSanctionsVerify), deps.Router.ModelName(tools.TaskSanctionsVerify),
			tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAISecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeSkipSecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		st.Secondary = &tools.SecondaryAssessment{
			Skipped:           true,
			Confirmed:         false,
			FinalRiskScore:    st.Primary.RiskScore,
			Rationale:         "未达到二次模型触发阈值，跳过二验。",
			TechnicalDegraded: false,
		}
		recordStep(st, nodeSkipSecondary, t0)
		st.Audit.AddStep(nodeSkipSecondary, store.LogJSON(map[string]any{
			"reason": "未达到二次模型触发阈值，跳过二验。",
		}), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeSkipSecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeAIReport, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		msgs := llm.ReportMessages(st, deps.Cfg)
		out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskReport), msgs, retryCfg)
		if err != nil {
			return nil, err
		}
		st.ReportMarkdown = out.Content
		ApplyAIDecisionCodes(st)
		recordStep(st, nodeAIReport, t0)
		st.Audit.AddDecision(string(tools.TaskReport), deps.Router.ModelName(tools.TaskReport),
			tools.TruncSummary(msgs), out.Content, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAIReport)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodePersist, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (tools.ScreeningResult, error) {
		t0 := time.Now()
		if st.Decision == "" && !st.FromCache {
			ApplyAIDecisionCodes(st)
		}
		res := finalizeResult(st)

		// REVIEW → 开案例
		if res.Decision == tools.DecisionReview && res.CaseID == "" && !st.FromCache {
			caseID := "CASE-" + tools.GetUUID()
			payload := store.LogJSON(map[string]any{
				"transaction": st.Transaction,
				"party":       st.Party,
				"candidates":  st.Candidates,
				"gate":        st.Gate,
				"primary":     st.Primary,
				"secondary":   st.Secondary,
			})
			rc := &tools.ReviewCase{
				CaseID:        caseID,
				TraceID:       st.TraceID,
				TransactionID: st.Transaction.TransactionID,
				Status:        "OPEN",
				DecisionCode:  tools.DecisionReview,
				PolicyIDs:     st.PolicyIDs,
				ListVersion:   st.ListVersion,
				PayloadJSON:   payload,
			}
			if err := deps.Store.CreateReviewCase(ctx, rc); err != nil {
				return tools.ScreeningResult{}, fmt.Errorf("create review case: %w", err)
			}
			st.CaseID = caseID
			res.CaseID = caseID
		}

		st.Audit.AddStep(nodePersist, store.LogJSON(st), time.Since(t0).Milliseconds())
		if err := deps.Store.FlushAudit(ctx, st.TraceID, st.Audit); err != nil {
			return tools.ScreeningResult{}, err
		}

		// 可缓存的确定性/终态结果（不含 OPEN 案例过程中的灰区也可缓存 APPROVE/REJECT）
		if !st.FromCache && (res.Decision == tools.DecisionApprove || res.Decision == tools.DecisionReject) {
			key := cacheKeyForParty(st.Party, st.ListVersion)
			cacheRes := res
			cacheRes.TraceID = ""
			cacheRes.CaseID = ""
			cacheRes.TotalDurationMs = 0
			cacheRes.PersistedAuditRows = 0
			_ = deps.Store.PutScreeningCache(ctx, key, &cacheRes, time.Duration(liveRules().CacheTTLSec)*time.Second)
		}

		res.PersistedAuditRows = len(st.Audit.Steps) + len(st.Audit.Decisions)
		return res, nil
	}), compose.WithNodeName(nodePersist)); err != nil {
		return nil, err
	}

	earlyBranch := compose.NewGraphBranch(func(ctx context.Context, st *tools.PipelineState) (string, error) {
		if st.Gate != nil && st.Gate.EarlyExit {
			return nodeEarlyDecide, nil
		}
		return nodeCacheLookup, nil
	}, map[string]bool{nodeEarlyDecide: true, nodeCacheLookup: true})

	cacheBranch := compose.NewGraphBranch(func(ctx context.Context, st *tools.PipelineState) (string, error) {
		if st.FromCache {
			return nodePersist, nil
		}
		return nodeLocalCandidates, nil
	}, map[string]bool{nodePersist: true, nodeLocalCandidates: true})

	matchBranch := compose.NewGraphBranch(func(ctx context.Context, st *tools.PipelineState) (string, error) {
		if st.Gate != nil && st.Gate.SkipAI {
			return nodeRuleDecide, nil
		}
		return nodeAIPrimary, nil
	}, map[string]bool{nodeRuleDecide: true, nodeAIPrimary: true})

	primaryBranch := compose.NewGraphBranch(func(ctx context.Context, st *tools.PipelineState) (string, error) {
		if st.Primary != nil && st.Primary.NeedsSecondaryReview && st.Primary.RiskScore >= thr {
			return nodeAISecondary, nil
		}
		return nodeSkipSecondary, nil
	}, map[string]bool{nodeAISecondary: true, nodeSkipSecondary: true})

	for _, step := range []struct {
		fn func() error
	}{
		{func() error { return g.AddEdge(compose.START, nodeIngest) }},
		{func() error { return g.AddEdge(nodeIngest, nodeNormalize) }},
		{func() error { return g.AddEdge(nodeNormalize, nodeFastFilter) }},
		{func() error { return g.AddBranch(nodeFastFilter, earlyBranch) }},
		{func() error { return g.AddEdge(nodeEarlyDecide, nodePersist) }},
		{func() error { return g.AddBranch(nodeCacheLookup, cacheBranch) }},
		{func() error { return g.AddEdge(nodeLocalCandidates, nodeMatchRoute) }},
		{func() error { return g.AddBranch(nodeMatchRoute, matchBranch) }},
		{func() error { return g.AddEdge(nodeRuleDecide, nodePersist) }},
		{func() error { return g.AddBranch(nodeAIPrimary, primaryBranch) }},
		{func() error { return g.AddEdge(nodeAISecondary, nodeAIReport) }},
		{func() error { return g.AddEdge(nodeSkipSecondary, nodeAIReport) }},
		{func() error { return g.AddEdge(nodeAIReport, nodePersist) }},
		{func() error { return g.AddEdge(nodePersist, compose.END) }},
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

func finalizeResult(st *tools.PipelineState) tools.ScreeningResult {
	score := 0.0
	if st.FinalScoreHint > 0 && st.FromCache {
		score = st.FinalScoreHint
	} else if st.Primary != nil {
		score = st.Primary.RiskScore
	}
	if st.Secondary != nil {
		if st.Secondary.TechnicalDegraded {
			if st.Primary != nil {
				score = st.Primary.RiskScore
			}
		} else if !st.Secondary.Skipped {
			score = st.Secondary.FinalRiskScore
		} else if st.Secondary.FinalRiskScore > 0 {
			score = st.Secondary.FinalRiskScore
		}
	}
	decision := st.Decision
	if decision == "" {
		decision = tools.DecisionFromScore(score)
	}
	level := tools.LevelFromScore(score)
	if decision == tools.DecisionReject {
		level = "HIGH"
	}
	return tools.ScreeningResult{
		BusinessType:   tools.BusinessCrossBorder,
		TraceID:        st.TraceID,
		TransactionID:  st.Transaction.TransactionID,
		Decision:       decision,
		PolicyIDs:      st.PolicyIDs,
		ListVersion:    st.ListVersion,
		CaseID:         st.CaseID,
		SkippedAI:      st.SkippedAI || st.FromCache,
		FinalRiskScore: score,
		Level:          level,
		Primary:        st.Primary,
		Secondary:      st.Secondary,
		ReportMarkdown: st.ReportMarkdown,
	}
}
