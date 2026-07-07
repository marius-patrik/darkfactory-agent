// Package store provides SQLite-backed persistence for runs and operations.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// Store persists runs and operation records.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database at path and migrates schemas.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS runs (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	image TEXT NOT NULL,
	command TEXT,
	env TEXT,
	labels TEXT,
	container_id TEXT,
	exit_code INTEGER,
	logs TEXT,
	created_at TEXT NOT NULL,
	started_at TEXT,
	finished_at TEXT,
	issue_ref TEXT,
	branch_ref TEXT,
	pr_ref TEXT,
	head_sha TEXT,
	task_id TEXT,
	evidence_path TEXT,
	external_url TEXT,
	error TEXT
);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);
CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at);

CREATE TABLE IF NOT EXISTS operations (
	idempotency_key TEXT PRIMARY KEY,
	operation_id TEXT NOT NULL,
	actor TEXT,
	source TEXT,
	target TEXT,
	intent TEXT,
	payload_hash TEXT,
	correlation_id TEXT,
	trace_id TEXT,
	created_at TEXT NOT NULL,
	retry TEXT,
	result TEXT
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	for _, stmt := range []string{
		`ALTER TABLE runs ADD COLUMN task_id TEXT`,
		`ALTER TABLE runs ADD COLUMN evidence_path TEXT`,
		`ALTER TABLE runs ADD COLUMN head_sha TEXT`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !isDuplicateColumn(err) {
			return err
		}
	}
	return nil
}

// SaveRun inserts or updates a run.
func (s *Store) SaveRun(ctx context.Context, r *contracts.Run) error {
	cmd, _ := json.Marshal(r.Command)
	env, _ := json.Marshal(r.Env)
	labels, _ := json.Marshal(r.Labels)
	started := sqlNullTime(r.StartedAt)
	finished := sqlNullTime(r.FinishedAt)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs(id, status, image, command, env, labels, container_id, exit_code, logs, created_at, started_at, finished_at, issue_ref, branch_ref, pr_ref, head_sha, task_id, evidence_path, external_url, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			image=excluded.image,
			env=excluded.env,
			container_id=excluded.container_id,
			exit_code=excluded.exit_code,
			logs=excluded.logs,
			external_url=excluded.external_url,
			head_sha=excluded.head_sha,
			task_id=excluded.task_id,
			evidence_path=excluded.evidence_path,
			started_at=excluded.started_at,
			finished_at=excluded.finished_at,
			error=excluded.error
	`, r.ID, string(r.Status), r.Image, string(cmd), string(env), string(labels), r.ContainerID, r.ExitCode, r.Logs, r.CreatedAt.Format(time.RFC3339Nano), started, finished, r.IssueRef, r.BranchRef, r.PRRef, r.HeadSHA, r.TaskID, r.EvidencePath, r.ExternalURL, r.Error)
	return err
}

// GetRun retrieves a run by ID.
func (s *Store) GetRun(ctx context.Context, id string) (*contracts.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, status, image, command, env, labels, container_id, exit_code, logs, created_at, started_at, finished_at, issue_ref, branch_ref, pr_ref, head_sha, task_id, evidence_path, external_url, error
		FROM runs WHERE id = ?`, id)
	return scanRun(row)
}

// ListRuns returns runs ordered by created_at desc.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]contracts.Run, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, status, image, command, env, labels, container_id, exit_code, logs, created_at, started_at, finished_at, issue_ref, branch_ref, pr_ref, head_sha, task_id, evidence_path, external_url, error FROM runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contracts.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListRunsByStatus returns runs with any of the supplied statuses ordered by creation time.
func (s *Store) ListRunsByStatus(ctx context.Context, statuses ...contracts.RunStatus) ([]contracts.Run, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(statuses))
	args := make([]any, 0, len(statuses))
	for _, status := range statuses {
		placeholders = append(placeholders, "?")
		args = append(args, string(status))
	}
	query := fmt.Sprintf(
		`SELECT id, status, image, command, env, labels, container_id, exit_code, logs, created_at, started_at, finished_at, issue_ref, branch_ref, pr_ref, head_sha, task_id, evidence_path, external_url, error FROM runs WHERE status IN (%s) ORDER BY created_at ASC`,
		strings.Join(placeholders, ","),
	)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contracts.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountRunsByStatus returns counts per status.
func (s *Store) CountRunsByStatus(ctx context.Context) (map[contracts.RunStatus]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM runs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[contracts.RunStatus]int)
	for rows.Next() {
		var st string
		var c int
		if err := rows.Scan(&st, &c); err != nil {
			return nil, err
		}
		out[contracts.RunStatus(st)] = c
	}
	return out, rows.Err()
}

// AppendLogs appends to a run's log buffer.
func (s *Store) AppendLogs(ctx context.Context, runID string, chunk string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET logs = COALESCE(logs,'') || ? WHERE id = ?`, chunk, runID)
	return err
}

// RecordOperation persists an OperationEnvelope and optional result.
func (s *Store) RecordOperation(ctx context.Context, op contracts.OperationEnvelope, result string) error {
	actor, _ := json.Marshal(op.Actor)
	src, _ := json.Marshal(op.Source)
	tgt, _ := json.Marshal(op.Target)
	retry, _ := json.Marshal(op.Retry)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO operations(idempotency_key, operation_id, actor, source, target, intent, payload_hash, correlation_id, trace_id, created_at, retry, result)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO NOTHING
	`, op.IdempotencyKey, op.OperationID, string(actor), string(src), string(tgt), op.Intent, op.PayloadHash, op.CorrelationID, op.TraceID, op.CreatedAt.Format(time.RFC3339Nano), string(retry), result)
	return err
}

// HasOperation checks idempotency.
func (s *Store) HasOperation(ctx context.Context, key string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM operations WHERE idempotency_key = ?`, key).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// GetOperationResult returns the stored result for an idempotency key.
func (s *Store) GetOperationResult(ctx context.Context, key string) (string, bool, error) {
	var result sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT result FROM operations WHERE idempotency_key = ?`, key).Scan(&result)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return result.String, true, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(row scanner) (*contracts.Run, error) {
	var r contracts.Run
	var cmd, env, labels string
	var createdAtStr string
	var started, finished sql.NullString
	var st string
	err := row.Scan(&r.ID, &st, &r.Image, &cmd, &env, &labels, &r.ContainerID, &r.ExitCode, &r.Logs, &createdAtStr, &started, &finished, &r.IssueRef, &r.BranchRef, &r.PRRef, &r.HeadSHA, &r.TaskID, &r.EvidencePath, &r.ExternalURL, &r.Error)
	if err != nil {
		return nil, err
	}
	r.Status = contracts.RunStatus(st)
	_ = json.Unmarshal([]byte(cmd), &r.Command)
	_ = json.Unmarshal([]byte(env), &r.Env)
	_ = json.Unmarshal([]byte(labels), &r.Labels)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	if started.Valid {
		t, _ := time.Parse(time.RFC3339Nano, started.String)
		r.StartedAt = &t
	}
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finished.String)
		r.FinishedAt = &t
	}
	return &r, nil
}

func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column")
}

func sqlNullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(time.RFC3339Nano), Valid: true}
}

