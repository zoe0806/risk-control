package tools

import "time"

// 单笔跨境交易对手筛查请求
type CrossBorderTransaction struct {
	TransactionID   string `json:"transaction_id"`
	Counterparty    string `json:"counterparty"` // 名称，可多语言
	Country         string `json:"country"`      // ISO2 或文本
	BankName        string `json:"bank_name,omitempty"`
	PaymentPurpose  string `json:"payment_purpose,omitempty"`
	AmountMinorUnit int64  `json:"amount_minor_unit,omitempty"`
	Currency        string `json:"currency,omitempty"`
	// 行为特征（阶段1 频次/速度闸门）
	AccountID string `json:"account_id,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
	ClientIP  string `json:"client_ip,omitempty"`
}

// NormalizedParty 清洗与标准化后的对手方信息。
type NormalizedParty struct {
	DisplayName       string   `json:"display_name"`
	NormalizedKey     string   `json:"normalized_key"`
	Tokens            []string `json:"tokens"`
	CountryNormalized string   `json:"country_normalized"`
}

// SanctionCandidate 本地名单缓存命中（粗筛候选，供 AI 精排）。
type SanctionCandidate struct {
	ID               int64    `json:"id"`
	ListCode         string   `json:"list_code"`
	ListVersion      string   `json:"list_version,omitempty"`
	NameOriginal     string   `json:"name_original"`
	NameNormalized   string   `json:"name_normalized"`
	Aliases          []string `json:"aliases,omitempty"`
	MatchScore       float64  `json:"match_score,omitempty"`
	MatchExplanation string   `json:"match_explanation,omitempty"`
}

// CBGateHit 本地闸门命中项。
type CBGateHit struct {
	PolicyID string `json:"policy_id"`
	Detail   string `json:"detail"`
}

// CBLocalGate 跨境本地确定性闸门结果（阶段1）。
type CBLocalGate struct {
	HardBlock     bool        `json:"hard_block"`
	WhitelistPass bool        `json:"whitelist_pass"`
	EarlyExit     bool        `json:"early_exit"` // 白/黑/硬规则：无需名单/AI
	ForceAI       bool        `json:"force_ai"`
	SkipAI        bool        `json:"skip_ai"` // 名单低分/无候选：跳过 LLM
	AutoReject    bool        `json:"auto_reject"`
	BlockReason   string      `json:"block_reason,omitempty"`
	Hits          []CBGateHit `json:"hits,omitempty"`
	LocalRiskScore float64    `json:"local_risk_score"`
	BestMatchScore float64    `json:"best_match_score,omitempty"`
}

// PrimaryAssessment AI 初筛结构化结果。
type PrimaryAssessment struct {
	RiskScore            float64  `json:"risk_score"`
	MatchedNames         []string `json:"matched_names"`
	Rationale            string   `json:"rationale"`
	NeedsSecondaryReview bool     `json:"needs_secondary_review"`
	RawModelOutput       string   `json:"raw_model_output,omitempty"`
}

// SecondaryAssessment 高风险二次验证结果。
type SecondaryAssessment struct {
	Confirmed      bool    `json:"confirmed"`
	FinalRiskScore float64 `json:"final_risk_score"`
	Rationale      string  `json:"rationale"`
	RawModelOutput string  `json:"raw_model_output,omitempty"`
	Skipped        bool    `json:"skipped"`
	// TechnicalDegraded 为 true 表示二验因超时/解析错误等技术原因未完成，未做 AI 二次验证（业务上区别于「规则跳过」）。
	TechnicalDegraded bool `json:"technical_degraded,omitempty"`
}

// AuditBuffer 流水线内累积的审计条目，仅在 persist 时一次性事务写入。
type AuditBuffer struct {
	Steps     []AuditStepDraft  `json:"-"`
	Decisions []AIDecisionDraft `json:"-"`
}

// AuditStepDraft 审计步骤草稿。
type AuditStepDraft struct {
	StepName   string
	DetailJSON string
	LatencyMs  int64
}

// AIDecisionDraft AI 决策行草稿。
type AIDecisionDraft struct {
	TaskKind     string
	ModelName    string
	InputSummary string
	OutputText   string
	LatencyMs    int64
}

// GraphObservation 一次 Invoke 的节点级观测（由 Eino compose.WithCallbacks 填充）。
type GraphObservation struct {
	NodeSpans []NodeSpanObservation `json:"node_spans,omitempty"`
	Edges     []EdgeObservation     `json:"edges,omitempty"`
}

// NodeSpanObservation 节点耗时 / 错误。
type NodeSpanObservation struct {
	Node       string `json:"node"`
	Component  string `json:"component,omitempty"`
	Type       string `json:"type,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// EdgeObservation 控制流边（上一节点 → 当前节点），便于绘制实际路径。
type EdgeObservation struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// PipelineState 贯穿 Graph 的状态载体（强类型、可序列化进审计）。
type PipelineState struct {
	TraceID string `json:"trace_id"`

	Transaction CrossBorderTransaction `json:"transaction"`
	Party       *NormalizedParty       `json:"party,omitempty"`
	Candidates  []SanctionCandidate    `json:"candidates,omitempty"`
	Gate        *CBLocalGate           `json:"gate,omitempty"`

	Primary   *PrimaryAssessment   `json:"primary,omitempty"`
	Secondary *SecondaryAssessment `json:"secondary,omitempty"`

	ReportMarkdown string `json:"report_markdown,omitempty"`

	// 稳定决策契约（阶段1/2）
	Decision    string   `json:"decision,omitempty"` // APPROVE|REVIEW|REJECT
	PolicyIDs   []string `json:"policy_ids,omitempty"`
	ListVersion string   `json:"list_version,omitempty"`
	CaseID      string   `json:"case_id,omitempty"`
	SkippedAI   bool     `json:"skipped_ai,omitempty"`
	FromCache   bool     `json:"from_cache,omitempty"`
	FinalScoreHint float64 `json:"-"` // 缓存命中时带入分数

	StepTimings map[string]time.Duration `json:"step_timings,omitempty"`

	Audit *AuditBuffer `json:"-"`
}

// Task 用于模型分层路由：未来可将轻量任务映射到更便宜模型。
type Task string

const (
	TaskSanctionsPrimary Task = "sanctions_primary"
	TaskSanctionsVerify  Task = "sanctions_verify"
	TaskReport           Task = "sanctions_report"
)
