package manager

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

func TestControlAPIHealthRoute(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()

	w := requestControl(t, o, "GET", "/v1/control/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body["status"] != "healthy" || body["leader"] != true {
		t.Fatalf("unexpected health body: %#v", body)
	}
}

func TestControlAPIListAndGetRunsSnakeCaseDTO(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()
	ctx := context.Background()
	if err := o.store.RecordRun(ctx, "run-control", 42, 3, "Task", "run/control", "https://example.test/pr/1", 1000); err != nil {
		t.Fatalf("record run: %v", err)
	}
	if err := o.store.SetRunTaskID(ctx, "run-control", "qft-task-1"); err != nil {
		t.Fatalf("set task id: %v", err)
	}
	if err := o.store.SetRunEvidencePath(ctx, "run-control", "/tmp/run-control.json"); err != nil {
		t.Fatalf("set evidence path: %v", err)
	}

	list := requestControl(t, o, "GET", "/v1/control/runs", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d: %s", list.Code, list.Body.String())
	}
	var listBody struct {
		Runs []controlRunDTO `json:"runs"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Runs) != 1 {
		t.Fatalf("expected one run, got %#v", listBody.Runs)
	}
	assertControlRunDTO(t, listBody.Runs[0])
	if strings.Contains(list.Body.String(), "RunID") || strings.Contains(list.Body.String(), "IssueNumber") {
		t.Fatalf("response exposed Go field names: %s", list.Body.String())
	}

	get := requestControl(t, o, "GET", "/v1/control/runs/run-control", "")
	if get.Code != http.StatusOK {
		t.Fatalf("get expected 200, got %d: %s", get.Code, get.Body.String())
	}
	var got controlRunDTO
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	assertControlRunDTO(t, got)
}

func TestControlAPIGetRun404(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()

	w := requestControl(t, o, "GET", "/v1/control/runs/missing", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestControlAPILogsProxy(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/runs/run-proxy/logs" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("proxy logs"))
	}))
	defer daemon.Close()
	o, cleanup := newControlTestManager(t, NewDaemonClient(daemon.URL))
	defer cleanup()

	logs := requestControl(t, o, "GET", "/v1/control/runs/run-proxy/logs", "")
	if logs.Code != http.StatusOK || logs.Body.String() != "proxy logs" {
		t.Fatalf("unexpected logs response %d: %q", logs.Code, logs.Body.String())
	}
}

func TestControlAPILogsUnavailableAndFailure(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()
	if w := requestControl(t, o, "GET", "/v1/control/runs/run/logs", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected unavailable daemon 503, got %d: %s", w.Code, w.Body.String())
	}

	daemon := httptest.NewServer(http.NotFoundHandler())
	defer daemon.Close()
	o.daemon = NewDaemonClient(daemon.URL)
	if w := requestControl(t, o, "GET", "/v1/control/runs/run/logs", ""); w.Code == http.StatusOK {
		t.Fatalf("daemon log failure must not be 200: %s", w.Body.String())
	}
}

func TestControlAPICancelSuccessAndStateTimestamp(t *testing.T) {
	cancelled := false
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/runs/run-cancel/cancel" {
			http.NotFound(w, r)
			return
		}
		cancelled = true
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}))
	defer daemon.Close()
	o, cleanup := newControlTestManager(t, NewDaemonClient(daemon.URL))
	defer cleanup()
	if err := o.store.RecordRun(context.Background(), "run-cancel", 42, 0, "Task", "run/cancel", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}

	cancel := requestControl(t, o, "POST", "/v1/control/runs/run-cancel/cancel", "")
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel expected 200, got %d: %s", cancel.Code, cancel.Body.String())
	}
	if !cancelled {
		t.Fatal("daemon cancel endpoint was not called")
	}
	run, err := o.store.GetRun(context.Background(), "run-cancel")
	if err != nil {
		t.Fatalf("get cancelled run: %v", err)
	}
	if run.CancellationRequestedAt == nil {
		t.Fatalf("cancellation request was not recorded: %#v", run)
	}
}

func TestControlAPICancelDaemonFailureAndUnknown(t *testing.T) {
	daemon := httptest.NewServer(http.NotFoundHandler())
	defer daemon.Close()
	o, cleanup := newControlTestManager(t, NewDaemonClient(daemon.URL))
	defer cleanup()
	if err := o.store.RecordRun(context.Background(), "run-cancel", 42, 0, "Task", "run/cancel", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}

	if w := requestControl(t, o, "POST", "/v1/control/runs/missing/cancel", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown run should be 404 before daemon call, got %d: %s", w.Code, w.Body.String())
	}
	if w := requestControl(t, o, "POST", "/v1/control/runs/run-cancel/cancel", ""); w.Code == http.StatusOK {
		t.Fatalf("daemon cancel failure must not return 200: %s", w.Body.String())
	}
	run, err := o.store.GetRun(context.Background(), "run-cancel")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.CancellationRequestedAt != nil {
		t.Fatalf("daemon failure should not mark cancellation requested: %#v", run)
	}
}

func TestControlAPITouchSuccessAnd404(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()
	if err := o.store.RecordRun(context.Background(), "run-touch", 42, 0, "Task", "run/touch", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}
	before, err := o.store.GetRun(context.Background(), "run-touch")
	if err != nil {
		t.Fatalf("get before touch: %v", err)
	}

	if w := requestControl(t, o, "POST", "/v1/control/runs/run-touch/touch", ""); w.Code != http.StatusOK {
		t.Fatalf("touch expected 200, got %d: %s", w.Code, w.Body.String())
	}
	after, err := o.store.GetRun(context.Background(), "run-touch")
	if err != nil {
		t.Fatalf("get after touch: %v", err)
	}
	if after.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatalf("touch should not move updated_at backwards: before=%s after=%s", before.UpdatedAt, after.UpdatedAt)
	}
	if w := requestControl(t, o, "POST", "/v1/control/runs/missing/touch", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown touch should be 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestControlAPICreateQFTTaskAppendsViaQueueStore(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()
	root := t.TempDir()
	queue := filepath.Join(root, "projects", "qft", "tasks", "queue.jsonl")
	installQFTQueueStoreHelper(t, root)
	t.Setenv("AGENTS_ROOT", root)
	t.Setenv("QFT_QUEUE_PATH", queue)

	body := `{"title":"Investigate QFT target","description":"Produce an allowed proof artifact","priority":"high"}`
	w := requestControl(t, o, "POST", "/v1/control/qft/tasks", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if id, ok := got["id"].(string); !ok || !strings.HasPrefix(id, "qft-task-") {
		t.Fatalf("task id should be qft-task-* string: %#v", got["id"])
	}
	if got["tenant"] != "qft" || got["status"] != "pending" || got["priority"] != "high" || got["created_by"] != "tui" {
		t.Fatalf("unexpected task metadata: %#v", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, got["created_at"].(string)); err != nil {
		t.Fatalf("created_at should be RFC3339Nano: %#v", got["created_at"])
	}

	data, err := os.ReadFile(queue)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one queue record, got %d: %q", len(lines), string(data))
	}
	var queued map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &queued); err != nil {
		t.Fatalf("decode queued record: %v", err)
	}
	if queued["id"] != got["id"] || queued["title"] != "Investigate QFT target" {
		t.Fatalf("queued record did not match response: queued=%#v response=%#v", queued, got)
	}
}

func TestControlAPICreateQFTTaskPreservesFollowUpMetadata(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()
	root := t.TempDir()
	queue := filepath.Join(root, "projects", "qft", "tasks", "queue.jsonl")
	installQFTQueueStoreHelper(t, root)
	t.Setenv("AGENTS_ROOT", root)
	t.Setenv("QFT_QUEUE_PATH", queue)

	body := `{"title":"Follow up QFT run","description":"Continue from run evidence","parent_run_id":"run-qft-1","parent_issue_number":123}`
	w := requestControl(t, o, "POST", "/v1/control/qft/tasks", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("follow-up create expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode follow-up response: %v", err)
	}
	if got["parent_run_id"] != "run-qft-1" || got["parent_issue_number"] != float64(123) {
		t.Fatalf("follow-up metadata missing: %#v", got)
	}
	if got["priority"] != "normal" {
		t.Fatalf("empty priority should default to normal: %#v", got)
	}
}

func TestControlAPICreateQFTTaskFailsClosedWithoutRuntimeRoot(t *testing.T) {
	o, cleanup := newControlTestManager(t, nil)
	defer cleanup()
	t.Setenv("AGENTS_ROOT", "")
	t.Setenv("QFT_QUEUE_PATH", "")

	body := `{"title":"QFT","description":"must not silently fall back"}`
	w := requestControl(t, o, "POST", "/v1/control/qft/tasks", body)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing AGENTS_ROOT should fail closed with 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuildControlQFTTaskRecordValidation(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if _, err := buildControlQFTTaskRecord(controlQFTTaskRequest{Description: "desc"}, now); err == nil {
		t.Fatal("missing title should fail")
	}
	if _, err := buildControlQFTTaskRecord(controlQFTTaskRequest{Title: "title"}, now); err == nil {
		t.Fatal("missing description should fail")
	}
	if _, err := buildControlQFTTaskRecord(controlQFTTaskRequest{Title: "title", Description: "desc", Priority: "urgent"}, now); err == nil {
		t.Fatal("invalid priority should fail")
	}
}

func installQFTQueueStoreHelper(t *testing.T, agentsRoot string) {
	t.Helper()
	target := filepath.Join(agentsRoot, ".user", "projects", "qft", "queue_store.py")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create qft helper dir: %v", err)
	}
	if err := os.WriteFile(target, []byte(qftQueueStoreFixture), 0o755); err != nil {
		t.Fatalf("write qft helper: %v", err)
	}
}

const qftQueueStoreFixture = `#!/usr/bin/env python3
import argparse
import json
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("command", choices=["append"])
parser.add_argument("--queue", required=True)
parser.add_argument("--json", required=True)
args = parser.parse_args()

record = json.loads(args.json)
queue = Path(args.queue)
queue.parent.mkdir(parents=True, exist_ok=True)
with queue.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps(record, separators=(",", ":")) + "\n")
`

func assertControlRunDTO(t *testing.T, got controlRunDTO) {
	t.Helper()
	if got.RunID != "run-control" || got.IssueNumber != 42 || got.SubtaskIndex != 3 || got.SubtaskTitle != "Task" {
		t.Fatalf("unexpected core DTO fields: %#v", got)
	}
	if got.Branch != "run/control" || got.PRURL != "https://example.test/pr/1" || got.LogIssueNumber != 1000 {
		t.Fatalf("unexpected link DTO fields: %#v", got)
	}
	if got.LastStatus != contracts.RunStatusQueued || got.TaskID != "qft-task-1" || got.EvidencePath != "/tmp/run-control.json" {
		t.Fatalf("unexpected status/control DTO fields: %#v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps missing from DTO: %#v", got)
	}
}

func newControlTestManager(t *testing.T, daemon *DaemonClient) (*Manager, func()) {
	t.Helper()
	st, err := NewStateStore(filepath.Join(t.TempDir(), "manager-state.db"))
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}
	cfg := DefaultConfig()
	cfg.DaemonURL = ""
	cfg.DaemonURLs = nil
	o := NewManager(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)), nil, nil, daemon, st, nil, nil)
	return o, func() { _ = st.Close() }
}

func requestControl(t *testing.T, o *Manager, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	o.registerControlRoutes(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

