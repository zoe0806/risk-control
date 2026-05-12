package tools

import (
	"fmt"
	"strings"
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
		return nil
	case BusinessCrossBorder:
		if strings.TrimSpace(r.Transaction.Counterparty) == "" {
			return fmt.Errorf("transaction.counterparty is required")
		}
		return nil
	default:
		return fmt.Errorf("invalid business kind %q", kind)
	}
}

// ForCrossBorderGraph 返回跨境制裁图 Invoke 用请求副本（仅保留跨境相关字段）。
func (r ScreeningRequest) ForCrossBorderGraph() ScreeningRequest {
	return ScreeningRequest{
		BusinessType: BusinessCrossBorder,
		Transaction:  r.Transaction,
	}
}

// ScreeningResult 统一对外筛查结果（跨境制裁与股票风控共用 JSON 形态）。
// TransactionID：跨境为交易号；股票为订单号 order_id。
// Blocked / BlockReason：仅股票硬阻断等场景有值。
type ScreeningResult struct {
	BusinessType string `json:"business_type,omitempty"` // cross_border | stock

	TraceID       string `json:"trace_id"`
	TransactionID string `json:"transaction_id"`

	Blocked     bool   `json:"blocked,omitempty"`
	BlockReason string `json:"block_reason,omitempty"`

	FinalRiskScore     float64              `json:"final_risk_score"`
	Level              string               `json:"level"` // LOW / MEDIUM / HIGH / BLOCKED（股票）
	Primary            *PrimaryAssessment   `json:"primary,omitempty"`
	Secondary          *SecondaryAssessment `json:"secondary,omitempty"`
	ReportMarkdown     string               `json:"report_markdown"`
	TotalDurationMs    int64                `json:"total_duration_ms"`
	PersistedAuditRows int                  `json:"persisted_audit_rows"`
}
