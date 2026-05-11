package store

import (
	"context"

	"risk_control/tools"
)

// Store 名单与审计持久化抽象，便于单测替换。
type Store interface {
	EnsureSchema(ctx context.Context) error
	SearchSanctions(ctx context.Context, party *tools.NormalizedParty, limit int) ([]tools.SanctionCandidate, error)
	InsertAuditStep(ctx context.Context, traceID, step string, detailJSON string, latencyMs int64) error
	InsertAIDecision(ctx context.Context, traceID, task, modelName, inputSummary, outputText string, latencyMs int64) error
	// FlushAudit 将流水线内累积的审计与 AI 决策行在同一事务中写入（仅 audit_log / ai_decision）。
	FlushAudit(ctx context.Context, traceID string, buf *tools.AuditBuffer) error
}
