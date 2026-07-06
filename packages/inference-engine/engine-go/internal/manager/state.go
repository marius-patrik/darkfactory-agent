package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"

	_ "modernc.org/sqlite"
)

// StateStore persists manager bookkeeping.
type StateStore struct {
	db *sql.DB
}

// NewStateStore opens (or creates) the SQLite database at path.
func NewStateStore(path string) (*StateStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &StateStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *StateStore) Close() error { return s.db.Close() }

func (s *StateStore) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS processed_issues (
	issue_number INTEGER PRIMARY KEY,
	label TEXT NOT NULL,
	title TEXT NOT NULL,
	processed_at TEXT NOT NULL,
	decomposition TEXT
);

CREATE TABLE IF NOT EXISTS manager_runs (
	run_id TEXT PRIMARY KEY,
	issue_number INTEGER NOT NULL,
	subtask_index INTEGER NOT NULL,
	subtask_title TEXT NOT NULL,
	branch TEXT,
	pr_url TEXT,
	log_issue_number INTEGER,
	last_status TEXT,
	result_ingested_at TEXT,
	review_status TEXT,
	review_ingested_at TEXT,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mruns_issue ON manager_runs(issue_number);

CREATE TABLE IF NOT EXISTS schema_migrations (
	name TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	for _, migration := range []stateMigration{
		{
			name: "manager_runs_result_review_columns",
			statements: []string{
				`ALTER TABLE manager_runs ADD COLUMN last_status TEXT`,
				`ALTER TABLE manager_runs ADD COLUMN result_ingested_at TEXT`,
				`ALTER TABLE manager_runs ADD COLUMN review_status TEXT`,
				`ALTER TABLE manager_runs ADD COLUMN review_ingested_at TEXT`,
			},
		},
		{
			name: "manager_runs_control_columns",
			statements: []string{
				`ALTER TABLE manager_runs ADD COLUMN task_id TEXT`,
				`ALTER TABLE manager_runs ADD COLUMN evidence_path TEXT`,
				`ALTER TABLE manager_runs ADD COLUMN cancellation_requested_at TEXT`,
				`ALTER TABLE manager_runs ADD COLUMN updated_at TEXT`,
				`UPDATE manager_runs SET updated_at = created_at WHERE updated_at IS NULL`,
			},
		},
	} {
		if err := s.applyMigration(migration); err != nil {
			return err
		}
	}
	return nil
}

type stateMigration struct {
	name       string
	statements []string
}

func (s *StateStore) applyMigration(m stateMigration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM schema_migrations WHERE name = ?`, m.name).Scan(&exists); err == nil {
		return tx.Commit()
	} else if err != sql.ErrNoRows {
		return err
	}
	for _, stmt := range m.statements {
		if _, err := tx.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(name, applied_at) VALUES (?, ?)`, m.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// IsProcessed checks whether an issue has already been handled.
func (s *StateStore) IsProcessed(ctx context.Context, issueNumber int) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM processed_issues WHERE issue_number = ?", issueNumber).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// MarkProcessed records that an issue has been handled.
func (s *StateStore) MarkProcessed(ctx context.Context, issueNumber int, label, title string, decomposition []SubTask) error {
	decomp, _ := json.Marshal(decomposition)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO processed_issues(issue_number, label, title, processed_at, decomposition)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(issue_number) DO UPDATE SET
			label=excluded.label,
			title=excluded.title,
			processed_at=excluded.processed_at,
			decomposition=excluded.decomposition
	`, issueNumber, label, title, time.Now().UTC().Format(time.RFC3339Nano), string(decomp))
	return err
}

// RecordRun stores metadata for a run created by the manager.
func (s *StateStore) RecordRun(ctx context.Context, runID string, issueNumber, subtaskIndex int, subtaskTitle, branch, prURL string, logIssueNumber int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO manager_runs(run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, issueNumber, subtaskIndex, subtaskTitle, branch, prURL, logIssueNumber, string(contracts.RunStatusQueued), now, now)
	return err
}

// ListRuns returns all manager runs ordered by newest update first.
func (s *StateStore) ListRuns(ctx context.Context) ([]ManagerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, result_ingested_at, review_status, review_ingested_at, created_at, task_id, evidence_path, cancellation_requested_at, updated_at
		FROM manager_runs ORDER BY COALESCE(updated_at, created_at) DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanManagerRuns(rows)
}

// GetRun returns one manager run by ID.
func (s *StateStore) GetRun(ctx context.Context, runID string) (ManagerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, result_ingested_at, review_status, review_ingested_at, created_at, task_id, evidence_path, cancellation_requested_at, updated_at
		FROM manager_runs WHERE run_id = ?`, runID)
	if err != nil {
		return ManagerRun{}, err
	}
	defer rows.Close()

	runs, err := scanManagerRuns(rows)
	if err != nil {
		return ManagerRun{}, err
	}
	if len(runs) == 0 {
		return ManagerRun{}, sql.ErrNoRows
	}
	return runs[0], nil
}

// ListRunsForIssue returns all manager runs tied to an issue.
func (s *StateStore) ListRunsForIssue(ctx context.Context, issueNumber int) ([]ManagerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, result_ingested_at, review_status, review_ingested_at, created_at, task_id, evidence_path, cancellation_requested_at, updated_at
		FROM manager_runs WHERE issue_number = ? ORDER BY subtask_index`, issueNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanManagerRuns(rows)
}

// ListRunsNeedingPR returns recorded runs that do not have a PR URL yet.
func (s *StateStore) ListRunsNeedingPR(ctx context.Context) ([]ManagerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, result_ingested_at, review_status, review_ingested_at, created_at, task_id, evidence_path, cancellation_requested_at, updated_at
		FROM manager_runs WHERE pr_url = '' OR pr_url IS NULL ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanManagerRuns(rows)
}

// ListRunsNeedingResult returns recorded runs whose terminal status has not been ingested.
func (s *StateStore) ListRunsNeedingResult(ctx context.Context) ([]ManagerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, result_ingested_at, review_status, review_ingested_at, created_at, task_id, evidence_path, cancellation_requested_at, updated_at
		FROM manager_runs
		WHERE result_ingested_at IS NULL
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanManagerRuns(rows)
}

// ListRunsNeedingReview returns terminal successful runs whose PR review verdict has not been ingested.
func (s *StateStore) ListRunsNeedingReview(ctx context.Context) ([]ManagerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, result_ingested_at, review_status, review_ingested_at, created_at, task_id, evidence_path, cancellation_requested_at, updated_at
		FROM manager_runs
		WHERE result_ingested_at IS NOT NULL
			AND last_status = ?
			AND pr_url IS NOT NULL
			AND pr_url != ''
			AND review_ingested_at IS NULL
		ORDER BY created_at`, string(contracts.RunStatusSucceeded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanManagerRuns(rows)
}

// SetRunPR records the draft PR URL for a run.
func (s *StateStore) SetRunPR(ctx context.Context, runID, prURL string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE manager_runs SET pr_url = ?, updated_at = ? WHERE run_id = ?`, prURL, time.Now().UTC().Format(time.RFC3339Nano), runID)
	return err
}

// SetRunTaskID records the external QFT task ID bound to a run.
func (s *StateStore) SetRunTaskID(ctx context.Context, runID, taskID string) error {
	return s.execRunUpdate(ctx, `UPDATE manager_runs SET task_id = ?, updated_at = ? WHERE run_id = ?`, taskID, time.Now().UTC().Format(time.RFC3339Nano), runID)
}

// SetRunEvidencePath records the terminal evidence path for a run.
func (s *StateStore) SetRunEvidencePath(ctx context.Context, runID, path string) error {
	return s.execRunUpdate(ctx, `UPDATE manager_runs SET evidence_path = ?, updated_at = ? WHERE run_id = ?`, path, time.Now().UTC().Format(time.RFC3339Nano), runID)
}

// SetCancellationRequested records cancellation intent for a run.
func (s *StateStore) SetCancellationRequested(ctx context.Context, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return s.execRunUpdate(ctx, `UPDATE manager_runs SET cancellation_requested_at = ?, updated_at = ? WHERE run_id = ?`, now, now, runID)
}

// TouchRun refreshes updated_at for a run without changing other fields.
func (s *StateStore) TouchRun(ctx context.Context, runID string) error {
	return s.execRunUpdate(ctx, `UPDATE manager_runs SET updated_at = ? WHERE run_id = ?`, time.Now().UTC().Format(time.RFC3339Nano), runID)
}

func (s *StateStore) execRunUpdate(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	if rows > 1 {
		return errors.New("manager run update affected multiple rows")
	}
	return nil
}

// SetRunStatus records the last daemon status seen for a run.
func (s *StateStore) SetRunStatus(ctx context.Context, runID string, status contracts.RunStatus, ingested bool) error {
	var ingestedAt any
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if ingested {
		ingestedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE manager_runs
		SET last_status = ?, result_ingested_at = COALESCE(?, result_ingested_at), updated_at = ?
		WHERE run_id = ?`, string(status), ingestedAt, now, runID)
	return err
}

// SetRunReview records the run PR review verdict after it was mirrored to parent evidence.
func (s *StateStore) SetRunReview(ctx context.Context, runID, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE manager_runs
		SET review_status = ?, review_ingested_at = ?, updated_at = ?
		WHERE run_id = ?`, status, now, now, runID)
	return err
}

func scanManagerRuns(rows *sql.Rows) ([]ManagerRun, error) {
	var out []ManagerRun
	for rows.Next() {
		var r ManagerRun
		var createdAt string
		var logIssue sql.NullInt64
		var lastStatus, resultIngestedAt, reviewStatus, reviewIngestedAt sql.NullString
		var taskID, evidencePath, cancellationRequestedAt, updatedAt sql.NullString
		if err := rows.Scan(&r.RunID, &r.IssueNumber, &r.SubtaskIndex, &r.SubtaskTitle, &r.Branch, &r.PRURL, &logIssue, &lastStatus, &resultIngestedAt, &reviewStatus, &reviewIngestedAt, &createdAt, &taskID, &evidencePath, &cancellationRequestedAt, &updatedAt); err != nil {
			return nil, err
		}
		if logIssue.Valid {
			r.LogIssueNumber = int(logIssue.Int64)
		}
		if lastStatus.Valid {
			r.LastStatus = contracts.RunStatus(lastStatus.String)
		}
		if resultIngestedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, resultIngestedAt.String)
			r.ResultIngestedAt = &t
		}
		if reviewStatus.Valid {
			r.ReviewStatus = reviewStatus.String
		}
		if reviewIngestedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, reviewIngestedAt.String)
			r.ReviewIngestedAt = &t
		}
		if taskID.Valid {
			r.TaskID = taskID.String
		}
		if evidencePath.Valid {
			r.EvidencePath = evidencePath.String
		}
		if cancellationRequestedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, cancellationRequestedAt.String)
			r.CancellationRequestedAt = &t
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if updatedAt.Valid {
			r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt.String)
		}
		if r.UpdatedAt.IsZero() {
			r.UpdatedAt = r.CreatedAt
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ManagerRun is a run created by the manager for an issue.
type ManagerRun struct {
	RunID                   string
	IssueNumber             int
	SubtaskIndex            int
	SubtaskTitle            string
	Branch                  string
	PRURL                   string
	LogIssueNumber          int
	LastStatus              contracts.RunStatus
	ResultIngestedAt        *time.Time
	ReviewStatus            string
	ReviewIngestedAt        *time.Time
	CreatedAt               time.Time
	TaskID                  string
	EvidencePath            string
	CancellationRequestedAt *time.Time
	UpdatedAt               time.Time
}

