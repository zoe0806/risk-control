package store

import (
	"context"

	"risk_control/tools"
)

// Noop 无数据库时的占位实现（审计仅内存侧由调用方日志承接）。
type Noop struct{}

func (Noop) EnsureSchema(ctx context.Context) error { return nil }

func (Noop) SearchSanctions(ctx context.Context, party *tools.NormalizedParty, limit int) ([]tools.SanctionCandidate, error) {
	return nil, nil
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

var _ Store = Noop{}
