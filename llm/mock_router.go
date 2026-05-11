package llm

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// 无 API Key 时的确定性输出，便于 CI / 本地演示编排而不打外部接口。
func newMockRouter() *Router {
	return &Router{
		primary:          &staticChatModel{reply: mockPrimaryJSON},
		verify:           &staticChatModel{reply: mockVerifyJSON},
		report:           &staticChatModel{reply: mockReportMD},
		primaryModelName: "mock-primary",
		verifyModelName:  "mock-verify",
		reportModelName:  "mock-report",
	}
}

const mockPrimaryJSON = `{
  "risk_score": 0.78,
  "matched_names": ["AL-SHABAAB"],
  "rationale": "名称与 SDN 条目在字符层面存在较高相似度，需二次核验。",
  "needs_secondary_review": true
}`

const mockVerifyJSON = `{
  "confirmed": true,
  "final_risk_score": 0.88,
  "rationale": "结合上下文与名单释义，倾向认定为同一实体或受控关联。"
}`

const mockReportMD = `## 制裁筛查审计摘要（演示）
- **结论**: 高风险预警
- **建议**: 暂停入账并提交合规复核。
`

type staticChatModel struct {
	reply string
}

func (s *staticChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(s.reply, nil), nil
}

func (s *staticChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := s.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	r, w := schema.Pipe[*schema.Message](1)
	_ = w.Send(msg, nil)
	w.Close()
	return r, nil
}
