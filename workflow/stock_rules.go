package workflow

import (
	"fmt"
	"strings"
	"time"

	"risk_control/config"
	"risk_control/tools"
)

// ApplyStockLocalRules 股票本地闸门（与 stock_subgraph 语义对齐，供编排器规则引擎使用）。
func ApplyStockLocalRules(st *tools.StockPipelineState, rules config.StockRules, vel *VelocityTracker) {
	if st.Gate == nil {
		st.Gate = &tools.StockLocalGate{}
	}
	if st.Norm == nil {
		return
	}
	sym := st.Norm.SymbolKey
	acct := st.Order.AccountID

	if setInList(rules.AbsoluteBanSymbols, sym) || strings.Contains(sym, "ST") {
		st.Gate.HardBlock = true
		st.Gate.BlockReason = "absolute_ban_list"
		st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindAbsolute, Code: sym, Detail: "标的在绝对禁止清单或 ST"})
		st.Gate.LocalRiskScore = 1
		return
	}
	if st.Order.Flags.BeforeEarnings {
		st.Gate.HardBlock = true
		st.Gate.BlockReason = "event_earnings_window"
		st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindEvent, Code: "EARNINGS", Detail: "财报窗口内禁止"})
		st.Gate.LocalRiskScore = 0.95
		return
	}
	if rules.MaxNotional > 0 {
		notional := st.Order.Price * float64(st.Order.Quantity)
		if notional > rules.MaxNotional {
			st.Gate.HardBlock = true
			st.Gate.BlockReason = "notional_limit"
			st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindAbsolute, Code: "NOTIONAL", Detail: fmt.Sprintf("名义本金超限 %.2f", notional)})
			st.Gate.LocalRiskScore = 0.9
			return
		}
	}
	if acct != "" && vel != nil {
		n := vel.Hit("stock_acct:"+strings.ToLower(acct), time.Duration(rules.VelocityWindowSec)*time.Second)
		if n > rules.VelocityMaxCount {
			st.Gate.HardBlock = true
			st.Gate.BlockReason = tools.PolicyVelocity
			st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindEvent, Code: "VELOCITY", Detail: fmt.Sprintf("下单频次 %d", n)})
			st.Gate.LocalRiskScore = 0.88
			return
		}
	}

	trusted := setInList(rules.TrustedAccounts, acct)
	if setInList(rules.WatchlistSymbols, sym) {
		st.Gate.ForceAIReview = true
		st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindWatchlist, Code: sym, Detail: "内部限制清单"})
		st.Gate.LocalRiskScore = 0.4
	}

	score := st.Gate.LocalRiskScore
	if trusted && !st.Gate.ForceAIReview {
		score = 0.05
	} else if score < 0.15 {
		score = 0.15
	}
	if len(st.Gate.Hits) > 0 {
		score += 0.1 * float64(len(st.Gate.Hits))
	}
	if len(st.Order.NewsSummary) > 80 {
		score += 0.25
		st.Gate.Hits = append(st.Gate.Hits, tools.StockGateHit{Kind: tools.StockBanKindUnstructured, Code: "NEWS_LEN", Detail: "舆情摘要较长"})
		st.Gate.LocalNeedsDeepReview = true
	}
	if st.Gate.ForceAIReview {
		score += 0.15
		st.Gate.LocalNeedsDeepReview = true
	}
	if score > 1 {
		score = 1
	}
	st.Gate.LocalRiskScore = score
}

// StockRuleDecision 将闸门映射为决策码。
func StockRuleDecision(st *tools.StockPipelineState, rules config.StockRules) (dec string, early bool, report string) {
	if st.Gate != nil && st.Gate.HardBlock {
		return tools.DecisionReject, true, fmt.Sprintf("## 本地裁决\n- **决策**: REJECT\n- **原因**: %s\n", st.Gate.BlockReason)
	}
	if st.Gate != nil && (st.Gate.ForceAIReview || st.Gate.LocalNeedsDeepReview) {
		return tools.DecisionReview, false, "## 本地裁决\n- 需深度/AI 复核\n"
	}
	if setInList(rules.TrustedAccounts, st.Order.AccountID) && st.Gate != nil && st.Gate.LocalRiskScore <= 0.2 {
		return tools.DecisionApprove, true, "## 本地裁决\n- **决策**: APPROVE\n- 可信账户快路径\n"
	}
	if st.Gate != nil && st.Gate.LocalRiskScore < 0.35 {
		return tools.DecisionApprove, true, "## 本地裁决\n- **决策**: APPROVE\n- 低风险本地放行\n"
	}
	return tools.DecisionReview, false, "## 本地裁决\n- 灰区\n"
}
