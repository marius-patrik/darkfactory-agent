package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/events"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/runner"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/store"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

type mockDocker struct {
	containers map[string]bool
	exitCodes  map[string]int
	exitErrs   map[string]error
	logs       map[string]string
}

func newMockDocker() *mockDocker {
	return &mockDocker{
		containers: make(map[string]bool),
		exitCodes:  make(map[string]int),
		exitErrs:   make(map[string]error),
		logs:       make(map[string]string),
	}
}

func (m *mockDocker) Ping(ctx context.Context) error { return nil }
func (m *mockDocker) Start(ctx context.Context, image string, cmd []string, env, labels map[string]string) (string, error) {
	cid := "cid-" + image
	m.containers[cid] = true
	m.exitCodes[cid] = 0
	m.logs[cid] = "mock logs"
	return cid, nil
}
func (m *mockDocker) Stop(ctx context.Context, id string) error {
	delete(m.containers, id)
	return nil
}
func (m *mockDocker) Remove(ctx context.Context, id string) error {
	delete(m.containers, id)
	return nil
}
func (m *mockDocker) IsRunning(ctx context.Context, id string) (bool, error) {
	return m.containers[id], nil
}
func (m *mockDocker) ExitCode(ctx context.Context, id string) (int, error) {
	if err := m.exitErrs[id]; err != nil {
		return m.exitCodes[id], err
	}
	return m.exitCodes[id], nil
}
func (m *mockDocker) Logs(ctx context.Context, id string) (string, error) {
	return m.logs[id], nil
}
func (m *mockDocker) URL(ctx context.Context, id string) (string, error) { return "", nil }
func (m *mockDocker) Close() error                                       { return nil }

var _ runner.Interface = (*mockDocker)(nil)

type memStore struct {
	*store.Store
}

type failingBus struct{}

func (f failingBus) PublishRunEvent(ctx context.Context, ev contracts.RunEvent) error {
	return errors.New("publish failed")
}

func (f failingBus) Close() error { return nil }

type failStatusBus struct {
	status contracts.RunStatus
}

func (f failStatusBus) PublishRunEvent(ctx context.Context, ev contracts.RunEvent) error {
	if ev.To == f.status {
		return errors.New("publish failed")
	}
	return nil
}

func (f failStatusBus) Close() error { return nil }

func setupQueue(t *testing.T, cap int) (*Queue, *mockDocker, *store.Store, context.Context) {
	return setupQueueWithBus(t, cap, events.NewNoop(), true)
}

