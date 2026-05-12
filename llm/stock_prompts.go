package llm

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"risk_control/config"
	"risk_control/tools"
)

// StockPrimaryMessages AI 初筛：自然语言纪律 + 订单 + 闸门摘要。
func StockPrimaryMessages(st *tools.StockPipelineState, cfg config.Config) []*schema.Message {
	gj, _ := json.Marshal(st.Gate)
	nj, _ := json.Marshal(st.Norm)
	sys := cfg.StockSysPrompt
	user := fmt.Sprintf(cfg.StockUserPrompt, st.Order, string(nj), string(gj), st.Order.DisciplineRules)
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}

// StockVerifyMessages 二验：高风险辩论式复核。
func StockVerifyMessages(st *tools.StockPipelineState, cfg config.Config) []*schema.Message {
	pj, _ := json.Marshal(st.Primary)
	gj, _ := json.Marshal(st.Gate)
	sys := cfg.StockVerifyPrompt
	user := fmt.Sprintf(`初筛: %s
闸门: %s
订单: %+v`, string(pj), string(gj), st.Order)
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}

// StockReportMessages 报告。
func StockReportMessages(st *tools.StockPipelineState, cfg config.Config) []*schema.Message {
	sys := cfg.StockReportPrompt
	sec := "（无二验）"
	if st.Secondary != nil && st.Secondary.TechnicalDegraded {
		sec = "【技术降级】未经 AI 二验。"
	} else if st.Secondary != nil && !st.Secondary.Skipped {
		b, _ := json.Marshal(st.Secondary)
		sec = string(b)
	} else if st.Secondary != nil && st.Secondary.Skipped {
		sec = "（规则跳过二验）"
	}
	pb, _ := json.Marshal(st.Primary)
	gb, _ := json.Marshal(st.Gate)
	user := fmt.Sprintf(`订单ID: %s
标的: %s
闸门: %s
初筛: %s
二验: %s`, st.Order.OrderID, st.Order.Symbol, string(gb), string(pb), sec)
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}
