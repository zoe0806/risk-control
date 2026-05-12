package tools

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// NormalizeStockOrder 清洗：代码、方向、时间戳等。
func NormalizeStockOrder(in StockOrder) (*NormalizedStockOrder, error) {
	raw := strings.TrimSpace(strings.ToUpper(in.Symbol))
	raw = strings.ReplaceAll(raw, " ", "")
	parts := strings.Split(raw, ".")
	base := parts[0]
	mkt := "UNKNOWN"
	if len(parts) > 1 {
		switch parts[1] {
		case "SH", "SSE":
			mkt = "SH"
		case "SZ", "SZSE":
			mkt = "SZ"
		}
	}
	if mkt == "UNKNOWN" && len(base) >= 1 {
		switch base[0] {
		case '6':
			mkt = "SH"
		case '0', '3':
			mkt = "SZ"
		}
	}
	side := strings.ToUpper(strings.TrimSpace(in.Side))
	if side != "BUY" && side != "SELL" {
		side = "UNKNOWN"
	}
	ts := in.Timestamp
	if ts <= 0 {
		ts = time.Now().UnixMilli()
	}
	return &NormalizedStockOrder{
		SymbolKey:   base,
		Market:      mkt,
		SideNorm:    side,
		Quantity:    in.Quantity,
		Price:       in.Price,
		TimestampMs: ts,
	}, nil
}

// NewStockPipelineState 初始化股票流水线状态。
func NewStockPipelineState(order StockOrder) *StockPipelineState {
	if order.OrderID == "" {
		order.OrderID = "ord-" + uuid.New().String()[:8]
	}
	return &StockPipelineState{
		TraceID:     uuid.New().String(),
		Order:       order,
		Gate:        &StockLocalGate{},
		StepTimings: map[string]time.Duration{},
		Audit:       &AuditBuffer{},
	}
}
