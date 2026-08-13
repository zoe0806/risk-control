package store

import (
	"context"
	"time"

	"risk_control/tools"
)

// Noop 无数据库时的占位实现（审计仅内存侧由调用方日志承接）。
type Noop struct{}

func (Noop) EnsureSchema(ctx context.Context) error { return nil }

func (Noop) SearchSanctions(ctx context.Context, party *tools.NormalizedParty, limit int) ([]tools.SanctionCandidate, error) {
	return nil, nil
}

func (Noop) ActiveListVersion(ctx context.Context) (string, error) { return "noop", nil }

func (Noop) GetScreeningCache(ctx context.Context, cacheKey string) (*tools.ScreeningResult, error) {
	return nil, nil
}

func (Noop) PutScreeningCache(ctx context.Context, cacheKey string, res *tools.ScreeningResult, ttl time.Duration) error {
	return nil
}

func (Noop) InsertAuditStep(ctx context.Context, traceID, step string, detailJSON string, latencyMs int64) error {
	return nil
}

func (Noop) InsertAIDecision(ctx context.Context, traceID, task, modelName, inputSummary, outputText string, latencyMs int64) error {
	return nil
}

func (Noop) FlushAudit(ctx context.Context, traceID string, buf *tools.AuditBuffer) error {
	return nil
}

func (Noop) CreateReviewCase(ctx context.Context, c *tools.ReviewCase) error { return nil }

func (Noop) GetReviewCase(ctx context.Context, caseID string) (*tools.ReviewCase, error) {
	return nil, nil
}

func (Noop) ListOpenReviewCases(ctx context.Context, limit int) ([]tools.ReviewCase, error) {
	return nil, nil
}

func (Noop) ResolveReviewCase(ctx context.Context, caseID, decision, resolver, note string) (*tools.ReviewCase, error) {
	return nil, nil
}

func (Noop) UpdateReviewCaseDraft(ctx context.Context, caseID, draftMarkdown string) error { return nil }

var _ Store = Noop{}
