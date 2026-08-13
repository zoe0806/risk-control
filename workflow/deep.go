package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"risk_control/config"
	"risk_control/llm"
	"risk_control/tools"
)

const (
	DeepProtocolV1 = "risk.deep.v1"

	DeepRuntimeNative = "native"
	DeepRuntimeEino   = "eino"
	DeepRuntimeCLI    = "cli"
	DeepRuntimeCodex  = "codex"
	DeepRuntimeOff    = "off"
)

// DeepRuntime 深度执行器：只消费内核已算完的证据，禁止重跑本地闸门。
// native / eino / cli 均可替换；CLI 用同一 JSON 契约对接 Codex 或自研 runtime。
type DeepRuntime interface {
	Name() string
	Invoke(ctx context.Context, in DeepInput) (DeepOutput, error)
}

// DeepInput 深度请求（可序列化，供 CLI stdin）。
type DeepInput struct {
	ProtocolVersion string   `json:"protocol_version"`
	Domain          string   `json:"domain"`
	TraceID         string   `json:"trace_id"`
	PackVersion     string   `json:"pack_version"`
	ListVersion     string   `json:"list_version,omitempty"`
	TransactionID   string   `json:"transaction_id"`
	ArbDecision     string   `json:"arb_decision"`
	ArbScore        float64  `json:"arb_score"`
	RouteBucket     string   `json:"route_bucket"`
	Tasks           []string `json:"tasks"`

	Transaction *tools.CrossBorderTransaction `json:"transaction,omitempty"`
	StockOrder  *tools.StockOrder             `json:"stock_order,omitempty"`

	CB    *tools.PipelineState      `json:"cb,omitempty"`
	Stock *tools.StockPipelineState `json:"stock,omitempty"`

	Prompts DeepPrompts `json:"prompts,omitempty"`
}

// DeepPrompts 已渲染的提示词，外部 CLI 无需加载本仓库 prompt。
type DeepPrompts struct {
	PrimarySystem string `json:"primary_system,omitempty"`
	PrimaryUser   string `json:"primary_user,omitempty"`
	VerifySystem  string `json:"verify_system,omitempty"`
	VerifyUser    string `json:"verify_user,omitempty"`
	ReportSystem  string `json:"report_system,omitempty"`
	ReportUser    string `json:"report_user,omitempty"`
}

// DeepOutput 深度响应（CLI stdout）。
type DeepOutput struct {
	ProtocolVersion string                     `json:"protocol_version"`
	Decision        string                     `json:"decision"`
	Score           float64                    `json:"score"`
	Primary         *tools.PrimaryAssessment   `json:"primary,omitempty"`
	Secondary       *tools.SecondaryAssessment `json:"secondary,omitempty"`
	ReportMarkdown  string                     `json:"report_markdown,omitempty"`
	PolicyIDs       []string                   `json:"policy_ids,omitempty"`
	Degraded        bool                       `json:"degraded,omitempty"`
	Error           string                     `json:"error,omitempty"`
	RuntimeName     string                     `json:"runtime_name,omitempty"`
	TraceID         string                     `json:"trace_id,omitempty"`
}

func NormalizeDeepKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return DeepRuntimeNative
	}
	return k
}

func NewDeepRuntime(ctx context.Context, deps *GraphDeps) (DeepRuntime, error) {
	if deps == nil {
		return nil, fmt.Errorf("graph deps is nil")
	}
	kind := NormalizeDeepKind(deps.Cfg.DeepRuntime.Kind)
	switch kind {
	case DeepRuntimeOff:
		return offRuntime{}, nil
	case DeepRuntimeCLI:
		return newCLIRuntime(deps.Cfg.DeepRuntime.CLI)
	case DeepRuntimeCodex:
		return newCodexRuntime(deps.Cfg.DeepRuntime.CLI)
	case DeepRuntimeEino:
		return newEinoRuntime(ctx, deps)
	case DeepRuntimeNative:
		return newNativeRuntime(deps)
	default:
		return nil, fmt.Errorf("unknown deep runtime %q (want native|eino|cli|codex|off)", kind)
	}
}

func deepRuntimeNeedsLLM(kind string) bool {
	switch NormalizeDeepKind(kind) {
	case DeepRuntimeNative, DeepRuntimeEino:
		return true
	default:
		return false
	}
}

type offRuntime struct{}

func (offRuntime) Name() string { return DeepRuntimeOff }

func (offRuntime) Invoke(_ context.Context, _ DeepInput) (DeepOutput, error) {
	return DeepOutput{ProtocolVersion: DeepProtocolV1, RuntimeName: DeepRuntimeOff, Error: "deep runtime disabled"}, fmt.Errorf("deep runtime disabled")
}

func hydrateCBState(in DeepInput) *tools.PipelineState {
	if in.CB != nil {
		if in.CB.Audit == nil {
			in.CB.Audit = &tools.AuditBuffer{}
		}
		if in.CB.StepTimings == nil {
			in.CB.StepTimings = map[string]time.Duration{}
		}
		if in.TraceID != "" && in.CB.TraceID == "" {
			in.CB.TraceID = in.TraceID
		}
		return in.CB
	}
	st := &tools.PipelineState{
		TraceID:     in.TraceID,
		Gate:        &tools.CBLocalGate{},
		StepTimings: map[string]time.Duration{},
		Audit:       &tools.AuditBuffer{},
		ListVersion: in.ListVersion,
	}
	if in.Transaction != nil {
		st.Transaction = *in.Transaction
	}
	return st
}

