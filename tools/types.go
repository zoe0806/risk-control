package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// 统一入口 JSON 的业务域（与 HTTP /v1/screen 的 ScreeningRequest 对齐）。
const (
	BusinessStock       = "stock"
	BusinessCrossBorder = "cross_border"
)

// ScreeningRequest 统一风控请求体：按 business_type 选择 stock_order 或 transaction。
type ScreeningRequest struct {
	BusinessType string                 `json:"business_type,omitempty"`
	Transaction  CrossBorderTransaction `json:"transaction"`
	StockOrder   StockOrder             `json:"stock_order,omitempty"`
}

// NewCrossBorderScreeningRequest 由单笔跨境交易构造图入口请求（批处理/兼容旧 JSON 扁平体时可复用）。
func NewCrossBorderScreeningRequest(txn CrossBorderTransaction) ScreeningRequest {
	return ScreeningRequest{
		BusinessType: BusinessCrossBorder,
		Transaction:  txn,
	}
}

// ResolveBusinessType 返回要执行的分支；未填时按「仅一方有有效负载」推断，否则报错要求显式指定。
func (r ScreeningRequest) ResolveBusinessType() (string, error) {
	bt := strings.TrimSpace(strings.ToLower(r.BusinessType))
	if bt != "" {
		switch bt {
		case BusinessStock, BusinessCrossBorder:
			return bt, nil
		default:
			return "", fmt.Errorf("unknown business_type %q", r.BusinessType)
		}
	}
	hasStock := strings.TrimSpace(r.StockOrder.Symbol) != ""
	hasCB := strings.TrimSpace(r.Transaction.Counterparty) != ""
	if hasStock && !hasCB {
		return BusinessStock, nil
	}
	if hasCB && !hasStock {
		return BusinessCrossBorder, nil
	}
	if hasStock && hasCB {
		return "", fmt.Errorf("ambiguous request: set business_type to %q or %q", BusinessStock, BusinessCrossBorder)
	}
	return "", fmt.Errorf("empty request: set business_type and populate stock_order or transaction")
}

// ValidatePayload 校验与 kind 对应的字段是否齐全。
func (r ScreeningRequest) ValidatePayload(kind string) error {
	switch kind {
	case BusinessStock:
		if strings.TrimSpace(r.StockOrder.Symbol) == "" {
			return fmt.Errorf("stock_order.symbol is required")
		}
		if strings.TrimSpace(r.StockOrder.OrderID) == "" {
			return fmt.Errorf("stock_order.order_id is required")
		}
		return nil
	case BusinessCrossBorder:
		if strings.TrimSpace(r.Transaction.Counterparty) == "" {
			return fmt.Errorf("transaction.counterparty is required")
		}
		if r.Transaction.TransactionID == "" {
			return fmt.Errorf("transaction.transaction_id is required")
		}
		return nil
	default:
		return fmt.Errorf("invalid business kind %q", kind)
	}
}

// ScreeningResult 统一对外筛查结果（跨境制裁与股票风控共用 JSON 形态）。
// TransactionID：跨境为交易号；股票为订单号 order_id。
// Blocked / BlockReason：仅股票硬阻断等场景有值。
// Decision / PolicyIDs：跨境稳定决策契约（APPROVE|REVIEW|REJECT）。
type ScreeningResult struct {
	BusinessType string `json:"business_type,omitempty"` // cross_border | stock

	TraceID       string `json:"trace_id"`
	TransactionID string `json:"transaction_id"`

	Blocked     bool   `json:"blocked,omitempty"`
	BlockReason string `json:"block_reason,omitempty"`

	Decision    string   `json:"decision,omitempty"` // APPROVE | REVIEW | REJECT
	PolicyIDs   []string `json:"policy_ids,omitempty"`
	ListVersion string   `json:"list_version,omitempty"`
	CaseID      string   `json:"case_id,omitempty"`
	SkippedAI   bool     `json:"skipped_ai,omitempty"`

	// 阶段3/4：多引擎编排观测
	RouteBucket string         `json:"route_bucket,omitempty"` // fast | light | deep
	Engines     []EngineTrace  `json:"engines,omitempty"`
	Shadow      *ShadowCompare `json:"shadow,omitempty"`
	Degraded    bool           `json:"degraded,omitempty"`
	PackVersion string         `json:"pack_version,omitempty"`

	FinalRiskScore     float64              `json:"final_risk_score"`
	Level              string               `json:"level"` // LOW / MEDIUM / HIGH / BLOCKED（股票）
	Primary            *PrimaryAssessment   `json:"primary,omitempty"`
	Secondary          *SecondaryAssessment `json:"secondary,omitempty"`
	ReportMarkdown     string               `json:"report_markdown"`
	TotalDurationMs    int64                `json:"total_duration_ms"`
	PersistedAuditRows int                  `json:"persisted_audit_rows"`
	DeepRuntime        string               `json:"deep_runtime,omitempty"`
}

// EngineTrace 单引擎执行摘要（可观测 / 仲裁审计）。
type EngineTrace struct {
	Engine    string   `json:"engine"`
	Decision  string   `json:"decision,omitempty"`
	Score     float64  `json:"score"`
	PolicyIDs []string `json:"policy_ids,omitempty"`
	LatencyMs int64    `json:"latency_ms"`
	Degraded  bool     `json:"degraded,omitempty"`
	Rationale string   `json:"rationale,omitempty"`
}

// ShadowCompare 影子策略对比（不影响主决策）。
type ShadowCompare struct {
	Enabled         bool   `json:"enabled"`
	PackVersion     string `json:"pack_version,omitempty"`
	ShadowDecision  string `json:"shadow_decision,omitempty"`
	PrimaryDecision string `json:"primary_decision,omitempty"`
	Differ          bool   `json:"differ"`
	Detail          string `json:"detail,omitempty"`
}

// ReviewCase 人工复核案例（阶段2）。
type ReviewCase struct {
	CaseID        string   `json:"case_id"`
	TraceID       string   `json:"trace_id"`
	TransactionID string   `json:"transaction_id"`
	Status        string   `json:"status"` // OPEN | APPROVED | REJECTED
	DecisionCode  string   `json:"decision_code"`
	PolicyIDs     []string `json:"policy_ids,omitempty"`
	ListVersion   string   `json:"list_version,omitempty"`
	PayloadJSON   string   `json:"payload_json,omitempty"`
	DraftMarkdown string   `json:"draft_markdown,omitempty"` // 阶段4：离线 SAR/案例草稿
	ResolveNote   string   `json:"resolve_note,omitempty"`
	Resolver      string   `json:"resolver,omitempty"`
	CreatedAt     string   `json:"created_at,omitempty"`
	ResolvedAt    string   `json:"resolved_at,omitempty"`
}

// ResolveCaseRequest 人工终裁写回。
type ResolveCaseRequest struct {
	Decision string `json:"decision"` // APPROVE | REJECT
	Resolver string `json:"resolver"`
	Note     string `json:"note,omitempty"`
}

func TruncSummary(msgs any) string {
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

func GetUUID() string {
	return uuid.New().String()
}
