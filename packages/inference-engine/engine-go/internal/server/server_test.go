package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/election"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/events"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/queue"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/runner"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/store"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

type mockDocker struct{}

func (m *mockDocker) Ping(ctx context.Context) error { return nil }
func (m *mockDocker) Start(ctx context.Context, image string, cmd []string, env, labels map[string]string) (string, error) {
	return "cid-" + image, nil
}
func (m *mockDocker) Stop(ctx context.Context, id string) error              { return nil }
func (m *mockDocker) Remove(ctx context.Context, id string) error            { return nil }
func (m *mockDocker) IsRunning(ctx context.Context, id string) (bool, error) { return false, nil }
func (m *mockDocker) ExitCode(ctx context.Context, id string) (int, error)   { return 0, nil }
func (m *mockDocker) Logs(ctx context.Context, id string) (string, error)    { return "logs", nil }
func (m *mockDocker) URL(ctx context.Context, id string) (string, error)     { return "", nil }
func (m *mockDocker) Close() error                                           { return nil }

var _ runner.Interface = (*mockDocker)(nil)

type standbyElection struct{}

func (standbyElection) Run(ctx context.Context) error { return nil }
func (standbyElection) IsLeader() bool                { return false }
func (standbyElection) NodeID() string                { return "standby-test" }

func setupServer(t *testing.T) (*Server, *store.Store, context.Context) {
	return setupServerWithElection(t, election.NewNoop("test"))
}

func setupServerWithElection(t *testing.T, elec election.Election) (*Server, *store.Store, context.Context) {
	path := "test_server.db"

	st, err := store.New(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	md := &mockDocker{}
	bus := events.NewNoop()
	broker := ops.NewBroker(st)
	q := queue.New(2, st, md, bus, broker, log)
	ctx := context.Background()
	q.Start(ctx)
	srv := New("127.0.0.1:0", "test", q, st, elec, log)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(shutdown)
		q.Stop()
		st.Close()
		os.Remove(path)
	})
	return srv, st, ctx
}

func TestServer_Health(t *testing.T) {
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp contracts.HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "healthy" {
		t.Fatalf("expected healthy, got %s", resp.Status)
	}
	if resp.Capacity.Max != 2 {
		t.Fatalf("expected max 2, got %d", resp.Capacity.Max)
	}
	if resp.NodeID != "test" || !resp.Leader {
		t.Fatalf("expected leader identity, got node=%q leader=%v", resp.NodeID, resp.Leader)
	}
	if resp.Executor != "kubernetes" || resp.Namespace != "agents" {
		t.Fatalf("expected execution identity, got executor=%q namespace=%q", resp.Executor, resp.Namespace)
	}
}

