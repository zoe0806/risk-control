package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"

	"risk_control/domain"
	"risk_control/llm"
	"risk_control/store"
)

const (
	nodeIngest          = "ingest"
	nodeNormalize       = "normalize"
	nodeLocalCandidates = "local_candidates"
	nodeAIPrimary       = "ai_primary"
	nodeAISecondary     = "ai_secondary"
	nodeSkipSecondary   = "skip_secondary"
	nodeAIReport        = "ai_report"
	nodePersist         = "persist"
)

// GraphDeps 注入存储与模型路由，便于单测替换。
type GraphDeps struct {
	Store  store.Store
	Router *llm.Router
}

// BuildScreeningGraph 制裁筛查多步编排：清洗 → 本地候选 → AI 初筛 → 条件二验 → 报告 → 审计。
// 通过 compose.NewGraph + AddLambdaNode + AddEdge + AddBranch 构建编排图
func BuildScreeningGraph(ctx context.Context, deps *GraphDeps) (compose.Runnable[domain.ScreeningRequest, domain.ScreeningResult], error) {
	if deps == nil || deps.Router == nil || deps.Store == nil {
		return nil, fmt.Errorf("workflow deps incomplete")
	}
	//compose.NewGraph 创建编排图
	g := compose.NewGraph[domain.ScreeningRequest, domain.ScreeningResult]()

	//AddLambdaNode 添加Lambda节点,对应业务步骤:清洗
	if err := g.AddLambdaNode(nodeIngest, compose.InvokableLambda(func(ctx context.Context, in domain.ScreeningRequest) (*domain.PipelineState, error) {
		if in.TransactionID == "" {
			in.TransactionID = "txn-demo-" + uuid.New().String()[:8]
		}
		return &domain.PipelineState{
			TraceID:     uuid.New().String(),
			Request:     in,
			StepTimings: map[string]time.Duration{},
		}, nil
	}), compose.WithNodeName(nodeIngest)); err != nil {
		return nil, err
	}
	//对应业务步骤:标准化
	if err := g.AddLambdaNode(nodeNormalize, compose.InvokableLambda(func(ctx context.Context, st *domain.PipelineState) (*domain.PipelineState, error) {
		t0 := time.Now()
		st.Party = domain.NormalizePartyName(st.Request.Counterparty, st.Request.Country)
		recordStep(st, nodeNormalize, t0)
		return st, nil
	}), compose.WithNodeName(nodeNormalize)); err != nil {
		return nil, err
	}
	//对应业务步骤:本地候选
	if err := g.AddLambdaNode(nodeLocalCandidates, compose.InvokableLambda(func(ctx context.Context, st *domain.PipelineState) (*domain.PipelineState, error) {
		t0 := time.Now()
		hits, err := deps.Store.SearchSanctions(ctx, st.Party, 48)
		if err != nil {
			return nil, err
		}
		st.Candidates = hits
		recordStep(st, nodeLocalCandidates, t0)
		_ = deps.Store.InsertAuditStep(ctx, st.TraceID, nodeLocalCandidates, store.LogJSON(map[string]any{
			"candidate_count": len(hits),
			"normalized_key":  st.Party.NormalizedKey,
		}), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeLocalCandidates)); err != nil {
		return nil, err
	}
	//对应业务步骤:AI初筛
	if err := g.AddLambdaNode(nodeAIPrimary, compose.InvokableLambda(func(ctx context.Context, st *domain.PipelineState) (*domain.PipelineState, error) {
		t0 := time.Now()
		msgs := llm.PrimaryMessages(st)
		out, err := deps.Router.For(llm.TaskSanctionsPrimary).Generate(ctx, msgs)
		if err != nil {
			return nil, err
		}
		raw := out.Content
		var pr domain.PrimaryAssessment
		if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &pr); err != nil {
			return nil, fmt.Errorf("primary json: %w", err)
		}
		pr.RawModelOutput = raw
		st.Primary = &pr
		recordStep(st, nodeAIPrimary, t0)
		_ = deps.Store.InsertAIDecision(ctx, st.TraceID, string(llm.TaskSanctionsPrimary), deps.Router.ModelName(llm.TaskSanctionsPrimary),
			truncSummary(msgs), raw, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAIPrimary)); err != nil {
		return nil, err
	}
	//对应业务步骤:AI二验
	if err := g.AddLambdaNode(nodeAISecondary, compose.InvokableLambda(func(ctx context.Context, st *domain.PipelineState) (*domain.PipelineState, error) {
		t0 := time.Now()
		msgs := llm.VerifyMessages(st)
		out, err := deps.Router.For(llm.TaskSanctionsVerify).Generate(ctx, msgs)
		if err != nil {
			return nil, err
		}
		raw := out.Content
		var sec domain.SecondaryAssessment
		if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &sec); err != nil {
			return nil, fmt.Errorf("secondary json: %w", err)
		}
		sec.Skipped = false
		sec.RawModelOutput = raw
		st.Secondary = &sec
		recordStep(st, nodeAISecondary, t0)
		_ = deps.Store.InsertAIDecision(ctx, st.TraceID, string(llm.TaskSanctionsVerify), deps.Router.ModelName(llm.TaskSanctionsVerify),
			truncSummary(msgs), raw, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAISecondary)); err != nil {
		return nil, err
	}
	//对应业务步骤:跳过二验
	if err := g.AddLambdaNode(nodeSkipSecondary, compose.InvokableLambda(func(ctx context.Context, st *domain.PipelineState) (*domain.PipelineState, error) {
		t0 := time.Now()
		st.Secondary = &domain.SecondaryAssessment{
			Skipped:        true,
			Confirmed:      false,
			FinalRiskScore: st.Primary.RiskScore,
			Rationale:      "未达到二次模型触发阈值，跳过二验。",
		}
		recordStep(st, nodeSkipSecondary, t0)
		return st, nil
	}), compose.WithNodeName(nodeSkipSecondary)); err != nil {
		return nil, err
	}
	//对应业务步骤:AI报告
	if err := g.AddLambdaNode(nodeAIReport, compose.InvokableLambda(func(ctx context.Context, st *domain.PipelineState) (*domain.PipelineState, error) {
		t0 := time.Now()
		msgs := llm.ReportMessages(st)
		out, err := deps.Router.For(llm.TaskReport).Generate(ctx, msgs)
		if err != nil {
			return nil, err
		}
		st.ReportMarkdown = out.Content
		recordStep(st, nodeAIReport, t0)
		_ = deps.Store.InsertAIDecision(ctx, st.TraceID, string(llm.TaskReport), deps.Router.ModelName(llm.TaskReport),
			truncSummary(msgs), out.Content, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(nodeAIReport)); err != nil {
		return nil, err
	}
	//对应业务步骤:持久化
	if err := g.AddLambdaNode(nodePersist, compose.InvokableLambda(func(ctx context.Context, st *domain.PipelineState) (domain.ScreeningResult, error) {
		t0 := time.Now()
		payload := store.LogJSON(st)
		_ = deps.Store.InsertAuditStep(ctx, st.TraceID, "pipeline_snapshot", payload, time.Since(t0).Milliseconds())
		//对应业务步骤:结果归集
		res := finalizeResult(st)
		res.PersistedAuditRows = 1
		return res, nil
	}), compose.WithNodeName(nodePersist)); err != nil {
		return nil, err
	}
	//AddBranch 添加分支节点,对应业务步骤:二验或跳过二验
	branch := compose.NewGraphBranch(func(ctx context.Context, st *domain.PipelineState) (string, error) {
		if st.Primary != nil && st.Primary.NeedsSecondaryReview && st.Primary.RiskScore >= 0.55 {
			return nodeAISecondary, nil
		}
		return nodeSkipSecondary, nil
	}, map[string]bool{nodeAISecondary: true, nodeSkipSecondary: true})

	for _, step := range []struct {
		fn func() error
	}{
		{func() error { return g.AddEdge(compose.START, nodeIngest) }},
		{func() error { return g.AddEdge(nodeIngest, nodeNormalize) }},
		{func() error { return g.AddEdge(nodeNormalize, nodeLocalCandidates) }},
		{func() error { return g.AddEdge(nodeLocalCandidates, nodeAIPrimary) }},
		{func() error { return g.AddBranch(nodeAIPrimary, branch) }},
		{func() error { return g.AddEdge(nodeAISecondary, nodeAIReport) }},
		{func() error { return g.AddEdge(nodeSkipSecondary, nodeAIReport) }},
		{func() error { return g.AddEdge(nodeAIReport, nodePersist) }},
		{func() error { return g.AddEdge(nodePersist, compose.END) }},
	} {
		if err := step.fn(); err != nil {
			return nil, err
		}
	}
	//Compile 编译图,对应业务步骤:编译图
	return g.Compile(ctx, compose.WithGraphName("sanctions_screening_v1"), compose.WithMaxRunSteps(64))
}

func recordStep(st *domain.PipelineState, name string, t0 time.Time) {
	if st.StepTimings == nil {
		st.StepTimings = map[string]time.Duration{}
	}
	st.StepTimings[name] = time.Since(t0)
}

func finalizeResult(st *domain.PipelineState) domain.ScreeningResult {
	score := 0.0
	if st.Primary != nil {
		score = st.Primary.RiskScore
	}
	if st.Secondary != nil && !st.Secondary.Skipped {
		score = st.Secondary.FinalRiskScore
	}
	level := "LOW"
	if score >= 0.65 {
		level = "HIGH"
	} else if score >= 0.35 {
		level = "MEDIUM"
	}
	return domain.ScreeningResult{
		TraceID:        st.TraceID,
		TransactionID:  st.Request.TransactionID,
		FinalRiskScore: score,
		Level:          level,
		Primary:        st.Primary,
		Secondary:      st.Secondary,
		ReportMarkdown: st.ReportMarkdown,
	}
}

func truncSummary(msgs any) string {
	b, err := json.Marshal(msgs)
	if err != nil {
		return ""
	}
	const max = 4000
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated)"
}
