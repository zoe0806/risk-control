package store

import (
	"context"

	"risk_control/domain"
)

// Store 名单与审计持久化抽象，便于单测替换。
type Store interface {
	EnsureSchema(ctx context.Context) error
	SearchSanctions(ctx context.Context, party *domain.NormalizedParty, limit int) ([]domain.SanctionCandidate, error)
	InsertAuditStep(ctx context.Context, traceID, step string, detailJSON string, latencyMs int64) error
	InsertAIDecision(ctx context.Context, traceID, task, modelName, inputSummary, outputText string, latencyMs int64) error
}