func TestServer_HealthCountsAllKnownTerminalStatuses(t *testing.T) {
	srv, st, ctx := setupServer(t)
	now := time.Now().UTC()
	statuses := []contracts.RunStatus{
		contracts.RunStatusSucceeded,
		contracts.RunStatusFailed,
		contracts.RunStatusInfraFailed,
		contracts.RunStatusNoOp,
		contracts.RunStatusBlocked,
		contracts.RunStatusCancelled,
		contracts.RunStatus("mystery-green"),
	}
	for i, status := range statuses {
		if err := st.SaveRun(ctx, &contracts.Run{
			ID:        string(rune('a' + i)),
			Status:    status,
			Image:     "alpine",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("save run %s: %v", status, err)
		}
	}

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp contracts.HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Capacity.Completed != 6 {
		t.Fatalf("expected six known terminal statuses counted as completed, got %d", resp.Capacity.Completed)
	}
}

func TestServer_SubmitRun(t *testing.T) {
	srv, st, ctx := setupServer(t)

	body, _ := json.Marshal(contracts.SubmitRunRequest{Image: "alpine", Command: []string{"echo", "hi"}})
	req := httptest.NewRequest("POST", "/v1/runs", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var run contracts.Run
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if run.Image != "alpine" {
		t.Fatalf("expected alpine, got %s", run.Image)
	}

	// Verify persisted
	got, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.ID != run.ID {
		t.Fatalf("run not persisted")
	}
}

func TestServer_SubmitRunRejectsStandby(t *testing.T) {
	srv, _, _ := setupServerWithElection(t, standbyElection{})

	body, _ := json.Marshal(contracts.SubmitRunRequest{Image: "alpine", Command: []string{"echo", "hi"}})
	req := httptest.NewRequest("POST", "/v1/runs", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["error"] != "not_leader" {
		t.Fatalf("expected not_leader, got %q", resp["error"])
	}
	if resp["node_id"] != "standby-test" {
		t.Fatalf("expected standby-test, got %q", resp["node_id"])
	}
}

func TestServer_GetRun(t *testing.T) {
	srv, st, ctx := setupServer(t)

	st.SaveRun(ctx, &contracts.Run{ID: "r99", Status: contracts.RunStatusQueued, Image: "alpine", CreatedAt: time.Now().UTC()})

	req := httptest.NewRequest("GET", "/v1/runs/r99", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var run contracts.Run
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if run.ID != "r99" {
		t.Fatalf("expected r99, got %s", run.ID)
	}
}

func TestServer_ListRuns(t *testing.T) {
	srv, st, ctx := setupServer(t)
	st.SaveRun(ctx, &contracts.Run{ID: "r1", Status: contracts.RunStatusQueued, Image: "alpine", CreatedAt: time.Now().UTC()})

	req := httptest.NewRequest("GET", "/v1/runs", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string][]contracts.Run
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp["runs"]) < 1 {
		t.Fatalf("expected at least 1 run, got %d", len(resp["runs"]))
	}
}

func TestServer_Logs(t *testing.T) {
	srv, st, ctx := setupServer(t)
	st.SaveRun(ctx, &contracts.Run{ID: "r-log", Status: contracts.RunStatusSucceeded, Image: "alpine", Logs: "hello world", CreatedAt: time.Now().UTC()})

	req := httptest.NewRequest("GET", "/v1/runs/r-log/logs", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello world") {
		t.Fatalf("expected logs, got %s", w.Body.String())
	}
}

func TestServer_Cancel(t *testing.T) {
	srv, st, ctx := setupServer(t)
	// Submit via queue so it is tracked in memory.
	srv.queue.Submit(ctx, &contracts.Run{ID: "r-cancel", Image: "alpine", Command: []string{"sleep", "10"}, CreatedAt: time.Now().UTC()})
	time.Sleep(100 * time.Millisecond)

	req := httptest.NewRequest("POST", "/v1/runs/r-cancel/cancel", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	got, _ := st.GetRun(ctx, "r-cancel")
	if got.Status != contracts.RunStatusCancelled {
		t.Fatalf("expected cancelled, got %s", got.Status)
	}
}

func TestServer_CancelTerminalRunIsIdempotent(t *testing.T) {
	srv, st, ctx := setupServer(t)
	finished := time.Now().UTC()
	run := &contracts.Run{
		ID:         "r-terminal",
		Status:     contracts.RunStatusFailed,
		Image:      "alpine",
		Command:    []string{"false"},
		CreatedAt:  finished.Add(-time.Minute),
		FinishedAt: &finished,
	}
	if err := st.SaveRun(ctx, run); err != nil {
		t.Fatalf("save terminal run: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/runs/r-terminal/cancel", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "already-terminal") {
		t.Fatalf("expected terminal acknowledgement, got %s", w.Body.String())
	}
	got, err := st.GetRun(ctx, "r-terminal")
	if err != nil {
		t.Fatalf("get terminal run: %v", err)
	}
	if got.Status != contracts.RunStatusFailed {
		t.Fatalf("terminal status should not change, got %s", got.Status)
	}
}

func TestServer_BadSubmit(t *testing.T) {
	srv, _, _ := setupServer(t)

	body, _ := json.Marshal(map[string]string{"image": ""})
	req := httptest.NewRequest("POST", "/v1/runs", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

