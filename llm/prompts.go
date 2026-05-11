package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"risk_control/domain"
)

func PrimaryMessages(st *domain.PipelineState) []*schema.Message {
	candJSON, _ := json.Marshal(st.Candidates)
	sys := `你是跨境支付合规助手，专注制裁名单（含 OFAC SDN、欧盟等）模糊匹配。
只输出 JSON，不要 Markdown，不要解释性前缀。JSON 字段：
{"risk_score":0-1浮点,"matched_names":字符串数组,"rationale":"简短中文","needs_secondary_review":布尔}
规则：多语言名称需考虑音译/拼写变体；无充分依据时 risk_score 应偏低且 needs_secondary_review=false。`
	user := fmt.Sprintf(`交易对手展示名: %s
国家/地区: %s
银行: %s
用途: %s
标准化键: %s
本地候选（SQL 粗筛）: %s`,
		st.Request.Counterparty,
		st.Request.Country,
		st.Request.BankName,
		st.Request.PaymentPurpose,
		st.Party.NormalizedKey,
		string(candJSON),
	)
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}

func VerifyMessages(st *domain.PipelineState) []*schema.Message {
	pj, _ := json.Marshal(st.Primary)
	sys := `你是合规复核模型，仅基于给定材料判断初筛是否成立。
只输出 JSON：{"confirmed":布尔,"final_risk_score":0-1,"rationale":"中文简短"}`
	user := fmt.Sprintf(`初筛结果: %s
原始请求: %+v
候选名单: %s`, string(pj), st.Request, mustJSON(st.Candidates))
	return []*schema.Message{
		schema.SystemMessage(sys),
		schema.UserMessage(user),
	}
}

func ReportMessages(st *domain.PipelineState) []*schema.Message {
	sys := `你是合规报告撰写助手，输出简短中文 Markdown（分级标题 + 要点列表），包含：结论、关键匹配、是否需人工复核、建议动作。`
	sec := "（未触发二次模型）"
	if st.Secondary != nil && !st.Secondary.Skipped {
		b, _ := json.Marshal(st.Secondary)
		sec = string(b)
	}
	pb, _ := json.Marshal(st.Primary)
	user := fmt.Sprintf(`交易ID: %s
对手方: %s
初筛: %s
二验: %s`, st.Request.TransactionID, st.Request.Counterparty, string(pb), sec)
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
