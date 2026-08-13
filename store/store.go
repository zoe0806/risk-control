package store

import (
	"context"
	"time"

	"risk_control/tools"
)

// Store 名单与审计持久化抽象，便于单测替换。
type Store interface {
	EnsureSchema(ctx context.Context) error
	SearchSanctions(ctx context.Context, party *tools.NormalizedParty, limit int) ([]tools.SanctionCandidate, error)
	ActiveListVersion(ctx context.Context) (string, error)

	GetScreeningCache(ctx context.Context, cacheKey string) (*tools.ScreeningResult, error)
	PutScreeningCache(ctx context.Context, cacheKey string, res *tools.ScreeningResult, ttl time.Duration) error

	InsertAuditStep(ctx context.Context, traceID, step string, detailJSON string, latencyMs int64) error
	InsertAIDecision(ctx context.Context, traceID, task, modelName, inputSummary, outputText string, latencyMs int64) error
	// FlushAudit 将流水线内累积的审计与 AI 决策行在同一事务中写入（仅 audit_log / ai_decision）。
	FlushAudit(ctx context.Context, traceID string, buf *tools.AuditBuffer) error

	CreateReviewCase(ctx context.Context, c *tools.ReviewCase) error
	GetReviewCase(ctx context.Context, caseID string) (*tools.ReviewCase, error)
	ListOpenReviewCases(ctx context.Context, limit int) ([]tools.ReviewCase, error)
	ResolveReviewCase(ctx context.Context, caseID, decision, resolver, note string) (*tools.ReviewCase, error)
	UpdateReviewCaseDraft(ctx context.Context, caseID, draftMarkdown string) error
}
