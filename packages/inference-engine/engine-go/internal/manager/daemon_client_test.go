package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

func TestDaemonClientSubmitRunSkipsStandby(t *testing.T) {
	var standbyCalls atomic.Int32
	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		standbyCalls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/runs" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"not_leader","node_id":"s002"}`))
	}))
	defer standby.Close()

	leader := newSubmitRunServer(t, "run-leader")
	defer leader.Close()

	client := NewDaemonClientMulti([]string{standby.URL, leader.URL})
	run, err := client.SubmitRun(context.Background(), contracts.SubmitRunRequest{
		Image:   "alpine",
		Command: []string{"echo", "ok"},
	})
	if err != nil {
		t.Fatalf("SubmitRun: %v", err)
	}
	if run.ID != "run-leader" {
		t.Fatalf("run ID = %q, want %q", run.ID, "run-leader")
	}

	run, err = client.SubmitRun(context.Background(), contracts.SubmitRunRequest{Image: "alpine"})
	if err != nil {
		t.Fatalf("second SubmitRun: %v", err)
	}
	if run.ID != "run-leader" {
		t.Fatalf("second run ID = %q, want %q", run.ID, "run-leader")
	}
	if got := standbyCalls.Load(); got != 1 {
		t.Fatalf("standby calls = %d, want 1", got)
	}
}

func TestDaemonClientSubmitRunLeaderFirst(t *testing.T) {
	leader := newSubmitRunServer(t, "run-first")
	defer leader.Close()

	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("standby should not be called when cached start is leader")
	}))
	defer standby.Close()

	client := NewDaemonClientMulti([]string{leader.URL, standby.URL})
	run, err := client.SubmitRun(context.Background(), contracts.SubmitRunRequest{
		Image:   "alpine",
		Command: []string{"echo", "ok"},
	})
	if err != nil {
		t.Fatalf("SubmitRun: %v", err)
	}
	if run.ID != "run-first" {
		t.Fatalf("run ID = %q, want %q", run.ID, "run-first")
	}
}

func TestDaemonClientGetLogsFailover(t *testing.T) {
	standby := httptest.NewServer(http.NotFoundHandler())
	defer standby.Close()
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/runs/run-logs/logs" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("leader logs"))
	}))
	defer leader.Close()

	client := NewDaemonClientMulti([]string{standby.URL, leader.URL})
	logs, err := client.GetLogs(context.Background(), "run-logs")
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if logs != "leader logs" {
		t.Fatalf("logs = %q, want leader logs", logs)
	}
}

func TestDaemonClientCancelRunFailover(t *testing.T) {
	var cancelled atomic.Int32
	standby := httptest.NewServer(http.NotFoundHandler())
	defer standby.Close()
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/runs/run-cancel/cancel" {
			http.NotFound(w, r)
			return
		}
		cancelled.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}))
	defer leader.Close()

	client := NewDaemonClientMulti([]string{standby.URL, leader.URL})
	if err := client.CancelRun(context.Background(), "run-cancel"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if cancelled.Load() != 1 {
		t.Fatalf("cancelled calls = %d, want 1", cancelled.Load())
	}
}

func newSubmitRunServer(t *testing.T, runID string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/runs" {
			http.NotFound(w, r)
			return
		}
		var req contracts.SubmitRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		run := contracts.Run{
			ID:        runID,
			Status:    contracts.RunStatusQueued,
			Image:     req.Image,
			Command:   req.Command,
			CreatedAt: time.Now().UTC(),
		}
		w.WriteHeader(http.StatusAccepted)
		if err := json.NewEncoder(w).Encode(run); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
}

