package githubactions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ghauth"
)

func newTestRunner(t *testing.T, srv *httptest.Server) *Runner {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	r := NewRunner(ghauth.StaticToken("test-token"), "owner", "repo", "agent-run.yml", "dev", log)
	r.client = srv.Client()
	// Rewrite API base to test server by injecting a custom transport.
	baseURL := srv.URL
	origTransport := srv.Client().Transport
	r.client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &rewriteTransport{
			baseURL:   baseURL,
			transport: origTransport,
		},
	}
	return r
}

type rewriteTransport struct {
	baseURL   string
	transport http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.baseURL, "http://")
	return rt.transport.RoundTrip(req)
}

func TestRunner_StartAndResolve(t *testing.T) {
	var dispatched bool
	var runInputs map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/repos/owner/repo/actions/workflows/agent-run.yml/dispatches" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			inputs, _ := body["inputs"].(map[string]any)
			runInputs = inputs
			dispatched = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if path == "/repos/owner/repo/actions/workflows/agent-run.yml/runs" {
			// Return a run that matches our dispatched run_id.
			resp := map[string]any{
				"workflow_runs": []map[string]any{
					{
						"id":         12345,
						"run_number": 1,
						"created_at": time.Now().UTC().Format(time.RFC3339),
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.HasPrefix(path, "/repos/owner/repo/actions/runs/") && !strings.HasSuffix(path, "/cancel") {
			var result map[string]any
			if strings.Contains(path, "/12345") {
				result = map[string]any{
					"id":         12345,
					"status":     "in_progress",
					"conclusion": nil,
					"inputs":     map[string]any{"run_id": "daemon-run-1"},
					"html_url":   "https://github.com/owner/repo/actions/runs/12345",
				}
			} else {
				result = map[string]any{
					"status":     "completed",
					"conclusion": "success",
				}
			}
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestRunner(t, srv)
	ctx := context.Background()
	id, err := r.Start(ctx, "agent:latest", nil, map[string]string{"AGENTS_RUN_ID": "daemon-run-1"}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if id != "12345" {
		t.Fatalf("expected workflow run id 12345, got %s", id)
	}
	if !dispatched {
		t.Fatal("expected dispatch")
	}
	if runInputs["run_id"] != "daemon-run-1" {
		t.Fatalf("expected run_id input daemon-run-1, got %v", runInputs["run_id"])
	}
}

func TestRunner_StartErrorsWhenListedRunInputsDoNotMatch(t *testing.T) {
	var dispatched bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/repos/owner/repo/actions/workflows/agent-run.yml/dispatches" {
			dispatched = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if path == "/repos/owner/repo/actions/workflows/agent-run.yml/runs" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workflow_runs": []map[string]any{
					{
						"id":         12345,
						"run_number": 1,
						"created_at": time.Now().UTC().Format(time.RFC3339),
					},
				},
			})
			return
		}
		if path == "/repos/owner/repo/actions/runs/12345" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     12345,
				"inputs": map[string]any{"run_id": "some-other-run"},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestRunner(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	id, err := r.Start(ctx, "agent:latest", nil, map[string]string{"AGENTS_RUN_ID": "daemon-run-1"}, nil)
	if err == nil {
		t.Fatalf("expected start error for mismatched workflow run inputs, got id %q", id)
	}
	if id == "12345" {
		t.Fatalf("expected not to return mismatched workflow run id, got %s", id)
	}
	if !strings.Contains(err.Error(), "resolve workflow run") {
		t.Fatalf("expected resolve workflow run error, got %v", err)
	}
	if !dispatched {
		t.Fatal("expected dispatch")
	}
}

func TestRunner_IsRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result map[string]any
		switch r.URL.Path {
		case "/repos/owner/repo/actions/runs/1":
			result = map[string]any{"status": "in_progress", "conclusion": nil}
		case "/repos/owner/repo/actions/runs/2":
			result = map[string]any{"status": "completed", "conclusion": "success"}
		case "/repos/owner/repo/actions/runs/3":
			result = map[string]any{"status": "queued", "conclusion": nil}
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	r := newTestRunner(t, srv)
	ctx := context.Background()

	if ok, err := r.IsRunning(ctx, "1"); err != nil || !ok {
		t.Fatalf("expected running, got %v, err %v", ok, err)
	}
	if ok, err := r.IsRunning(ctx, "2"); err != nil || ok {
		t.Fatalf("expected not running, got %v, err %v", ok, err)
	}
	if ok, err := r.IsRunning(ctx, "3"); err != nil || !ok {
		t.Fatalf("expected running (queued), got %v, err %v", ok, err)
	}
}

func TestRunner_ExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result map[string]any
		switch r.URL.Path {
		case "/repos/owner/repo/actions/runs/10":
			result = map[string]any{"status": "completed", "conclusion": "success"}
		case "/repos/owner/repo/actions/runs/11":
			result = map[string]any{"status": "completed", "conclusion": "failure"}
		case "/repos/owner/repo/actions/runs/12":
			result = map[string]any{"status": "completed", "conclusion": "cancelled"}
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	r := newTestRunner(t, srv)
	ctx := context.Background()

	code, _ := r.ExitCode(ctx, "10")
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	code, _ = r.ExitCode(ctx, "11")
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
	code, _ = r.ExitCode(ctx, "12")
	if code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
}

func TestRunner_Stop(t *testing.T) {
	var cancelled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo/actions/runs/99/cancel" {
			cancelled = true
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestRunner(t, srv)
	ctx := context.Background()
	if err := r.Stop(ctx, "99"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !cancelled {
		t.Fatal("expected cancel call")
	}
}

func TestRunner_Logs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo/actions/runs/77" {
			_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/owner/repo/actions/runs/77"})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestRunner(t, srv)
	ctx := context.Background()
	url, err := r.Logs(ctx, "77")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(url, "actions/runs/77") {
		t.Fatalf("expected run url, got %s", url)
	}
}

func TestRunner_StartMissingRunID(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	r := NewRunner(ghauth.StaticToken("tok"), "o", "r", "w.yml", "dev", log)
	ctx := context.Background()
	_, err := r.Start(ctx, "img", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing run_id")
	}
}

func TestRunner_ConcurrencyCap(t *testing.T) {
	// The githubactions runner itself doesn't enforce a cap; the queue does.
	// This test verifies the runner can be started many times without internal blocking.
	dispatchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo/actions/workflows/agent-run.yml/dispatches" {
			dispatchCount++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/repos/owner/repo/actions/workflows/agent-run.yml/runs" {
			// Return matching run.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workflow_runs": []map[string]any{
					{
						"id":         dispatchCount,
						"run_number": dispatchCount,
						"created_at": time.Now().UTC().Format(time.RFC3339),
					},
				},
			})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/repos/owner/repo/actions/runs/") {
			// Single run endpoint – return inputs that match the dispatched run_id.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     dispatchCount,
				"inputs": map[string]any{"run_id": fmt.Sprintf("r%d", dispatchCount)},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestRunner(t, srv)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := r.Start(ctx, "img", nil, map[string]string{"AGENTS_RUN_ID": fmt.Sprintf("r%d", i)}, nil)
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if dispatchCount != 5 {
		t.Fatalf("expected 5 dispatches, got %d", dispatchCount)
	}
}

