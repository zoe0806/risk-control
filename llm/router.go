package llm

import (
	"context"
	"fmt"

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"risk_control/config"
	"risk_control/tools"
)

// Router 将业务任务映射到具体 ChatModel，便于独立替换与成本控制。
type Router struct {
	primary model.BaseChatModel
	verify  model.BaseChatModel
	report  model.BaseChatModel

	primaryModelName string
	verifyModelName  string
	reportModelName  string
}

// 多模型分层/模型路由，根据配置创建模型实例，并通过Router.For方法根据任务类型选择对应模型
func NewRouter(ctx context.Context, cfg config.Config) (*Router, error) {
	if cfg.DeepSeekAPIKey == "" {
		return nil, fmt.Errorf("ai model api key is empty")
	}
	base := cfg.DeepSeekBaseURL
	timeout := cfg.LLMTimeout
	mk := func(name string) (model.BaseChatModel, error) {
		return openaiext.NewChatModel(ctx, &openaiext.ChatModelConfig{
			APIKey:  cfg.DeepSeekAPIKey,
			BaseURL: base,
			ByAzure: false,
			Model:   name,
			Timeout: timeout,
		})
	}
	p, err := mk(cfg.ModelPrimary)
	if err != nil {
		return nil, fmt.Errorf("primary model: %w", err)
	}
	v, err := mk(cfg.ModelVerify)
	if err != nil {
		return nil, fmt.Errorf("verify model: %w", err)
	}
	r, err := mk(cfg.ModelReport)
	if err != nil {
		return nil, fmt.Errorf("report model: %w", err)
	}
	return &Router{
		primary:          p,
		verify:           v,
		report:           r,
		primaryModelName: cfg.ModelPrimary,
		verifyModelName:  cfg.ModelVerify,
		reportModelName:  cfg.ModelReport,
	}, nil
}

// For 返回任务对应模型（强类型路由入口）。
func (rt *Router) For(t tools.Task) model.BaseChatModel {
	switch t {
	case tools.TaskSanctionsVerify:
		return rt.verify
	case tools.TaskReport:
		return rt.report
	default:
		return rt.primary
	}
}

func (rt *Router) ModelName(t tools.Task) string {
	switch t {
	case tools.TaskSanctionsVerify:
		return rt.verifyModelName
	case tools.TaskReport:
		return rt.reportModelName
	default:
		return rt.primaryModelName
	}
}
