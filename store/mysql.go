package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"risk_control/tools"
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
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)
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
			list_version VARCHAR(64) NOT NULL DEFAULT 'demo_v1',
			source VARCHAR(64) NOT NULL DEFAULT 'demo',
			effective_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			name_original VARCHAR(512) NOT NULL,
			name_normalized VARCHAR(512) NOT NULL,
			aliases_json JSON NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_name_norm (name_normalized),
			INDEX idx_list (list_code),
			INDEX idx_list_ver (list_version)
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
		`CREATE TABLE IF NOT EXISTS review_case (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			case_id VARCHAR(64) NOT NULL,
			trace_id VARCHAR(64) NOT NULL,
			transaction_id VARCHAR(128) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'OPEN',
			decision_code VARCHAR(32) NOT NULL,
			policy_ids_json JSON NULL,
			list_version VARCHAR(64) NULL,
			payload_json JSON NULL,
			draft_markdown MEDIUMTEXT NULL,
			resolve_note TEXT NULL,
			resolver VARCHAR(128) NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			resolved_at TIMESTAMP NULL,
			UNIQUE KEY uk_case (case_id),
			INDEX idx_status (status),
			INDEX idx_trace (trace_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	}
	for _, s := range stmts {
		if _, err := m.db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	if err := m.migrateSanctionsColumns(ctx); err != nil {
		return err
	}
	_, _ = m.db.ExecContext(ctx, `ALTER TABLE review_case ADD COLUMN draft_markdown MEDIUMTEXT NULL`)
	return m.seedDemo(ctx)
}

func (m *MySQL) migrateSanctionsColumns(ctx context.Context) error {
	alters := []string{
		`ALTER TABLE sanctions_entry ADD COLUMN list_version VARCHAR(64) NOT NULL DEFAULT 'demo_v1'`,
		`ALTER TABLE sanctions_entry ADD COLUMN source VARCHAR(64) NOT NULL DEFAULT 'demo'`,
		`ALTER TABLE sanctions_entry ADD COLUMN effective_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP`,
	}
	for _, a := range alters {
		if _, err := m.db.ExecContext(ctx, a); err != nil {
			// 列已存在则忽略
			if !strings.Contains(err.Error(), "Duplicate column") {
				// 某些 MySQL 版本文案不同，宽松忽略 1060
				if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
					// 仍可能是其它错误；仅当明确不是重复列时返回
					var num int
					if _, scanErr := fmt.Sscanf(err.Error(), "Error %d", &num); scanErr == nil && num == 1060 {
						continue
					}
					// 尝试继续：多数环境为已存在
					continue
				}
			}
		}
	}
	return nil
}

func (m *MySQL) seedDemo(ctx context.Context) error {
	var n int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sanctions_entry`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	type row struct {
		list, ver, source, orig, norm string
		aliases                       []string
	}
	rows := []row{
		{"SDN", "ofac_demo_2026q1", "OFAC", "AL-SHABAAB", "AL_SHABAAB", []string{"Al Shabaab", "Harakat Al-Shabaab"}},
		{"SDN", "ofac_demo_2026q1", "OFAC", "КАЗАНТИП FINANCIAL", "КАЗАНТИП_FINANCIAL", nil},
		{"EU", "eu_demo_2026q1", "EU", "ROSNEFT OIL", "ROSNEFT_OIL", []string{"Rosneft", "PJSC Rosneft Oil Company"}},
		{"SDN", "ofac_demo_2026q1", "OFAC", "张三 制裁示例实体", "张三_制裁示例实体", []string{"Zhang San Sanction Demo"}},
		{"UN", "un_demo_2026q1", "UN", "EVIL ENTITY LTD", "EVIL_ENTITY_LTD", []string{"Evil Entity Limited", "Evil Entity"}},
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range rows {
		var aliases any
		if len(r.aliases) > 0 {
			b, _ := json.Marshal(r.aliases)
			aliases = string(b)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sanctions_entry (list_code, list_version, source, name_original, name_normalized, aliases_json) VALUES (?,?,?,?,?,?)`,
			r.list, r.ver, r.source, r.orig, r.norm, aliases,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *MySQL) ActiveListVersion(ctx context.Context) (string, error) {
	var ver string
	err := m.db.QueryRowContext(ctx, `SELECT list_version FROM sanctions_entry ORDER BY effective_at DESC, id DESC LIMIT 1`).Scan(&ver)
	if err == sql.ErrNoRows {
		return "empty", nil
	}
	if err != nil {
		return "", err
	}
	return ver, nil
}

func (m *MySQL) SearchSanctions(ctx context.Context, party *tools.NormalizedParty, limit int) ([]tools.SanctionCandidate, error) {
	if limit <= 0 {
		limit = 32
	}
	if party == nil {
		return nil, nil
	}
	var clauses []string
	var args []any
	for _, t := range party.Tokens {
		if len(t) < 2 {
			continue
		}
		clauses = append(clauses, "(name_normalized LIKE ? OR name_original LIKE ? OR CAST(aliases_json AS CHAR) LIKE ?)")
		pat := "%" + t + "%"
		args = append(args, pat, pat, pat)
	}
	core := tools.StripCompanySuffix(party.NormalizedKey)
	if core != "" && core != party.NormalizedKey {
		clauses = append(clauses, "(name_normalized LIKE ? OR name_original LIKE ?)")
		pat := "%" + strings.ReplaceAll(core, "_", "%") + "%"
		args = append(args, pat, pat)
	}
	if len(clauses) == 0 {
		clauses = append(clauses, "(name_normalized LIKE ? OR name_original LIKE ?)")
		pat := "%" + strings.ReplaceAll(party.NormalizedKey, "_", "%") + "%"
		args = append(args, pat, pat)
	}
	// 多取一些供本地模糊精排
	fetch := limit * 3
	if fetch < 48 {
		fetch = 48
	}
	q := fmt.Sprintf(`SELECT id, list_code, COALESCE(list_version,''), name_original, name_normalized, aliases_json
		FROM sanctions_entry WHERE %s LIMIT %d`, strings.Join(clauses, " OR "), fetch)

	rows, err := m.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []tools.SanctionCandidate
	for rows.Next() {
		var c tools.SanctionCandidate
		var aliases sql.NullString
		if err := rows.Scan(&c.ID, &c.ListCode, &c.ListVersion, &c.NameOriginal, &c.NameNormalized, &aliases); err != nil {
			return nil, err
		}
		if aliases.Valid && aliases.String != "" {
			_ = json.Unmarshal([]byte(aliases.String), &c.Aliases)
		}
		c.MatchExplanation = "sql_token_prefilter"
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tools.RankCandidates(party, out, 0, limit), nil
}

func (m *MySQL) GetScreeningCache(ctx context.Context, cacheKey string) (*tools.ScreeningResult, error) {
	if cacheKey == "" {
		return nil, nil
	}
	var payload string
	var exp time.Time
	err := m.db.QueryRowContext(ctx,
		`SELECT payload_json, expires_at FROM screening_cache WHERE cache_key=?`, cacheKey,
	).Scan(&payload, &exp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(exp) {
		_, _ = m.db.ExecContext(ctx, `DELETE FROM screening_cache WHERE cache_key=?`, cacheKey)
		return nil, nil
	}
	var res tools.ScreeningResult
	if err := json.Unmarshal([]byte(payload), &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (m *MySQL) PutScreeningCache(ctx context.Context, cacheKey string, res *tools.ScreeningResult, ttl time.Duration) error {
	if cacheKey == "" || res == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	exp := time.Now().Add(ttl)
	_, err = m.db.ExecContext(ctx,
		`INSERT INTO screening_cache (cache_key, payload_json, expires_at) VALUES (?,?,?)
		 ON DUPLICATE KEY UPDATE payload_json=VALUES(payload_json), expires_at=VALUES(expires_at)`,
		cacheKey, string(b), exp,
	)
	return err
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

func (m *MySQL) FlushAudit(ctx context.Context, traceID string, buf *tools.AuditBuffer) error {
	if buf == nil {
		return nil
	}
	if len(buf.Steps) == 0 && len(buf.Decisions) == 0 {
		return nil
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, s := range buf.Steps {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO audit_log (trace_id, step_name, detail_json, latency_ms) VALUES (?,?,?,?)`,
			traceID, s.StepName, s.DetailJSON, s.LatencyMs,
		); err != nil {
			return err
		}
	}
	for _, d := range buf.Decisions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ai_decision (trace_id, task_kind, model_name, input_summary, output_text, latency_ms) VALUES (?,?,?,?,?,?)`,
			traceID, d.TaskKind, d.ModelName, d.InputSummary, d.OutputText, d.LatencyMs,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *MySQL) CreateReviewCase(ctx context.Context, c *tools.ReviewCase) error {
	if c == nil || c.CaseID == "" {
		return fmt.Errorf("empty review case")
	}
	pol, _ := json.Marshal(c.PolicyIDs)
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO review_case (case_id, trace_id, transaction_id, status, decision_code, policy_ids_json, list_version, payload_json)
		 VALUES (?,?,?,?,?,?,?,?)`,
		c.CaseID, c.TraceID, c.TransactionID, coalesce(c.Status, "OPEN"), coalesce(c.DecisionCode, tools.DecisionReview),
		string(pol), c.ListVersion, c.PayloadJSON,
	)
	return err
}

