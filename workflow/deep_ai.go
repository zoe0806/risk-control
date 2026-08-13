package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
)

func applyCBPrimary(ctx context.Context, deps *GraphDeps, st *tools.PipelineState) error {
	t0 := time.Now()
	msgs := llm.PrimaryMessages(st, deps.Cfg)
	out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskSanctionsPrimary), msgs, llm.DefaultRetryConfig())
	if err != nil {
		return err
	}
	raw := out.Content
	var pr tools.PrimaryAssessment
	if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &pr); err != nil {
		return fmt.Errorf("primary json: %w", err)
	}
	pr.RawModelOutput = raw
	st.Primary = &pr
	recordStep(st, nodeAIPrimary, t0)
	st.Audit.AddDecision(string(tools.TaskSanctionsPrimary), deps.Router.ModelName(tools.TaskSanctionsPrimary),
		tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
	return nil
}

func applyCBSecondary(ctx context.Context, deps *GraphDeps, st *tools.PipelineState) {
	t0 := time.Now()
	msgs := llm.VerifyMessages(st, deps.Cfg)
	out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskSanctionsVerify), msgs, llm.DefaultRetryConfig())
	if err != nil {
		st.Secondary = degradedSecondary(st, err)
		recordStep(st, nodeAISecondary, t0)
		st.Audit.AddStep("ai_secondary_degraded", store.LogJSON(map[string]any{"error": err.Error()}), time.Since(t0).Milliseconds())
		return
	}
	raw := out.Content
	var sec tools.SecondaryAssessment
	if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &sec); err != nil {
		st.Secondary = degradedSecondary(st, err)
		recordStep(st, nodeAISecondary, t0)
		st.Audit.AddStep("ai_secondary_degraded", store.LogJSON(map[string]any{"error": fmt.Sprintf("json: %v", err)}), time.Since(t0).Milliseconds())
		return
	}
	sec.Skipped = false
	sec.TechnicalDegraded = false
	sec.RawModelOutput = raw
	st.Secondary = &sec
	recordStep(st, nodeAISecondary, t0)
	st.Audit.AddDecision(string(tools.TaskSanctionsVerify), deps.Router.ModelName(tools.TaskSanctionsVerify),
		tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
}

func applyCBSkipSecondary(st *tools.PipelineState) {
	t0 := time.Now()
	score := 0.0
	if st.Primary != nil {
		score = st.Primary.RiskScore
	}
	st.Secondary = &tools.SecondaryAssessment{
		Skipped:           true,
		Confirmed:         false,
		FinalRiskScore:    score,
		Rationale:         "未达到二次模型触发阈值，跳过二验。",
		TechnicalDegraded: false,
	}
	recordStep(st, nodeSkipSecondary, t0)
	st.Audit.AddStep(nodeSkipSecondary, store.LogJSON(map[string]any{"reason": "未达到二次模型触发阈值，跳过二验。"}), time.Since(t0).Milliseconds())
}

func applyCBReport(ctx context.Context, deps *GraphDeps, st *tools.PipelineState) error {
	t0 := time.Now()
	msgs := llm.ReportMessages(st, deps.Cfg)
	out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskReport), msgs, llm.DefaultRetryConfig())
	if err != nil {
		return err
	}
	st.ReportMarkdown = out.Content
	ApplyAIDecisionCodes(st)
	recordStep(st, nodeAIReport, t0)
	st.Audit.AddDecision(string(tools.TaskReport), deps.Router.ModelName(tools.TaskReport),
		tools.TruncSummary(msgs), out.Content, time.Since(t0).Milliseconds())
	return nil
}

func cbNeedsSecondary(st *tools.PipelineState, thr float64) bool {
	return st.Primary != nil && st.Primary.NeedsSecondaryReview && st.Primary.RiskScore >= thr
}

func runCBDeepAI(ctx context.Context, deps *GraphDeps, st *tools.PipelineState) error {
	if deps == nil || deps.Router == nil {
		return fmt.Errorf("llm router not configured")
	}
	if st.Audit == nil {
		st.Audit = &tools.AuditBuffer{}
	}
	if err := applyCBPrimary(ctx, deps, st); err != nil {
		return err
	}
	if cbNeedsSecondary(st, primaryRiskThreshold(deps.Cfg)) {
		applyCBSecondary(ctx, deps, st)
	} else {
		applyCBSkipSecondary(st)
	}
	if err := applyCBReport(ctx, deps, st); err != nil {
		return err
	}
	return nil
}

