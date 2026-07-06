package manager

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"

	_ "modernc.org/sqlite"
)

func TestStateStoreMigratesOldRunSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager-state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	createdAt := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.Exec(`
CREATE TABLE manager_runs (
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
INSERT INTO manager_runs(run_id, issue_number, subtask_index, subtask_title, branch, pr_url, log_issue_number, last_status, created_at)
VALUES ('run-old', 42, 1, 'Old task', 'run/old', '', NULL, 'failed', ?);
`, createdAt); err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close old db: %v", err)
	}

	st, err := NewStateStore(path)
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}

	run, err := st.GetRun(context.Background(), "run-old")
	if err != nil {
		t.Fatalf("get migrated run: %v", err)
	}
	if run.TaskID != "" || run.EvidencePath != "" || run.CancellationRequestedAt != nil {
		t.Fatalf("old row should allow missing new fields: %#v", run)
	}
	if run.LastStatus != contracts.RunStatusFailed {
		t.Fatalf("expected old failed status, got %s", run.LastStatus)
	}
	if run.UpdatedAt.IsZero() || !run.UpdatedAt.Equal(run.CreatedAt) {
		t.Fatalf("expected updated_at fallback to created_at, got created=%s updated=%s", run.CreatedAt, run.UpdatedAt)
	}
	if got := migrationNames(t, st); len(got) != 2 || got["manager_runs_result_review_columns"] == "" || got["manager_runs_control_columns"] == "" {
		t.Fatalf("expected recorded schema migrations, got %#v", got)
	}
	var storedUpdatedAt string
	if err := st.db.QueryRow(`SELECT updated_at FROM manager_runs WHERE run_id = 'run-old'`).Scan(&storedUpdatedAt); err != nil {
		t.Fatalf("query backfilled updated_at: %v", err)
	}
	if storedUpdatedAt != createdAt {
		t.Fatalf("expected physical updated_at backfill %q, got %q", createdAt, storedUpdatedAt)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}

	reopened, err := NewStateStore(path)
	if err != nil {
		t.Fatalf("reopen migrated state store: %v", err)
	}
	defer reopened.Close()
	reopenedRun, err := reopened.GetRun(context.Background(), "run-old")
	if err != nil {
		t.Fatalf("get reopened migrated run: %v", err)
	}
	if reopenedRun.UpdatedAt.IsZero() || !reopenedRun.UpdatedAt.Equal(reopenedRun.CreatedAt) {
		t.Fatalf("repeated migration should preserve backfilled updated_at, got created=%s updated=%s", reopenedRun.CreatedAt, reopenedRun.UpdatedAt)
	}
	if got := migrationNames(t, reopened); len(got) != 2 {
		t.Fatalf("repeated migration should not duplicate migration rows, got %#v", got)
	}
}

func TestStateStoreRunControlFieldsPersist(t *testing.T) {
	st, err := NewStateStore(filepath.Join(t.TempDir(), "manager-state.db"))
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.RecordRun(ctx, "run-new", 7, 0, "New task", "run/new", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}
	created, err := st.GetRun(ctx, "run-new")
	if err != nil {
		t.Fatalf("get created run: %v", err)
	}
	if created.UpdatedAt.IsZero() || created.CreatedAt.IsZero() {
		t.Fatalf("expected created and updated timestamps: %#v", created)
	}

	if err := st.SetRunTaskID(ctx, "run-new", "qft-task-20260529T120000Z-abcd"); err != nil {
		t.Fatalf("set task id: %v", err)
	}
	if err := st.SetRunEvidencePath(ctx, "run-new", "/tmp/evidence/run-new.json"); err != nil {
		t.Fatalf("set evidence path: %v", err)
	}
	if err := st.SetCancellationRequested(ctx, "run-new"); err != nil {
		t.Fatalf("set cancellation requested: %v", err)
	}
	if err := st.TouchRun(ctx, "run-new"); err != nil {
		t.Fatalf("touch run: %v", err)
	}

	run, err := st.GetRun(ctx, "run-new")
	if err != nil {
		t.Fatalf("get updated run: %v", err)
	}
	if run.TaskID != "qft-task-20260529T120000Z-abcd" {
		t.Fatalf("task id did not persist: %#v", run)
	}
	if run.EvidencePath != "/tmp/evidence/run-new.json" {
		t.Fatalf("evidence path did not persist: %#v", run)
	}
	if run.CancellationRequestedAt == nil {
		t.Fatalf("cancellation request timestamp missing: %#v", run)
	}
	if run.UpdatedAt.Before(run.CreatedAt) {
		t.Fatalf("updated_at should not precede created_at: created=%s updated=%s", run.CreatedAt, run.UpdatedAt)
	}
}

func TestStateStoreListsRunsNewestUpdateFirst(t *testing.T) {
	st, err := NewStateStore(filepath.Join(t.TempDir(), "manager-state.db"))
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.RecordRun(ctx, "run-a", 1, 0, "A", "run/a", "", 0); err != nil {
		t.Fatalf("record run a: %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := st.RecordRun(ctx, "run-b", 1, 1, "B", "run/b", "", 0); err != nil {
		t.Fatalf("record run b: %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := st.TouchRun(ctx, "run-a"); err != nil {
		t.Fatalf("touch run a: %v", err)
	}

	runs, err := st.ListRuns(ctx)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].RunID != "run-a" {
		t.Fatalf("expected newest updated run first, got %#v", runs)
	}
}

func migrationNames(t *testing.T, st *StateStore) map[string]string {
	t.Helper()
	rows, err := st.db.Query(`SELECT name, applied_at FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var name, appliedAt string
		if err := rows.Scan(&name, &appliedAt); err != nil {
			t.Fatalf("scan migration: %v", err)
		}
		out[name] = appliedAt
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrations: %v", err)
	}
	return out
}