func (m *MySQL) GetReviewCase(ctx context.Context, caseID string) (*tools.ReviewCase, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT case_id, trace_id, transaction_id, status, decision_code, policy_ids_json, COALESCE(list_version,''),
		 COALESCE(payload_json,''), COALESCE(draft_markdown,''), COALESCE(resolve_note,''), COALESCE(resolver,''),
		 created_at, resolved_at FROM review_case WHERE case_id=?`, caseID)
	return scanReviewCase(row)
}

func (m *MySQL) ListOpenReviewCases(ctx context.Context, limit int) ([]tools.ReviewCase, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT case_id, trace_id, transaction_id, status, decision_code, policy_ids_json, COALESCE(list_version,''),
		 COALESCE(payload_json,''), COALESCE(draft_markdown,''), COALESCE(resolve_note,''), COALESCE(resolver,''),
		 created_at, resolved_at FROM review_case WHERE status='OPEN' ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tools.ReviewCase
	for rows.Next() {
		c, err := scanReviewCaseRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (m *MySQL) ResolveReviewCase(ctx context.Context, caseID, decision, resolver, note string) (*tools.ReviewCase, error) {
	decision = strings.ToUpper(strings.TrimSpace(decision))
	if decision != tools.DecisionApprove && decision != tools.DecisionReject {
		return nil, fmt.Errorf("decision must be APPROVE or REJECT")
	}
	status := "APPROVED"
	if decision == tools.DecisionReject {
		status = "REJECTED"
	}
	res, err := m.db.ExecContext(ctx,
		`UPDATE review_case SET status=?, decision_code=?, resolver=?, resolve_note=?, resolved_at=CURRENT_TIMESTAMP
		 WHERE case_id=? AND status='OPEN'`,
		status, decision, resolver, note, caseID,
	)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("case not found or already resolved: %s", caseID)
	}
	return m.GetReviewCase(ctx, caseID)
}

func (m *MySQL) UpdateReviewCaseDraft(ctx context.Context, caseID, draftMarkdown string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE review_case SET draft_markdown=? WHERE case_id=?`, draftMarkdown, caseID)
	return err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanReviewCase(row scannable) (*tools.ReviewCase, error) {
	var c tools.ReviewCase
	var pol sql.NullString
	var created time.Time
	var resolved sql.NullTime
	if err := row.Scan(&c.CaseID, &c.TraceID, &c.TransactionID, &c.Status, &c.DecisionCode, &pol,
		&c.ListVersion, &c.PayloadJSON, &c.DraftMarkdown, &c.ResolveNote, &c.Resolver, &created, &resolved); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if pol.Valid && pol.String != "" {
		_ = json.Unmarshal([]byte(pol.String), &c.PolicyIDs)
	}
	c.CreatedAt = created.Format(time.RFC3339)
	if resolved.Valid {
		c.ResolvedAt = resolved.Time.Format(time.RFC3339)
	}
	return &c, nil
}

func scanReviewCaseRows(rows *sql.Rows) (*tools.ReviewCase, error) {
	return scanReviewCase(rows)
}

func coalesce(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// LogJSON 辅助序列化。
func LogJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