func applyStockPrimary(ctx context.Context, deps *GraphDeps, st *tools.StockPipelineState) error {
	t0 := time.Now()
	msgs := llm.StockPrimaryMessages(st, deps.Cfg)
	out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskStockPrimary), msgs, llm.DefaultRetryConfig())
	if err != nil {
		return err
	}
	raw := out.Content
	var pr tools.PrimaryAssessment
	if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &pr); err != nil {
		return fmt.Errorf("stock primary json: %w", err)
	}
	pr.RawModelOutput = raw
	st.Primary = &pr
	tools.RecordStockStep(st, stAIPrimary, t0)
	st.Audit.AddDecision(string(tools.TaskStockPrimary), deps.Router.ModelName(tools.TaskStockPrimary),
		tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
	return nil
}

func applyStockSecondary(ctx context.Context, deps *GraphDeps, st *tools.StockPipelineState) {
	t0 := time.Now()
	msgs := llm.StockVerifyMessages(st, deps.Cfg)
	out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskStockVerify), msgs, llm.DefaultRetryConfig())
	if err != nil {
		st.Secondary = degradedStockSecondary(st, err)
		tools.RecordStockStep(st, stAISecondary, t0)
		st.Audit.AddStep(stAISecondary, store.LogJSON(map[string]any{"error": err.Error()}), time.Since(t0).Milliseconds())
		return
	}
	raw := out.Content
	var sec tools.SecondaryAssessment
	if err := json.Unmarshal([]byte(llm.ExtractJSONObject(raw)), &sec); err != nil {
		st.Secondary = degradedStockSecondary(st, err)
		tools.RecordStockStep(st, stAISecondary, t0)
		st.Audit.AddStep(stAISecondary, store.LogJSON(map[string]any{"error": fmt.Sprintf("json: %v", err)}), time.Since(t0).Milliseconds())
		return
	}
	sec.Skipped = false
	sec.TechnicalDegraded = false
	sec.RawModelOutput = raw
	st.Secondary = &sec
	tools.RecordStockStep(st, stAISecondary, t0)
	st.Audit.AddDecision(string(tools.TaskStockVerify), deps.Router.ModelName(tools.TaskStockVerify),
		tools.TruncSummary(msgs), raw, time.Since(t0).Milliseconds())
}

func applyStockSkipSecondary(st *tools.StockPipelineState) {
	t0 := time.Now()
	score := 0.0
	if st.Primary != nil {
		score = st.Primary.RiskScore
	}
	st.Secondary = &tools.SecondaryAssessment{
		Skipped:           true,
		Confirmed:         false,
		FinalRiskScore:    score,
		Rationale:         "未达到二验触发条件，跳过。",
		TechnicalDegraded: false,
	}
	tools.RecordStockStep(st, stSkipSecondary, t0)
}

func applyStockReport(ctx context.Context, deps *GraphDeps, st *tools.StockPipelineState) error {
	t0 := time.Now()
	msgs := llm.StockReportMessages(st, deps.Cfg)
	out, err := llm.GenerateWithRetry(ctx, deps.Router.For(tools.TaskStockReport), msgs, llm.DefaultRetryConfig())
	if err != nil {
		return err
	}
	st.ReportMarkdown = out.Content
	tools.RecordStockStep(st, stAIReport, t0)
	st.Audit.AddDecision(string(tools.TaskStockReport), deps.Router.ModelName(tools.TaskStockReport),
		tools.TruncSummary(msgs), out.Content, time.Since(t0).Milliseconds())
	return nil
}

func runStockDeepAI(ctx context.Context, deps *GraphDeps, st *tools.StockPipelineState) error {
	if deps == nil || deps.Router == nil {
		return fmt.Errorf("llm router not configured")
	}
	if st.Audit == nil {
		st.Audit = &tools.AuditBuffer{}
	}
	if err := applyStockPrimary(ctx, deps, st); err != nil {
		return err
	}
	if stockNeedsSecondary(st, primaryStockRiskThreshold(deps.Cfg)) {
		applyStockSecondary(ctx, deps, st)
	} else {
		applyStockSkipSecondary(st)
	}
	return applyStockReport(ctx, deps, st)
}
