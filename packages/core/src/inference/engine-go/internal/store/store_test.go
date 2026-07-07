package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

func TestStore_RunCRUD(t *testing.T) {
	path := "test_store.db"

	st, err := New(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { st.Close(); os.Remove(path) }()

	ctx := context.Background()
	now := time.Now().UTC()
	r := &contracts.Run{
		ID:           "run-1",
		Status:       contracts.RunStatusQueued,
		Image:        "alpine:latest",
		Command:      []string{"echo", "hello"},
		Env:          map[string]string{"X": "1"},
		Labels:       map[string]string{"tenant": "qft"},
		HeadSHA:      "0123456789abcdef0123456789abcdef01234567",
		TaskID:       "qft-task-run-1",
		EvidencePath: "/tmp/run-1.json",
		CreatedAt:    now,
	}

	if err := st.SaveRun(ctx, r); err != nil {
		t.Fatalf("save run: %v", err)
	}

	got, err := st.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.ID != r.ID || got.Image != r.Image {
		t.Fatalf("run mismatch: %+v", got)
	}
	if len(got.Command) != 2 || got.Command[0] != "echo" {
		t.Fatalf("command mismatch: %v", got.Command)
	}
	if got.Env["X"] != "1" {
		t.Fatalf("env mismatch: %v", got.Env)
	}
	if got.HeadSHA != r.HeadSHA || got.TaskID != "qft-task-run-1" || got.EvidencePath != "/tmp/run-1.json" {
		t.Fatalf("evidence fields mismatch: head=%q task=%q evidence=%q", got.HeadSHA, got.TaskID, got.EvidencePath)
	}

	// Update status
	r.Status = contracts.RunStatusRunning
	if err := st.SaveRun(ctx, r); err != nil {
		t.Fatalf("update run: %v", err)
	}
	got, _ = st.GetRun(ctx, "run-1")
	if got.Status != contracts.RunStatusRunning {
		t.Fatalf("expected running, got %s", got.Status)
	}
}

func TestStore_ListRuns(t *testing.T) {
	path := "test_list.db"

	st, err := New(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { st.Close(); os.Remove(path) }()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		st.SaveRun(ctx, &contracts.Run{
			ID:        fmt.Sprintf("run-%d", i),
			Status:    contracts.RunStatusQueued,
			Image:     "alpine",
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		})
	}

	runs, err := st.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(runs))
	}
}

func TestStore_CountRunsByStatus(t *testing.T) {
	path := "test_count.db"

	st, err := New(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { st.Close(); os.Remove(path) }()

	ctx := context.Background()
	st.SaveRun(ctx, &contracts.Run{ID: "r1", Status: contracts.RunStatusQueued, Image: "alpine", CreatedAt: time.Now().UTC()})
	st.SaveRun(ctx, &contracts.Run{ID: "r2", Status: contracts.RunStatusRunning, Image: "alpine", CreatedAt: time.Now().UTC()})
	st.SaveRun(ctx, &contracts.Run{ID: "r3", Status: contracts.RunStatusRunning, Image: "alpine", CreatedAt: time.Now().UTC()})

	counts, err := st.CountRunsByStatus(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts[contracts.RunStatusQueued] != 1 {
		t.Fatalf("expected 1 queued, got %d", counts[contracts.RunStatusQueued])
	}
	if counts[contracts.RunStatusRunning] != 2 {
		t.Fatalf("expected 2 running, got %d", counts[contracts.RunStatusRunning])
	}
}

func TestStore_OperationIdempotency(t *testing.T) {
	path := "test_ops.db"

	st, err := New(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { st.Close(); os.Remove(path) }()

	ctx := context.Background()
	op := contracts.OperationEnvelope{
		OperationID:    "op-1",
		IdempotencyKey: "key-abc",
		Intent:         "test",
		CreatedAt:      time.Now().UTC(),
	}

	seen, err := st.HasOperation(ctx, "key-abc")
	if err != nil {
		t.Fatalf("has op: %v", err)
	}
	if seen {
		t.Fatal("expected not seen")
	}

	if err := st.RecordOperation(ctx, op, "ok"); err != nil {
		t.Fatalf("record: %v", err)
	}

	seen, err = st.HasOperation(ctx, "key-abc")
	if err != nil {
		t.Fatalf("has op 2: %v", err)
	}
	if !seen {
		t.Fatal("expected seen")
	}

	// Duplicate record should not error.
	if err := st.RecordOperation(ctx, op, "ok"); err != nil {
		t.Fatalf("duplicate record: %v", err)
	}
}