func hydrateStockState(in DeepInput) *tools.StockPipelineState {
	if in.Stock != nil {
		if in.Stock.Audit == nil {
			in.Stock.Audit = &tools.AuditBuffer{}
		}
		if in.Stock.StepTimings == nil {
			in.Stock.StepTimings = map[string]time.Duration{}
		}
		if in.TraceID != "" && in.Stock.TraceID == "" {
			in.Stock.TraceID = in.TraceID
		}
		return in.Stock
	}
	st := &tools.StockPipelineState{
		TraceID:     in.TraceID,
		Gate:        &tools.StockLocalGate{},
		StepTimings: map[string]time.Duration{},
		Audit:       &tools.AuditBuffer{},
	}
	if in.StockOrder != nil {
		st.Order = *in.StockOrder
	}
	return st
}

func deepOutputFromCB(st *tools.PipelineState, runtime string) DeepOutput {
	if st == nil {
		return DeepOutput{ProtocolVersion: DeepProtocolV1, RuntimeName: runtime, Decision: tools.DecisionReview, Score: 0.5}
	}
	score := scoreFromState(st)
	dec := st.Decision
	if dec == "" {
		dec = tools.DecisionFromScore(score)
	}
	degraded := st.Secondary != nil && st.Secondary.TechnicalDegraded
	return DeepOutput{
		ProtocolVersion: DeepProtocolV1,
		Decision:        dec,
		Score:           score,
		Primary:         st.Primary,
		Secondary:       st.Secondary,
		ReportMarkdown:  st.ReportMarkdown,
		PolicyIDs:       append([]string{}, st.PolicyIDs...),
		Degraded:        degraded,
		RuntimeName:     runtime,
		TraceID:         st.TraceID,
	}
}

func deepOutputFromStock(st *tools.StockPipelineState, runtime string) DeepOutput {
	if st == nil {
		return DeepOutput{ProtocolVersion: DeepProtocolV1, RuntimeName: runtime, Decision: tools.DecisionReview, Score: 0.5}
	}
	tmp := finalizeStockScreeningResult(st)
	return DeepOutput{
		ProtocolVersion: DeepProtocolV1,
		Decision:        tmp.Decision,
		Score:           tmp.FinalRiskScore,
		Primary:         st.Primary,
		Secondary:       st.Secondary,
		ReportMarkdown:  st.ReportMarkdown,
		Degraded:        st.Secondary != nil && st.Secondary.TechnicalDegraded,
		RuntimeName:     runtime,
		TraceID:         st.TraceID,
	}
}

func fillDeepPrompts(in *DeepInput, cfg config.Config) {
	if in == nil {
		return
	}
	switch in.Domain {
	case tools.BusinessCrossBorder:
		st := hydrateCBState(*in)
		if st.Party == nil {
			st.Party = tools.NormalizePartyName(st.Transaction.Counterparty, st.Transaction.Country)
		}
		if msgs := llm.PrimaryMessages(st, cfg); len(msgs) >= 2 {
			in.Prompts.PrimarySystem = msgs[0].Content
			in.Prompts.PrimaryUser = msgs[1].Content
		}
		if msgs := llm.VerifyMessages(st, cfg); len(msgs) >= 2 {
			in.Prompts.VerifySystem = msgs[0].Content
			in.Prompts.VerifyUser = msgs[1].Content
		}
		if msgs := llm.ReportMessages(st, cfg); len(msgs) >= 2 {
			in.Prompts.ReportSystem = msgs[0].Content
			in.Prompts.ReportUser = msgs[1].Content
		}
	case tools.BusinessStock:
		st := hydrateStockState(*in)
		if msgs := llm.StockPrimaryMessages(st, cfg); len(msgs) >= 2 {
			in.Prompts.PrimarySystem = msgs[0].Content
			in.Prompts.PrimaryUser = msgs[1].Content
		}
		if msgs := llm.StockVerifyMessages(st, cfg); len(msgs) >= 2 {
			in.Prompts.VerifySystem = msgs[0].Content
			in.Prompts.VerifyUser = msgs[1].Content
		}
		if msgs := llm.StockReportMessages(st, cfg); len(msgs) >= 2 {
			in.Prompts.ReportSystem = msgs[0].Content
			in.Prompts.ReportUser = msgs[1].Content
		}
	}
}

func parseDeepOutputJSON(raw []byte) (DeepOutput, error) {
	raw = bytesTrimJSON(raw)
	candidates := []string{string(raw), llm.ExtractJSONObject(string(raw))}
	s := string(raw)
	if idx := strings.LastIndex(s, "{"); idx >= 0 {
		candidates = append(candidates, s[idx:])
	}
	var lastErr error
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		var out DeepOutput
		if err := json.Unmarshal([]byte(c), &out); err != nil {
			lastErr = err
			continue
		}
		if out.Decision == "" && out.Primary == nil && out.ReportMarkdown == "" {
			continue
		}
		if out.ProtocolVersion == "" {
			out.ProtocolVersion = DeepProtocolV1
		}
		return out, nil
	}
	if lastErr != nil {
		return DeepOutput{}, fmt.Errorf("cli stdout json: %w", lastErr)
	}
	return DeepOutput{}, fmt.Errorf("cli stdout is not DeepOutput JSON")
}

func bytesTrimJSON(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	s = strings.TrimPrefix(s, "\uFEFF")
	return []byte(s)
}
