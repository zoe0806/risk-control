package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"risk_control/config"
	"risk_control/tools"
)

func PrimaryMessages(st *tools.PipelineState, cfg config.Config) []*schema.Message {
	candJSON, _ := json.Marshal(st.Candidates)
	tx := st.Request.Transaction
	sys := cfg.SysPrompt
	user := fmt.Sprintf(cfg.UserPrompt, tx.Counterparty, tx.Country, tx.BankName, tx.PaymentPurpose, st.Party.NormalizedKey, string(candJSON))
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}

func VerifyMessages(st *tools.PipelineState, cfg config.Config) []*schema.Message {
	pj, _ := json.Marshal(st.Primary)
	sys := cfg.VerifyPrompt
	user := fmt.Sprintf(`初筛结果: %s
原始请求: %+v
候选名单: %s`, string(pj), st.Request, mustJSON(st.Candidates))
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}

func ReportMessages(st *tools.PipelineState, cfg config.Config) []*schema.Message {
	sys := cfg.ReportPrompt
	sec := "（未触发二次模型）"
	switch {
	case st.Secondary != nil && st.Secondary.TechnicalDegraded:
		sec = "【重要】因技术原因，未经 AI 二次验证；最终分数与结论以初筛为准，请人工复核。"
	case st.Secondary != nil && !st.Secondary.Skipped:
		b, _ := json.Marshal(st.Secondary)
		sec = string(b)
	case st.Secondary != nil && st.Secondary.Skipped && !st.Secondary.TechnicalDegraded:
		sec = "（规则路径：未达二验阈值，已跳过二次模型）"
	}
	pb, _ := json.Marshal(st.Primary)
	tx := st.Request.Transaction
	user := fmt.Sprintf(`交易ID: %s
对手方: %s
初筛: %s
二验: %s`, tx.TransactionID, tx.Counterparty, string(pb), sec)
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ExtractJSONObject 从模型输出中剥离 ```json 围栏。
func ExtractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		var b strings.Builder
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				break
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		s = strings.TrimSpace(b.String())
	}
	return s
}
