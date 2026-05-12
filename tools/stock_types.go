package tools

import "time"

// StockOrder 股票订单（演示字段，可扩展盘口/持仓等）。
type StockOrder struct {
	OrderID   string  `json:"order_id"`
	Symbol    string  `json:"symbol"` // 如 600519 或 600519.SH
	Side      string  `json:"side"`   // BUY / SELL
	Quantity  int64   `json:"quantity"`
	Price     float64 `json:"price"`
	AccountID string  `json:"account_id,omitempty"`
	Timestamp int64   `json:"timestamp_ms,omitempty"` // Unix 毫秒，缺省则服务填

	// Flags 事件类约束（演示）
	Flags StockOrderFlags `json:"flags"`

	// DisciplineRules 自然语言纪律，供 AI 初筛（演示）
	DisciplineRules string `json:"discipline_rules,omitempty"`
	// NewsSummary 可选：已聚合的公告/舆情摘要，供本地闸门里「非结构化」启发式打分
	NewsSummary string `json:"news_summary,omitempty"`
}

// StockOrderFlags 事件驱动等标记。
type StockOrderFlags struct {
	BeforeEarnings bool `json:"before_earnings,omitempty"` // 财报窗口内等
}

// NormalizedStockOrder 清洗后的订单视图。
type NormalizedStockOrder struct {
	SymbolKey   string  `json:"symbol_key"` // 大写、去空格，如 600519
	Market      string  `json:"market"`     // SH/SZ/UNKNOWN
	SideNorm    string  `json:"side_norm"`  // BUY / SELL
	Quantity    int64   `json:"quantity"`
	Price       float64 `json:"price"`
	TimestampMs int64   `json:"timestamp_ms"`
}

type StockBanKind string

const (
	StockBanKindAbsolute     StockBanKind = "absolute_ban" //标的在绝对禁止清单
	StockBanKindEvent        StockBanKind = "event_ban"    //财报窗口内禁止
	StockBanKindWatchlist    StockBanKind = "watchlist"    //内部限制清单命中，强制进入 AI 初筛
	StockBanKindUnstructured StockBanKind = "unstructured" //舆情/公告摘要较长，提高本地关注分（演示启发式）
)

// StockGateHit 名单或规则命中说明。
type StockGateHit struct {
	Kind   StockBanKind `json:"kind"` // absolute_ban | event_ban | watchlist | unstructured
	Code   string       `json:"code,omitempty"`
	Detail string       `json:"detail,omitempty"`
}

// StockLocalGate 嵌套子图「本地与规则闸门」输出（短路优先语义）。
type StockLocalGate struct {
	HardBlock            bool           `json:"hard_block"`
	BlockReason          string         `json:"block_reason,omitempty"`
	ForceAIReview        bool           `json:"force_ai_review"` // 限制清单命中：强制进入 AI 初筛
	Hits                 []StockGateHit `json:"hits,omitempty"`
	LocalRiskScore       float64        `json:"local_risk_score"`        // 0~1，非结构化启发式
	LocalNeedsDeepReview bool           `json:"local_needs_deep_review"` // 与初筛阈值叠加用
}

// StockPipelineState 股票图状态。
type StockPipelineState struct {
	TraceID string `json:"trace_id"`

	Order StockOrder            `json:"order"`
	Norm  *NormalizedStockOrder `json:"norm,omitempty"`
	Gate  *StockLocalGate       `json:"gate,omitempty"`

	Primary   *PrimaryAssessment   `json:"primary,omitempty"`
	Secondary *SecondaryAssessment `json:"secondary,omitempty"`

	ReportMarkdown string `json:"report_markdown,omitempty"`

	StepTimings map[string]time.Duration `json:"step_timings,omitempty"`
	Audit       *AuditBuffer             `json:"-"`
}

// 股票 LLM 任务（与制裁共用同一 Router 上的不同 Task 常量）。
const (
	TaskStockPrimary Task = "stock_primary"
	TaskStockVerify  Task = "stock_verify"
	TaskStockReport  Task = "stock_report"
)

func RecordStockStep(st *StockPipelineState, name string, t0 time.Time) {
	if st.StepTimings == nil {
		st.StepTimings = make(map[string]time.Duration)
	}
	st.StepTimings[name] = time.Since(t0)
}