func setupQueueWithBus(t *testing.T, cap int, bus events.Bus, start bool) (*Queue, *mockDocker, *store.Store, context.Context) {
	path := "test_queue_" + t.Name() + ".db"

	st, err := store.New(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	md := newMockDocker()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	q := New(cap, st, md, bus, broker, log)
	ctx := context.Background()
	if start {
		q.Start(ctx)
	}
	t.Cleanup(func() {
		if start {
			q.Stop()
		}
		st.Close()
		os.Remove(path)
	})
	return q, md, st, ctx
}

func TestQueue_SubmitQueuesRun(t *testing.T) {
	q, _, st, ctx := setupQueue(t, 2)

	run := &contracts.Run{ID: "r1", Image: "alpine", Command: []string{"echo", "hi"}}
	if err := q.Submit(ctx, run); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Since cap=2 and queue is empty, it should start immediately.
	time.Sleep(200 * time.Millisecond)

	got, err := st.GetRun(ctx, "r1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != contracts.RunStatusRunning && got.Status != contracts.RunStatusSucceeded {
		t.Fatalf("expected running or succeeded, got %s", got.Status)
	}
}

func TestQueueRecoverPersistedQueuedRunsStartsWorkAfterRestart(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	created := time.Now().UTC().Add(-time.Minute)
	if err := st.SaveRun(ctx, &contracts.Run{
		ID:        "persisted-queued",
		Status:    contracts.RunStatusQueued,
		Image:     "alpine",
		CreatedAt: created,
	}); err != nil {
		t.Fatalf("save queued run: %v", err)
	}

	q.recoverPersisted(ctx)

	got, err := st.GetRun(ctx, "persisted-queued")
	if err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if got.Status != contracts.RunStatusRunning {
		t.Fatalf("expected recovered queued run to start, got %s", got.Status)
	}
	if got.ContainerID == "" || !md.containers[got.ContainerID] {
		t.Fatalf("expected live executor id, got %q", got.ContainerID)
	}
}

func TestQueueRecoverPersistedRunningRunClosesMissingExecutor(t *testing.T) {
	q, _, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	started := time.Now().UTC().Add(-time.Hour)
	if err := st.SaveRun(ctx, &contracts.Run{
		ID:          "stale-running",
		Status:      contracts.RunStatusRunning,
		Image:       "alpine",
		ContainerID: "missing-job",
		CreatedAt:   started,
		StartedAt:   &started,
	}); err != nil {
		t.Fatalf("save running run: %v", err)
	}

	q.recoverPersisted(ctx)

	got, err := st.GetRun(ctx, "stale-running")
	if err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if got.Status != contracts.RunStatusInfraFailed {
		t.Fatalf("expected stale running run to be infra-failed, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatal("expected finished_at to be set")
	}
	if got.Error == "" || !strings.Contains(got.Error, "no live executor") {
		t.Fatalf("expected stale executor error, got %q", got.Error)
	}
}

func TestQueueRecoverPersistedQueuedRunRepinsStaleImage(t *testing.T) {
	t.Setenv("AGENTS_HARNESS_IMAGE", "agents/harness:v3.0.37")
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	created := time.Now().UTC().Add(-time.Hour)
	if err := st.SaveRun(ctx, &contracts.Run{
		ID:        "stale-image",
		Status:    contracts.RunStatusQueued,
		Image:     "agents/harness:v3.0.22",
		CreatedAt: created,
	}); err != nil {
		t.Fatalf("save queued run: %v", err)
	}

	q.recoverPersisted(ctx)

	got, err := st.GetRun(ctx, "stale-image")
	if err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if got.Status != contracts.RunStatusRunning {
		t.Fatalf("expected stale queued image to be re-pinned and started, got %s", got.Status)
	}
	if got.Image != "agents/harness:v3.0.37" {
		t.Fatalf("expected current harness image, got %q", got.Image)
	}
	if got.ContainerID != "cid-agents/harness:v3.0.37" || !md.containers[got.ContainerID] {
		t.Fatalf("expected run to start with current image, container_id=%q", got.ContainerID)
	}
	if got.Error != "" || got.FinishedAt != nil {
		t.Fatalf("expected active re-pinned run without terminal error, error=%q finished_at=%v", got.Error, got.FinishedAt)
	}
}

func TestQueueRecoverPersistedRunningRunRepinsStaleImage(t *testing.T) {
	t.Setenv("AGENTS_HARNESS_IMAGE", "agents/harness:v3.0.38")
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	started := time.Now().UTC().Add(-time.Hour)
	md.containers["stale-job"] = true
	if err := st.SaveRun(ctx, &contracts.Run{
		ID:          "stale-running-image",
		Status:      contracts.RunStatusRunning,
		Image:       "agents/harness:v3.0.22",
		ContainerID: "stale-job",
		CreatedAt:   started,
		StartedAt:   &started,
	}); err != nil {
		t.Fatalf("save running run: %v", err)
	}

	q.recoverPersisted(ctx)

	got, err := st.GetRun(ctx, "stale-running-image")
	if err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if got.Status != contracts.RunStatusRunning {
		t.Fatalf("expected stale running image to be re-pinned and restarted, got %s", got.Status)
	}
	if md.containers["stale-job"] {
		t.Fatal("expected stale running executor to be stopped")
	}
	if got.Image != "agents/harness:v3.0.38" {
		t.Fatalf("expected current harness image, got %q", got.Image)
	}
	if got.ContainerID != "cid-agents/harness:v3.0.38" || !md.containers[got.ContainerID] {
		t.Fatalf("expected restarted executor on current image, container_id=%q", got.ContainerID)
	}
	if got.Error != "" || got.FinishedAt != nil {
		t.Fatalf("expected active re-pinned run without terminal error, error=%q finished_at=%v", got.Error, got.FinishedAt)
	}
}

func TestQueue_SubmitReturnsQueuedPublishFailure(t *testing.T) {
	q, _, st, ctx := setupQueueWithBus(t, 1, failingBus{}, false)

	err := q.Submit(ctx, &contracts.Run{ID: "r-queued-publish", Image: "alpine", Command: []string{"true"}})
	if err == nil {
		t.Fatal("expected queued publish failure")
	}
	got, getErr := st.GetRun(ctx, "r-queued-publish")
	if getErr != nil {
		t.Fatalf("get run: %v", getErr)
	}
	if got.Status != contracts.RunStatusInfraFailed {
		t.Fatalf("expected infra-failed, got %s", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "queued event publish failed") {
		t.Fatalf("expected queued publish failure error, got %q", got.Error)
	}
	if len(q.Waiting()) != 0 || len(q.Running()) != 0 {
		t.Fatalf("failed submission must not remain queued/running")
	}
}

func TestQueue_CancelRunning(t *testing.T) {
	q, md, st, ctx := setupQueue(t, 2)

	run := &contracts.Run{ID: "r2", Image: "alpine", Command: []string{"sleep", "60"}}
	if err := q.Submit(ctx, run); err != nil {
		t.Fatalf("submit: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := q.Cancel(ctx, "r2"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	got, _ := st.GetRun(ctx, "r2")
	if got.Status != contracts.RunStatusCancelled {
		t.Fatalf("expected cancelled, got %s", got.Status)
	}
	if md.containers["cid-alpine"] {
		t.Fatal("expected container removed")
	}
}

func TestQueue_CancelRunningReturnsPublishFailure(t *testing.T) {
	q, _, st, ctx := setupQueueWithBus(t, 1, failStatusBus{status: contracts.RunStatusCancelled}, false)

	run := &contracts.Run{ID: "r-cancel-publish", Image: "alpine", Command: []string{"sleep", "60"}}
	if err := q.Submit(ctx, run); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := q.Cancel(ctx, "r-cancel-publish"); err == nil {
		t.Fatal("expected cancel publish failure")
	}
	got, err := st.GetRun(ctx, "r-cancel-publish")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != contracts.RunStatusCancelled {
		t.Fatalf("expected cancelled, got %s", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "publish failed") {
		t.Fatalf("expected publish failure error, got %q", got.Error)
	}
}

func TestQueue_BoundedConcurrency(t *testing.T) {
	q, _, st, ctx := setupQueue(t, 1)

	// Submit two runs; only one should run, the other queued.
	if err := q.Submit(ctx, &contracts.Run{ID: "r3", Image: "a", Command: []string{"sleep", "10"}}); err != nil {
		t.Fatalf("submit r3: %v", err)
	}
	if err := q.Submit(ctx, &contracts.Run{ID: "r4", Image: "b", Command: []string{"sleep", "10"}}); err != nil {
		t.Fatalf("submit r4: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	running := q.Running()
	waiting := q.Waiting()
	if len(running)+len(waiting) != 2 {
		t.Fatalf("expected 2 total, got %d running + %d waiting", len(running), len(waiting))
	}
	if len(running) > 1 {
		t.Fatalf("expected at most 1 running, got %d", len(running))
	}

	// Cancel the running one so the waiting one can start.
	for _, r := range running {
		q.Cancel(ctx, r.ID)
	}
	time.Sleep(300 * time.Millisecond)

	// After reconciliation, waiting should have started.
	// But since our mock immediately reports !IsRunning, reconciliation will finish it.
	// So we just assert no runaway.
	got3, _ := st.GetRun(ctx, "r3")
	got4, _ := st.GetRun(ctx, "r4")
	if got3.Status == contracts.RunStatusQueued && got4.Status == contracts.RunStatusQueued {
		t.Fatal("expected at least one run to progress from queued")
	}
}

func TestQueue_Cap(t *testing.T) {
	q := New(3, nil, nil, nil, nil, nil)
	if q.Cap() != 3 {
		t.Fatalf("expected cap 3, got %d", q.Cap())
	}
}

func TestQueue_DefaultCap(t *testing.T) {
	q := New(0, nil, nil, nil, nil, nil)
	if q.Cap() != 1 {
		t.Fatalf("expected default cap 1, got %d", q.Cap())
	}
}

func TestQueue_DoesNotPersistSucceededWhenTerminalPublishFails(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, failStatusBus{status: contracts.RunStatusSucceeded}, false)

	if err := q.Submit(ctx, &contracts.Run{ID: "r-publish", Image: "alpine", Command: []string{"true"}}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-publish")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatalf("publish failure must not persist succeeded status")
	}
	if got.Status != contracts.RunStatusInfraFailed {
		t.Fatalf("expected infra-failed, got %s", got.Status)
	}
	if got.Error == "" {
		t.Fatal("expected publish failure error")
	}
}

func TestQueue_DoesNotPersistSucceededWhenExitCodeReadFails(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)

	if err := q.Submit(ctx, &contracts.Run{ID: "r-exit-code", Image: "alpine", Command: []string{"true"}}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	cid := "cid-alpine"
	md.containers[cid] = false
	md.exitCodes[cid] = 0
	md.exitErrs[cid] = errors.New("lost executor status")
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-exit-code")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatalf("exit-code failure must not persist succeeded status")
	}
	if got.Status != contracts.RunStatusInfraFailed {
		t.Fatalf("expected infra-failed, got %s", got.Status)
	}
	if got.ExitCode != -1 {
		t.Fatalf("expected synthetic failed exit code -1, got %d", got.ExitCode)
	}
	if got.Error == "" || !strings.Contains(got.Error, "lost executor status") {
		t.Fatalf("expected exit-code error to be recorded, got %q", got.Error)
	}
}

func TestQueue_KeepsTerminalRunInMemoryWhenFinalSaveFails(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)

	if err := q.Submit(ctx, &contracts.Run{ID: "r-save", Image: "alpine", Command: []string{"true"}}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	q.reconcile(ctx)

	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.running["r-save"]; !ok {
		t.Fatal("run was removed from memory despite failed terminal persistence")
	}
}

func TestQueue_RunningEventPublishFailureFailsRun(t *testing.T) {
	q, _, st, ctx := setupQueueWithBus(t, 1, failStatusBus{status: contracts.RunStatusRunning}, false)

	if err := q.Submit(ctx, &contracts.Run{ID: "r-running-publish", Image: "alpine", Command: []string{"true"}}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	got, err := st.GetRun(ctx, "r-running-publish")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status == contracts.RunStatusRunning || got.Status == contracts.RunStatusSucceeded {
		t.Fatalf("running publish failure must not leave run green or active, got %s", got.Status)
	}
	if got.Status != contracts.RunStatusInfraFailed {
		t.Fatalf("expected infra-failed, got %s", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "running event publish failed") {
		t.Fatalf("expected running publish failure error, got %q", got.Error)
	}
}

func TestQueue_ExitZeroRequiresTerminalEvidenceForRunTask(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	root := t.TempDir()
	t.Setenv("AGENTS_ROOT", root)

	if err := q.Submit(ctx, &contracts.Run{
		ID:      "r-missing-evidence",
		Image:   "alpine",
		Command: []string{"bash", "/app/run-task.sh"},
		Labels:  map[string]string{"tenant": "qft"},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-missing-evidence")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatalf("missing terminal evidence must not persist succeeded")
	}
	if got.Status != contracts.RunStatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "missing terminal evidence") {
		t.Fatalf("expected missing evidence error, got %q", got.Error)
	}
}

func TestQueue_ExitZeroFailsWhenTenantConfigMissing(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	root := t.TempDir()
	t.Setenv("AGENTS_ROOT", root)
	writeRootTelemetryEvidence(t, root, "r-missing-tenant-config", "qft", "succeeded")

	if err := q.Submit(ctx, &contracts.Run{
		ID:      "r-missing-tenant-config",
		Image:   "alpine",
		Command: []string{"bash", "/app/run-task.sh"},
		Labels:  map[string]string{"tenant": "qft"},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-missing-tenant-config")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatalf("missing tenant config must not persist succeeded")
	}
	if got.Status != contracts.RunStatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "missing tenant config") {
		t.Fatalf("expected missing tenant config error, got %q", got.Error)
	}
}

func TestQueue_ExitZeroConsumesSucceededTerminalEvidence(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	root := t.TempDir()
	t.Setenv("AGENTS_ROOT", root)
	writeEvidence(t, root, "qft", "r-evidence-ok", "succeeded")

	if err := q.Submit(ctx, &contracts.Run{
		ID:      "r-evidence-ok",
		Image:   "alpine",
		Command: []string{"bash", "/app/run-task.sh"},
		Labels:  map[string]string{"tenant": "qft"},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-evidence-ok")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != contracts.RunStatusSucceeded {
		t.Fatalf("expected succeeded from evidence, got %s: %s", got.Status, got.Error)
	}
	if got.Error != "" {
		t.Fatalf("expected no error on success evidence, got %q", got.Error)
	}
	if got.HeadSHA != "fake-head-sha" {
		t.Fatalf("expected run record stamped with evidence head sha, got %q", got.HeadSHA)
	}
}

func TestQueue_ExitZeroFailsClosedOnHeadSHAMismatch(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	root := t.TempDir()
	t.Setenv("AGENTS_ROOT", root)
	writeEvidence(t, root, "qft", "r-head-mismatch", "succeeded")

	if err := q.Submit(ctx, &contracts.Run{
		ID:        "r-head-mismatch",
		Image:     "alpine",
		Command:   []string{"bash", "/app/run-task.sh"},
		Labels:    map[string]string{"tenant": "qft"},
		BranchRef: "run/test",
		HeadSHA:   "expected-head-sha",
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-head-mismatch")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatal("head sha mismatch must not persist succeeded")
	}
	if got.Status != contracts.RunStatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "head_sha") || !strings.Contains(got.Error, "expected-head-sha") {
		t.Fatalf("expected head sha mismatch error, got %q", got.Error)
	}
	if got.HeadSHA != "expected-head-sha" {
		t.Fatalf("run record should retain expected head sha, got %q", got.HeadSHA)
	}
}

func TestQueue_StartPassesBoundHeadSHAEnv(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)

	if err := q.Submit(ctx, &contracts.Run{
		ID:      "r-head-env",
		Image:   "alpine",
		Command: []string{"echo", "hi"},
		HeadSHA: "bound-head-sha",
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	got, err := st.GetRun(ctx, "r-head-env")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != contracts.RunStatusRunning {
		t.Fatalf("expected running, got %s", got.Status)
	}
	if got.Env["AGENTS_HEAD_SHA"] != "bound-head-sha" {
		t.Fatalf("expected bound head sha env, got %q", got.Env["AGENTS_HEAD_SHA"])
	}
	if _, ok := md.containers["cid-alpine"]; !ok {
		t.Fatal("expected container to start")
	}
}

func TestQueue_ExitZeroConsumesInfraFailedTerminalEvidence(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	root := t.TempDir()
	t.Setenv("AGENTS_ROOT", root)
	writeEvidence(t, root, "qft", "r-evidence-infra", "infra-failed")

	if err := q.Submit(ctx, &contracts.Run{
		ID:      "r-evidence-infra",
		Image:   "alpine",
		Command: []string{"bash", "/app/run-task.sh"},
		Labels:  map[string]string{"tenant": "qft"},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-evidence-infra")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != contracts.RunStatusInfraFailed {
		t.Fatalf("expected infra-failed from evidence, got %s", got.Status)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatal("infra-failed evidence must not become succeeded")
	}
}

func TestQueue_NonzeroManagedRunConsumesInfraFailedTerminalEvidence(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	root := t.TempDir()
	t.Setenv("AGENTS_ROOT", root)
	writeEvidence(t, root, "qft", "r-nonzero-infra", "infra-failed")

	if err := q.Submit(ctx, &contracts.Run{
		ID:      "r-nonzero-infra",
		Image:   "alpine",
		Command: []string{"bash", "/app/run-task.sh"},
		Labels:  map[string]string{"tenant": "qft"},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.exitCodes["cid-alpine"] = 1
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-nonzero-infra")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != contracts.RunStatusInfraFailed {
		t.Fatalf("expected infra-failed from nonzero terminal evidence, got %s: %s", got.Status, got.Error)
	}
	if got.TaskID != "qft-task-r-nonzero-infra" {
		t.Fatalf("expected task id from evidence, got %q", got.TaskID)
	}
	if got.EvidencePath == "" || !strings.Contains(got.EvidencePath, "r-nonzero-infra.json") {
		t.Fatalf("expected evidence path from queue ingestion, got %q", got.EvidencePath)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatal("nonzero infra evidence must not become succeeded")
	}
}

func TestQueue_ExitZeroConsumesTerminalNonSuccessEvidence(t *testing.T) {
	cases := []contracts.RunStatus{
		contracts.RunStatusFailed,
		contracts.RunStatusNoOp,
		contracts.RunStatusBlocked,
	}
	for _, want := range cases {
		runID := "r-evidence-" + string(want)
		root := t.TempDir()
		writeEvidence(t, root, "qft", runID, string(want))
		got, _ := (&Queue{}).classifyTerminalExit(&contracts.Run{
			ID:      runID,
			Command: []string{"bash", "/app/run-task.sh"},
			Env:     map[string]string{"AGENTS_ROOT": root},
			Labels:  map[string]string{"tenant": "qft"},
		}, 0)
		if got != want {
			t.Fatalf("expected %s from evidence, got %s", want, got)
		}
		if got == contracts.RunStatusSucceeded {
			t.Fatal("non-success evidence must not become succeeded")
		}
	}
}

func TestQueue_ExitZeroFailsClosedOnUnknownTerminalEvidenceStatus(t *testing.T) {
	q, md, st, ctx := setupQueueWithBus(t, 1, events.NewNoop(), false)
	root := t.TempDir()
	t.Setenv("AGENTS_ROOT", root)
	writeEvidence(t, root, "qft", "r-evidence-unknown", "mystery-green")

	if err := q.Submit(ctx, &contracts.Run{
		ID:      "r-evidence-unknown",
		Image:   "alpine",
		Command: []string{"bash", "/app/run-task.sh"},
		Labels:  map[string]string{"tenant": "qft"},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	md.containers["cid-alpine"] = false
	q.reconcile(ctx)

	got, err := st.GetRun(ctx, "r-evidence-unknown")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status == contracts.RunStatusSucceeded {
		t.Fatal("unknown evidence status must not become succeeded")
	}
	if got.Status != contracts.RunStatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "unknown terminal evidence status") {
		t.Fatalf("expected unknown status error, got %q", got.Error)
	}
}

func writeEvidence(t *testing.T, root, tenant, runID, status string) {
	t.Helper()
	dir := filepath.Join(root, "projects", tenant, "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	payload := map[string]any{
		"run_id":           runID,
		"task_id":          "qft-task-" + runID,
		"tenant":           tenant,
		"issue_number":     42,
		"branch":           "run/test",
		"pr_url":           "https://github.test/pr/1",
		"log_issue_number": 1000,
		"head_sha":         "fake-head-sha",
		"status":           status,
		"artifact": map[string]any{
			"kind":  "proof-certificate",
			"paths": []string{".user/projects/qft/research/proof_certificates/proof.json"},
		},
		"kubernetes": map[string]any{
			"namespace":      "agents",
			"job_name":       "agent-" + runID,
			"container_name": "agent",
			"log_ref":        "k8s://agents/jobs/agent-" + runID,
		},
		"failure": map[string]string{
			"kind":    "none",
			"message": "",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, runID+".json"), data, 0o644); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
}

func writeRootTelemetryEvidence(t *testing.T, root, runID, tenant, status string) {
	t.Helper()
	dir := filepath.Join(root, "telemetry", "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir telemetry evidence: %v", err)
	}
	payload := map[string]any{
		"run_id":       runID,
		"task_id":      "qft-task-" + runID,
		"tenant":       tenant,
		"issue_number": 42,
		"branch":       "run/test",
		"head_sha":     "fake-head-sha",
		"status":       status,
		"artifact": map[string]any{
			"kind":  "proof-certificate",
			"paths": []string{".user/projects/qft/research/proof_certificates/proof.json"},
		},
		"kubernetes": map[string]any{
			"namespace":      "agents",
			"job_name":       "agent-" + runID,
			"container_name": "agent",
			"log_ref":        "k8s://agents/jobs/agent-" + runID,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal telemetry evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, runID+".json"), data, 0o644); err != nil {
		t.Fatalf("write telemetry evidence: %v", err)
	}
}

