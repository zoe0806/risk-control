package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"

	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"

	"risk_control/config"
)

const (
	stockGraphName    = "stock_risk_v1"
	stockSubgraphName = "stock_subgraph_v1"

	stIngest        = "st_ingest"
	stNormalize     = "st_normalize"
	stLocalGate     = "st_local_gate"
	stBlockedReport = "st_blocked_report"
	stAIPrimary     = "st_ai_primary"
	stAISecondary   = "st_ai_secondary"
	stSkipSecondary = "st_skip_secondary"
	stAIReport      = "st_ai_report"
	stPersist       = "st_persist"
)

// BuildStockRiskGraph 股票风控：清洗 →【嵌套】本地闸门子图→ 分支(硬阻断/AI链路) → 报告 → 审计。
func BuildStockRiskGraph(ctx context.Context, deps *GraphDeps) (compose.Runnable[tools.StockOrder, tools.ScreeningResult], error) {
	if deps == nil || deps.Router == nil || deps.Store == nil {
		return nil, fmt.Errorf("workflow deps incomplete")
	}
	retryCfg := llm.DefaultRetryConfig()
	thr := primaryStockRiskThreshold(deps.Cfg)

	//构建本地子图
	localSG, err := BuildStockLocalGateGraph(ctx)
	if err != nil {
		return nil, err
	}

	g := compose.NewGraph[tools.StockOrder, tools.ScreeningResult]()

	if err := g.AddLambdaNode(stIngest, compose.InvokableLambda(func(ctx context.Context, in tools.StockOrder) (*tools.StockPipelineState, error) {
		return &tools.StockPipelineState{
			TraceID:     tools.GetUUID(),
			Order:       in,
			Gate:        &tools.StockLocalGate{},
			StepTimings: map[string]time.Duration{},
			Audit:       &tools.AuditBuffer{},
		}, nil
	}), compose.WithNodeName(stIngest)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stNormalize, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		norm, err := tools.NormalizeStockOrder(st.Order)
		if err != nil {
			return nil, err
		}
		st.Norm = norm
		tools.RecordStockStep(st, stNormalize, t0)
		return st, nil
	}), compose.WithNodeName(stNormalize)); err != nil {
		return nil, err
	}

	//这里使用本地子图，使用本地数据进行风控
	if err := g.AddGraphNode(stLocalGate, localSG, compose.WithNodeName(stockSubgraphName)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stBlockedReport, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		st.ReportMarkdown = fmt.Sprintf("## 订单已阻断\n- **原因**: %s\n- **标的**: %s\n- **闸门命中**: 见审计 `stock_sub_*` 步骤。\n",
			st.Gate.BlockReason, st.Order.Symbol)
		tools.RecordStockStep(st, stBlockedReport, t0)
		st.Audit.AddStep(stBlockedReport, store.LogJSON(map[string]any{"reason": st.Gate.BlockReason}), time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(stBlockedReport)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stAIPrimary, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		msgs := llm.StockPrimaryMessages(st, deps.Cfg)
		out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskStockPrimary), msgs, retryCfg)
		if err != nil {
			return nil, err
		}
		raw := out.Content
		var pr tools.PrimaryAssessment
		if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &pr); err != nil {
			return nil, fmt.Errorf("stock primary json: %w", err)
		}
		pr.RawModelOutput = raw
		st.Primary = &pr
		tools.RecordStockStep(st, stAIPrimary, t0)
		st.Audit.AddDecision(string(tools.TaskStockPrimary), deps.Router.ModelName(tools.TaskStockPrimary),
			tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(stAIPrimary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stAISecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		msgs := llm.StockVerifyMessages(st, deps.Cfg)
		out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskStockVerify), msgs, retryCfg)
		if err != nil {
			st.Secondary = degradedStockSecondary(st, err)
			tools.RecordStockStep(st, stAISecondary, t0)
			st.Audit.AddStep(stAISecondary, store.LogJSON(map[string]any{"error": err.Error()}), time.Since(t0).Milliseconds())
			return st, nil
		}
		raw := out.Content
		var sec tools.SecondaryAssessment
		if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &sec); err != nil {
			st.Secondary = degradedStockSecondary(st, err)
			tools.RecordStockStep(st, stAISecondary, t0)
			st.Audit.AddStep(stAISecondary, store.LogJSON(map[string]any{"error": fmt.Sprintf("json: %v", err)}), time.Since(t0).Milliseconds())
			return st, nil
		}
		sec.Skipped = false
		sec.TechnicalDegraded = false
		sec.RawModelOutput = raw
		st.Secondary = &sec
		tools.RecordStockStep(st, stAISecondary, t0)
		st.Audit.AddDecision(string(tools.TaskStockVerify), deps.Router.ModelName(tools.TaskStockVerify),
			tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(stAISecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stSkipSecondary, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		st.Secondary = &tools.SecondaryAssessment{
			Skipped:           true,
			Confirmed:         false,
			FinalRiskScore:    st.Primary.RiskScore,
			Rationale:         "未达到二验触发条件，跳过。",
			TechnicalDegraded: false,
		}
		tools.RecordStockStep(st, stSkipSecondary, t0)
		return st, nil
	}), compose.WithNodeName(stSkipSecondary)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stAIReport, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		msgs := llm.StockReportMessages(st, deps.Cfg)
		out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskStockReport), msgs, retryCfg)
		if err != nil {
			return nil, err
		}
		st.ReportMarkdown = out.Content
		tools.RecordStockStep(st, stAIReport, t0)
		st.Audit.AddDecision(string(tools.TaskStockReport), deps.Router.ModelName(tools.TaskStockReport),
			tools.TruncSummary(msgs), out.Content, time.Since(t0).Milliseconds())
		return st, nil
	}), compose.WithNodeName(stAIReport)); err != nil {
		return nil, err
	}

	if err := g.AddLambdaNode(stPersist, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (tools.ScreeningResult, error) {
		t0 := time.Now()
		st.Audit.AddStep(stPersist, store.LogJSON(st), time.Since(t0).Milliseconds())

		if err := deps.Store.FlushAudit(ctx, st.TraceID, st.Audit); err != nil {
			return tools.ScreeningResult{}, err
		}
		res := finalizeStockScreeningResult(st)
		res.PersistedAuditRows = len(st.Audit.Steps) + len(st.Audit.Decisions)
		return res, nil
	}), compose.WithNodeName(stPersist)); err != nil {
		return nil, err
	}

	//创建分支，黑名单命中则阻断
	branchGate := compose.NewGraphBranch(func(ctx context.Context, st *tools.StockPipelineState) (string, error) {
		if st.Gate != nil && st.Gate.HardBlock {
			return stBlockedReport, nil
		}
		return stAIPrimary, nil
	}, map[string]bool{stBlockedReport: true, stAIPrimary: true})

	//创建分支，根据阈值结果决定是否进行AI二次验证
	branchSecondary := compose.NewGraphBranch(func(ctx context.Context, st *tools.StockPipelineState) (string, error) {
		if stockNeedsSecondary(st, thr) {
			return stAISecondary, nil
		}
		return stSkipSecondary, nil
	}, map[string]bool{stAISecondary: true, stSkipSecondary: true})

	//注册边和分支
	for _, step := range []struct {
		fn func() error
	}{
		{func() error { return g.AddEdge(compose.START, stIngest) }},
		{func() error { return g.AddEdge(stIngest, stNormalize) }},
		{func() error { return g.AddEdge(stNormalize, stLocalGate) }},
		{func() error { return g.AddBranch(stLocalGate, branchGate) }},
		{func() error { return g.AddEdge(stBlockedReport, stPersist) }},
		{func() error { return g.AddBranch(stAIPrimary, branchSecondary) }},
		{func() error { return g.AddEdge(stAISecondary, stAIReport) }},
		{func() error { return g.AddEdge(stSkipSecondary, stAIReport) }},
		{func() error { return g.AddEdge(stAIReport, stPersist) }},
		{func() error { return g.AddEdge(stPersist, compose.END) }},
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

// stockNeedsSecondary 限制清单命中且风险评分大于0.45则强制进入AI二次验证
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
	res.Level = "LOW"
	if score >= 0.65 {
		res.Level = "HIGH"
	} else if score >= 0.35 {
		res.Level = "MEDIUM"
	}
	return res
}
