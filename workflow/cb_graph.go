package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"

	"risk_control/config"
	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
)

const (
	cbGraphName = "cb_risk_v1"

	nodeIngest          = "ingest"           //提取 清洗
	nodeNormalize       = "normalize"        //归一化
	nodeLocalCandidates = "local_candidates" //本地候选
	nodeAIPrimary       = "ai_primary"       //AI初筛
	nodeAISecondary     = "ai_secondary"     //AI二次
	nodeSkipSecondary   = "skip_secondary"   //跳过二次
	nodeAIReport        = "ai_report"        //AI报告
	nodePersist         = "persist"          //持久化
)

func primaryRiskThreshold(cfg config.Config) float64 {
	if cfg.PrimaryRiskScore > 0 {
		return cfg.PrimaryRiskScore
	}
	return 0.55
}

// BuildCrossBorderRiskGraph 制裁筛查多步编排
func BuildCrossBorderRiskGraph(ctx context.Context, deps *GraphDeps) (compose.Runnable[tools.CrossBorderTransaction, tools.ScreeningResult], error) {
	if deps == nil || deps.Router == nil || deps.Store == nil {
		return nil, fmt.Errorf("workflow deps incomplete")
	}
	retryCfg := llm.DefaultRetryConfig()
	thr := primaryRiskThreshold(deps.Cfg)

	g := compose.NewGraph[tools.CrossBorderTransaction, tools.ScreeningResult]()

	//AddLambdaNode 注册一个节点，InvokableLambda包装一个函数，函数签名：func(ctx context.Context, in tools.CrossBorderTransaction) (*tools.PipelineState, error)
	if err := g.AddLambdaNode(nodeIngest, compose.InvokableLambda(func(ctx context.Context, in tools.CrossBorderTransaction) (*tools.PipelineState, error) {
		return &tools.PipelineState{
			TraceID:     uuid.New().String(),
			Transaction: in,
			StepTimings: map[string]time.Duration{},
			Audit:       &tools.AuditBuffer{},
		}, nil
	}), compose.WithNodeName(nodeIngest)); err != nil {
		return nil, err
	}

	// 注册第二个节点，PipelineState 作为输入，PipelineState 作为输出
	if err := g.AddLambdaNode(nodeNormalize, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		//归一化交易对手方名称
		st.Party = tools.NormalizePartyName(st.Transaction.Counterparty, st.Transaction.Country)
		recordStep(st, nodeNormalize, t0)
		return st, nil
	}), compose.WithNodeName(nodeNormalize)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeLocalCandidates, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		//搜索制裁名单
		hits, err := deps.Store.SearchSanctions(ctx, st.Party, 48)
		if err != nil {
			return nil, err
		}
		st.Candidates = hits
		recordStep(st, nodeLocalCandidates, t0)
		//审计日志
		st.Audit.AddStep(nodeLocalCandidates, store.LogJSON(map[string]any{
			"candidate_count": len(hits),
			"normalized_key":  st.Party.NormalizedKey,
		}), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeLocalCandidates)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeAIPrimary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		//拼接初筛消息
		msgs := llm.PrimaryMessages(st, deps.Cfg)
		//调用初筛模型
		out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskSanctionsPrimary), msgs, retryCfg)
		if err != nil {
			return nil, err
		}
		raw := out.Content
		var pr tools.PrimaryAssessment //解析初筛结果
		if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &pr); err != nil {
			return nil, fmt.Errorf("primary json: %w", err)
		}
		pr.RawModelOutput = raw
		st.Primary = &pr
		recordStep(st, nodeAIPrimary, t0)
		//审计日志
		st.Audit.AddDecision(string(tools.TaskSanctionsPrimary), deps.Router.ModelName(tools.TaskSanctionsPrimary),
			tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAIPrimary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodeAISecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (*tools.PipelineState, error) {
		t0 := time.Now()
		msgs := llm.VerifyMessages(st, deps.Cfg)
		//调用二次验证模型
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
		//审计日志
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
		recordStep(st, nodeAIReport, t0)
		//审计日志
		st.Audit.AddDecision(string(tools.TaskReport), deps.Router.ModelName(tools.TaskReport),
			tools.TruncSummary(msgs), out.Content, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAIReport)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(nodePersist, compose.InvokableLambda(func(ctx context.Context, st *tools.PipelineState) (tools.ScreeningResult, error) {
		t0 := time.Now()
		payload := store.LogJSON(st)
		st.Audit.AddStep(nodePersist, payload, time.Since(t0).Milliseconds())

		res := finalizeResult(st)

		if err := deps.Store.FlushAudit(ctx, st.TraceID, st.Audit); err != nil {
			return tools.ScreeningResult{}, err
		}

		res.PersistedAuditRows = len(st.Audit.Steps) + len(st.Audit.Decisions)
		return res, nil
	}), compose.WithNodeName(nodePersist)); err != nil {
		return nil, err
	}

	//创建分支，根据初筛结果决定是否进行二次验证
	primaryBranch := compose.NewGraphBranch(func(ctx context.Context, st *tools.PipelineState) (string, error) {
		if st.Primary != nil && st.Primary.NeedsSecondaryReview && st.Primary.RiskScore >= thr {
			return nodeAISecondary, nil
		}
		return nodeSkipSecondary, nil
	}, map[string]bool{nodeAISecondary: true, nodeSkipSecondary: true})

	//图顺序：清洗 → 归一化 → 本地候选 → AI初筛 → 分支(二次验证/跳过二次) → AI报告 → 审计 → 持久化
	for _, step := range []struct {
		fn func() error
	}{
		{func() error { return g.AddEdge(compose.START, nodeIngest) }},
		{func() error { return g.AddEdge(nodeIngest, nodeNormalize) }},
		{func() error { return g.AddEdge(nodeNormalize, nodeLocalCandidates) }},
		{func() error { return g.AddEdge(nodeLocalCandidates, nodeAIPrimary) }},
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

// 记录每个步骤的耗时
func recordStep(st *tools.PipelineState, name string, t0 time.Time) {
	if st.StepTimings == nil {
		st.StepTimings = map[string]time.Duration{}
	}
	st.StepTimings[name] = time.Since(t0)
}

func finalizeResult(st *tools.PipelineState) tools.ScreeningResult {
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
	level := "LOW"
	if score >= 0.65 {
		level = "HIGH"
	} else if score >= 0.35 {
		level = "MEDIUM"
	}
	return tools.ScreeningResult{
		BusinessType:   tools.BusinessCrossBorder,
		TraceID:        st.TraceID,
		TransactionID:  st.Transaction.TransactionID,
		FinalRiskScore: score,
		Level:          level,
		Primary:        st.Primary,
		Secondary:      st.Secondary,
		ReportMarkdown: st.ReportMarkdown,
	}
}
