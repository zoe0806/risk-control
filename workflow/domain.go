package workflow

import "context"

// DomainProfile 业务域适配器：领域逻辑在此，编排骨架与业务无关。
// 深度 LLM 不走本接口，由 DeepRuntime 消费内核已算完的 DeepInput。
type DomainProfile interface {
	ID() string
	PreAnalyze(pack *PolicyPack, req any) (PreAnalysis, error)
	RunRules(ctx context.Context, e *RiskEngine, pack *PolicyPack, req any, pre PreAnalysis) EngineResult
	RunGraph(e *RiskEngine, pack *PolicyPack, req any) EngineResult
	RunLight(pack *PolicyPack, pre PreAnalysis, graphScore float64) EngineResult
	NeedDeep(pre PreAnalysis, rule EngineResult, arbDecision string) bool
	ShadowLocal(ctx context.Context, e *RiskEngine, shadow *PolicyPack, req any) (decision, detail string)
	SubjectLabel(req any) string
	TransactionID(req any) string
}
