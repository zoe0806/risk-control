package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	"risk_control/domain"
)

// MySQL 使用 InnoDB 与显式事务（调用方可在外层 BeginTx）。
type MySQL struct {
	db *sql.DB
}

func OpenMySQL(dsn string) (*MySQL, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("empty MYSQL_DSN")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &MySQL{db: db}, nil
}

func (m *MySQL) Close() error { return m.db.Close() }

func (m *MySQL) DB() *sql.DB { return m.db }

func (m *MySQL) EnsureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sanctions_entry (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			list_code VARCHAR(32) NOT NULL,
			name_original VARCHAR(512) NOT NULL,
			name_normalized VARCHAR(512) NOT NULL,
			aliases_json JSON NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_name_norm (name_normalized),
			INDEX idx_list (list_code)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS screening_cache (
			cache_key VARCHAR(256) PRIMARY KEY,
			payload_json JSON NOT NULL,
			expires_at TIMESTAMP NOT NULL,
			INDEX idx_expires (expires_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			trace_id VARCHAR(64) NOT NULL,
			step_name VARCHAR(128) NOT NULL,
			detail_json JSON NULL,
			latency_ms BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_trace (trace_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS ai_decision (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			trace_id VARCHAR(64) NOT NULL,
			task_kind VARCHAR(64) NOT NULL,
			model_name VARCHAR(128) NOT NULL,
			input_summary TEXT NOT NULL,
			output_text MEDIUMTEXT NOT NULL,
			latency_ms BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_trace (trace_id),
			INDEX idx_task (task_kind)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	}
	for _, s := range stmts {
		if _, err := m.db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	return m.seedDemo(ctx)
}

func (m *MySQL) seedDemo(ctx context.Context) error {
	var n int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sanctions_entry`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	rows := []struct {
		list, orig, norm string
	}{
		{"SDN", "AL-SHABAAB", "AL_SHABAAB"},
		{"SDN", "КАЗАНТИП FINANCIAL", "КАЗАНТИП_FINANCIAL"},
		{"EU", "ROSNEFT OIL", "ROSNEFT_OIL"},
		{"SDN", "张三 制裁示例实体", "张三_制裁示例实体"},
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sanctions_entry (list_code, name_original, name_normalized, aliases_json) VALUES (?,?,?,NULL)`,
			r.list, r.orig, r.norm,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *MySQL) SearchSanctions(ctx context.Context, party *domain.NormalizedParty, limit int) ([]domain.SanctionCandidate, error) {
	if limit <= 0 {
		limit = 32
	}
	// 粗筛：token OR 前缀，演示用；生产可换全文检索 / ES / 向量召回。
	var clauses []string
	var args []any
	for _, t := range party.Tokens {
		if len(t) < 2 {
			continue
		}
		clauses = append(clauses, "(name_normalized LIKE ? OR name_original LIKE ?)")
		pat := "%" + t + "%"
		args = append(args, pat, pat)
	}
	if len(clauses) == 0 {
		clauses = append(clauses, "(name_normalized LIKE ? OR name_original LIKE ?)")
		pat := "%" + strings.ReplaceAll(party.NormalizedKey, "_", "%") + "%"
		args = append(args, pat, pat)
	}
	q := fmt.Sprintf(`SELECT id, list_code, name_original, name_normalized FROM sanctions_entry WHERE %s LIMIT %d`,
		strings.Join(clauses, " OR "), limit)

	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.SanctionCandidate
	for rows.Next() {
		var c domain.SanctionCandidate
		if err := rows.Scan(&c.ID, &c.ListCode, &c.NameOriginal, &c.NameNormalized); err != nil {
			return nil, err
		}
		c.MatchExplanation = "sql_token_prefilter"
		out = append(out, c)
	}
	return out, rows.Err()
}

func (m *MySQL) InsertAuditStep(ctx context.Context, traceID, step string, detailJSON string, latencyMs int64) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO audit_log (trace_id, step_name, detail_json, latency_ms) VALUES (?,?,?,?)`,
		traceID, step, detailJSON, latencyMs,
	)
	return err
}

func (m *MySQL) InsertAIDecision(ctx context.Context, traceID, task, modelName, inputSummary, outputText string, latencyMs int64) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO ai_decision (trace_id, task_kind, model_name, input_summary, output_text, latency_ms) VALUES (?,?,?,?,?,?)`,
		traceID, task, modelName, inputSummary, outputText, latencyMs,
	)
	return err
}

// LogJSON 辅助序列化。
func LogJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
