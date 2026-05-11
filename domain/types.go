package domain

import "time"

// ScreeningRequest 单笔跨境交易对手筛查请求（演示用最小字段集）。
type ScreeningRequest struct {
	TransactionID   string `json:"transaction_id"`
	Counterparty    string `json:"counterparty"`    // 名称，可多语言
	Country         string `json:"country"`         // ISO2 或文本
	BankName        string `json:"bank_name,omitempty"`
	PaymentPurpose  string `json:"payment_purpose,omitempty"`
	AmountMinorUnit int64  `json:"amount_minor_unit,omitempty"`
	Currency        string `json:"currency,omitempty"`
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
	ID               int64  `json:"id"`
	ListCode         string `json:"list_code"`
	NameOriginal     string `json:"name_original"`
	NameNormalized   string `json:"name_normalized"`
	MatchExplanation string `json:"match_explanation,omitempty"`
}

// PrimaryAssessment AI 初筛结构化结果。
type PrimaryAssessment struct {
	RiskScore           float64  `json:"risk_score"`
	MatchedNames        []string `json:"matched_names"`
	Rationale           string   `json:"rationale"`
	NeedsSecondaryReview bool    `json:"needs_secondary_review"`
	RawModelOutput      string   `json:"raw_model_output,omitempty"`
}

// SecondaryAssessment 高风险二次验证结果。
type SecondaryAssessment struct {
	Confirmed       bool    `json:"confirmed"`
	FinalRiskScore  float64 `json:"final_risk_score"`
	Rationale       string  `json:"rationale"`
	RawModelOutput  string  `json:"raw_model_output,omitempty"`
	Skipped         bool    `json:"skipped"`
}

// PipelineState 贯穿 Graph 的状态载体（强类型、可序列化进审计）。
type PipelineState struct {
	TraceID string `json:"trace_id"`

	Request   ScreeningRequest    `json:"request"`
	Party     *NormalizedParty    `json:"party,omitempty"`
	Candidates []SanctionCandidate `json:"candidates,omitempty"`

	Primary   *PrimaryAssessment   `json:"primary,omitempty"`
	Secondary *SecondaryAssessment `json:"secondary,omitempty"`

	ReportMarkdown string `json:"report_markdown,omitempty"`

	StepTimings map[string]time.Duration `json:"step_timings,omitempty"`
}

// ScreeningResult 对外返回与持久化摘要。
type ScreeningResult struct {
	TraceID            string    `json:"trace_id"`
	TransactionID      string    `json:"transaction_id"`
	FinalRiskScore     float64   `json:"final_risk_score"`
	Level              string    `json:"level"` // LOW / MEDIUM / HIGH
	Primary            *PrimaryAssessment   `json:"primary,omitempty"`
	Secondary          *SecondaryAssessment `json:"secondary,omitempty"`
	ReportMarkdown     string    `json:"report_markdown"`
	TotalDurationMs    int64     `json:"total_duration_ms"`
	PersistedAuditRows int       `json:"persisted_audit_rows"`
}
