package workflow

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"

	"risk_control/store"
	"risk_control/tools"
)

// 嵌套子图节点：本地闸门内部顺序（短路在节点内通过 HardBlock 跳过加重逻辑）。
const (
	subAbsolute     = "stock_sub_absolute_ban"
	subEvent        = "stock_sub_event_ban"
	subWatchlist    = "stock_sub_watchlist"
	subUnstructured = "stock_sub_unstructured"
)

// demoAbsoluteBanSymbols 演示绝对禁止（可后续换 MySQL）。
var demoAbsoluteBanSymbols = map[string]struct{}{
	"300136": {},
}

// demoWatchlistRestriction 演示「限制清单」→ 强制 AI 复核。
var demoWatchlistRestriction = map[string]struct{}{
	"300346": {},
}

// BuildStockLocalGateGraph 本地与规则闸门子图：使用本地数据进行风控
func BuildStockLocalGateGraph(_ context.Context) (*compose.Graph[*tools.StockPipelineState, *tools.StockPipelineState], error) {
	sg := compose.NewGraph[*tools.StockPipelineState, *tools.StockPipelineState]()

	if err := sg.AddLambdaNode(subAbsolute, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		if st.Gate == nil {
			st.Gate = &tools.StockLocalGate{}
		}
		if st.Norm == nil {
			tools.RecordStockStep(st, subAbsolute, t0)
			return st, nil
		}
		sym := st.Norm.SymbolKey
		if _, banned := demoAbsoluteBanSymbols[sym]; banned {
			st.Gate.HardBlock = true
			st.Gate.BlockReason = "absolute_ban_list"
			st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindAbsolute, Code: sym, Detail: "标的在绝对禁止清单"})
		}
		if !st.Gate.HardBlock && strings.Contains(sym, "ST") {
			st.Gate.HardBlock = true
			st.Gate.BlockReason = "st_like_symbol"
			st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindAbsolute, Code: "ST_PATTERN", Detail: "代码含 ST 片段，硬拦截"})
		}
		st.Audit.AddStep(subAbsolute, store.LogJSON(st.Gate), time.Since(t0).Milliseconds())
		tools.RecordStockStep(st, subAbsolute, t0)
		return st, nil
	}), compose.WithNodeName(subAbsolute)); err != nil {
		return nil, err
	}

	if err := sg.AddLambdaNode(subEvent, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		if st.Gate == nil {
			st.Gate = &tools.StockLocalGate{}
		}
		if st.Gate.HardBlock {
			tools.RecordStockStep(st, subEvent, t0)
			return st, nil
		}
		if st.Order.Flags.BeforeEarnings {
			st.Gate.HardBlock = true
			st.Gate.BlockReason = "event_earnings_window"
			st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindEvent, Code: "EARNINGS", Detail: "财报窗口内禁止"})
		}
		st.Audit.AddStep(subEvent, store.LogJSON(map[string]any{"hard_block": st.Gate.HardBlock}), time.Since(t0).Milliseconds())
		tools.RecordStockStep(st, subEvent, t0)
		return st, nil
	}), compose.WithNodeName(subEvent)); err != nil {
		return nil, err
	}

	if err := sg.AddLambdaNode(subWatchlist, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		if st.Gate == nil {
			st.Gate = &tools.StockLocalGate{}
		}
		if st.Gate.HardBlock || st.Norm == nil {
			tools.RecordStockStep(st, subWatchlist, t0)
			return st, nil
		}
		sym := st.Norm.SymbolKey
		if _, rest := demoWatchlistRestriction[sym]; rest {
			st.Gate.ForceAIReview = true
			st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindWatchlist, Code: sym, Detail: "内部限制清单命中，强制进入 AI 初筛"})
		}
		st.Audit.AddStep(subWatchlist, store.LogJSON(map[string]any{"force_ai": st.Gate.ForceAIReview}), time.Since(t0).Milliseconds())
		tools.RecordStockStep(st, subWatchlist, t0)
		return st, nil
	}), compose.WithNodeName(subWatchlist)); err != nil {
		return nil, err
	}

	if err := sg.AddLambdaNode(subUnstructured, compose.InvokableLambda(func(ctx context.Context, st *tools.StockPipelineState) (*tools.StockPipelineState, error) {
		t0 := time.Now()
		if st.Gate == nil {
			st.Gate = &tools.StockLocalGate{}
		}
		if st.Gate.HardBlock {
			tools.RecordStockStep(st, subUnstructured, t0)
			return st, nil
		}
		score := 0.15
		if len(st.Gate.Hits) > 0 {
			score += 0.1 * float64(len(st.Gate.Hits))
		}
		if len(st.Order.NewsSummary) > 80 {
			score += 0.25
			st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindUnstructured, Code: "NEWS_LEN", Detail: "舆情/公告摘要较长，提高本地关注分"})
		}
		if score > 0.55 {
			st.Gate.LocalNeedsDeepReview = true
		}
		if st.Gate.ForceAIReview {
			score += 0.15
		}
		if score > 1 {
			score = 1
		}
		st.Gate.LocalRiskScore = score
		st.Audit.AddStep(subUnstructured, store.LogJSON(map[string]any{"local_risk": score, "need_deep": st.Gate.LocalNeedsDeepReview}), time.Since(t0).Milliseconds())
		tools.RecordStockStep(st, subUnstructured, t0)
		return st, nil
	}), compose.WithNodeName(subUnstructured)); err != nil {
		return nil, err
	}

	for _, step := range []struct {
		fn func() error
	}{
		{func() error { return sg.AddEdge(compose.START, subAbsolute) }},
		{func() error { return sg.AddEdge(subAbsolute, subEvent) }},
		{func() error { return sg.AddEdge(subEvent, subWatchlist) }},
		{func() error { return sg.AddEdge(subWatchlist, subUnstructured) }},
		{func() error { return sg.AddEdge(subUnstructured, compose.END) }},
	} {
		if err := step.fn(); err != nil {
			return nil, err
		}
	}

	return sg, nil
}
