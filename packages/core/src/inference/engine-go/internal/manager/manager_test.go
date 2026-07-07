package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/store"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// ---------- Mocks ----------

type mockGitHub struct {
	mu               sync.Mutex
	issues           []Issue
	branches         map[string]bool
	prs              []string
	prCalls          []draftPRCall
	files            []fileWriteCall
	failBranches     map[string]bool
	failBranchPrefix string
	failCreatePR     bool
	failCreateIssue  bool
	failAddLabels    bool
	comments         map[int][]string
	labelsAdded      map[int][]string
	issueLabels      map[int][]string
	issuesCreated    []struct {
		Title  string
		Body   string
		Labels []string
		Num    int
	}
	nextIssueNum int
}

type draftPRCall struct {
	Title string
	Body  string
	Head  string
	Base  string
	URL   string
}

type fileWriteCall struct {
	Path    string
	Branch  string
	Message string
	Content string
}

func newMockGitHub() *mockGitHub {
	return &mockGitHub{
		branches:     make(map[string]bool),
		failBranches: make(map[string]bool),
		comments:     make(map[int][]string),
		labelsAdded:  make(map[int][]string),
		issueLabels:  make(map[int][]string),
		nextIssueNum: 1000,
	}
}

func (m *mockGitHub) ListOpenIssues(ctx context.Context) ([]Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Issue(nil), m.issues...), nil
}

func (m *mockGitHub) CreateBranch(ctx context.Context, branchName, from string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failBranches[branchName] {
		return fmt.Errorf("forced branch failure %s", branchName)
	}
	if m.failBranchPrefix != "" && strings.HasPrefix(branchName, m.failBranchPrefix) {
		return fmt.Errorf("forced branch failure %s", branchName)
	}
	m.branches[branchName] = true
	return nil
}

func (m *mockGitHub) CreateDraftPR(ctx context.Context, title, body, head, base string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCreatePR {
		return "", fmt.Errorf("forced draft PR failure")
	}
	if !m.branches[head] {
		return "", fmt.Errorf("head branch %q does not exist", head)
	}
	url := fmt.Sprintf("https://github.com/test/pr/%d", len(m.prs)+1)
	m.prs = append(m.prs, url)
	m.prCalls = append(m.prCalls, draftPRCall{Title: title, Body: body, Head: head, Base: base, URL: url})
	return url, nil
}

func (m *mockGitHub) CreateIssue(ctx context.Context, title, body string, labels []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCreateIssue {
		return 0, fmt.Errorf("forced issue creation failure")
	}
	num := m.nextIssueNum
	m.nextIssueNum++
	m.issuesCreated = append(m.issuesCreated, struct {
		Title  string
		Body   string
		Labels []string
		Num    int
	}{Title: title, Body: body, Labels: labels, Num: num})
	return num, nil
}

func (m *mockGitHub) CreateOrUpdateFile(ctx context.Context, path, branch, message, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.branches[branch] {
		return fmt.Errorf("branch %q does not exist", branch)
	}
	m.files = append(m.files, fileWriteCall{Path: path, Branch: branch, Message: message, Content: content})
	return nil
}

func (m *mockGitHub) AddComment(ctx context.Context, issueNumber int, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.comments[issueNumber] = append(m.comments[issueNumber], body)
	return nil
}

func (m *mockGitHub) AddLabels(ctx context.Context, issueNumber int, labels []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAddLabels {
		return fmt.Errorf("forced label failure")
	}
	m.labelsAdded[issueNumber] = append(m.labelsAdded[issueNumber], labels...)
	return nil
}

func (m *mockGitHub) GetIssueLabels(ctx context.Context, issueNumber int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.issueLabels[issueNumber]...), nil
}

type mockGateway struct {
	responses []string
	calls     int
}

func (m *mockGateway) ChatCompletion(ctx context.Context, modelRole string, messages []Message, allowCloud bool) (string, error) {
	if m.calls < len(m.responses) {
		r := m.responses[m.calls]
		m.calls++
		return r, nil
	}
	return "[]", nil
}

type mockDaemon struct {
	runs map[string]*contracts.Run
	mu   sync.Mutex
}

func newMockDaemon() *mockDaemon {
	return &mockDaemon{runs: make(map[string]*contracts.Run)}
}

func (m *mockDaemon) SubmitRun(ctx context.Context, req contracts.SubmitRunRequest) (*contracts.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := &contracts.Run{
		ID:        fmt.Sprintf("run-%d", len(m.runs)),
		Status:    contracts.RunStatusQueued,
		Image:     req.Image,
		Command:   req.Command,
		Env:       req.Env,
		Labels:    req.Labels,
		IssueRef:  req.IssueRef,
		BranchRef: req.BranchRef,
		PRRef:     req.PRRef,
		CreatedAt: time.Now().UTC(),
	}
	m.runs[run.ID] = run
	return run, nil
}

func (m *mockDaemon) GetRun(ctx context.Context, id string) (*contracts.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return r, nil
}

func setupManager(t *testing.T) (*Manager, *mockGitHub, *mockGateway, *mockDaemon, *StateStore, context.Context) {
	path := "test_manager_" + t.Name() + ".db"
	statePath := path + "_state"

	st, err := store.New(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	stateStore, err := NewStateStore(statePath)
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gw := &mockGateway{}
	dm := newMockDaemon()

	cfg := DefaultConfig()
	cfg.PollInterval = "1h" // don't tick during tests
	cfg.MaxConcurrentRuns = 4

	o := NewManager(cfg, log, gh, (*GatewayClient)(nil), (*DaemonClient)(nil), stateStore, broker, nil)
	// We need to inject mocks directly since GatewayClient and DaemonClient are concrete.
	// Use reflection or just accept we test at a higher level. Instead, test the tick logic
	// by overriding the fields after construction.
	o.github = gh
	o.gateway = (*GatewayClient)(nil) // will be replaced
	o.daemon = (*DaemonClient)(nil)   // will be replaced

	t.Cleanup(func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	})

	return o, gh, gw, dm, stateStore, context.Background()
}

// Test classifyLabels directly.
func TestClassifyLabels(t *testing.T) {
	cases := []struct {
		labels []string
		want   string
	}{
		{[]string{"PRD"}, "PRD"},
		{[]string{"prd"}, "PRD"},
		{[]string{"ADR", "docs"}, "ADR"},
		{[]string{"log"}, "log"},
		{[]string{"suggestion"}, "suggestion"},
		{[]string{"bug", "help wanted"}, ""},
		{[]string{}, ""},
	}
	for _, tc := range cases {
		got := classifyLabels(tc.labels)
		if got != tc.want {
			t.Fatalf("classifyLabels(%v) = %q, want %q", tc.labels, got, tc.want)
		}
	}
}

// Test slugify.
func TestSlugify(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Hello World", "hello-world"},
		{"  Spaces  ", "spaces"},
		{"A--B---C", "a-b-c"},
		{"", "task"},
		{"Special!@#Chars", "special-chars"},
	}
	for _, tc := range cases {
		got := slugify(tc.in)
		if got != tc.want {
			t.Fatalf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Test the full PRD flow with mocked GitHub, gateway, and daemon.
func TestManager_PRDFlow(t *testing.T) {
	path := "test_manager_prd.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 42, Title: "Build feature X", Body: "We need feature X.", Labels: []string{"PRD"}},
	}

	// Gateway returns two sub-tasks.
	gwResponse := `[
		{"title": "Implement core", "description": "Do the core work", "command": ["echo", "core"], "env": {"FOO": "bar"}},
		{"title": "Add tests", "description": "Write tests", "command": ["echo", "tests"], "env": {}}
	]`

	// Build a mock gateway server.
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": gwResponse}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer gwSrv.Close()

	// Build a mock daemon server.
	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/runs" {
			var req contracts.SubmitRunRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			run := contracts.Run{
				ID:        fmt.Sprintf("run-%d", time.Now().UnixNano()),
				Status:    contracts.RunStatusQueued,
				Image:     req.Image,
				Command:   req.Command,
				Env:       req.Env,
				Labels:    req.Labels,
				IssueRef:  req.IssueRef,
				BranchRef: req.BranchRef,
				CreatedAt: time.Now().UTC(),
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(run)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer dmSrv.Close()

	cfg := DefaultConfig()
	cfg.DaemonURL = dmSrv.URL
	cfg.GatewayURL = gwSrv.URL
	cfg.DefaultImage = "agents/test:latest"
	cfg.BaseBranch = "dev"

	gw := NewGatewayClient(gwSrv.URL)
	dm := NewDaemonClient(dmSrv.URL)

	o := NewManager(cfg, log, gh, gw, dm, stateStore, broker, nil)

	ctx := context.Background()
	if err := o.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Assert branch creations.
	gh.mu.Lock()
	if len(gh.branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(gh.branches))
	}
	prCount := len(gh.prs)
	issueCount := len(gh.issuesCreated)
	gh.mu.Unlock()

	if prCount != 2 {
		t.Fatalf("expected 2 draft PRs, got %d", prCount)
	}
	if issueCount != 2 {
		t.Fatalf("expected 2 log issues, got %d", issueCount)
	}

	// Assert processed.
	processed, _ := stateStore.IsProcessed(ctx, 42)
	if !processed {
		t.Fatal("expected issue 42 to be processed")
	}

	runs, _ := stateStore.ListRunsForIssue(ctx, 42)
	if len(runs) != 2 {
		t.Fatalf("expected 2 manager runs, got %d", len(runs))
	}
	for _, run := range runs {
		if !strings.HasPrefix(run.PRURL, "https://github.com/test/pr/") {
			t.Fatalf("expected real PR URL, got %q", run.PRURL)
		}
		if run.LogIssueNumber == 0 {
			t.Fatalf("expected log issue number for run %s", run.RunID)
		}
	}
}

func TestManager_PRDSpawnFailureDoesNotMarkProcessed(t *testing.T) {
	path := "test_manager_prd_failure.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 43, Title: "Build brittle feature", Body: "Needs retry-safe spawning.", Labels: []string{"PRD"}},
	}
	gh.failBranchPrefix = "run/"

	gwResponse := `[
		{"title": "will fail branch", "description": "Do work", "command": [], "env": {}}
	]`
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": gwResponse}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer gwSrv.Close()

	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("daemon should not be called when branch creation fails")
	}))
	defer dmSrv.Close()

	cfg := DefaultConfig()
	cfg.GatewayURL = gwSrv.URL
	cfg.DaemonURL = dmSrv.URL
	cfg.DefaultImage = "agents/test:latest"
	cfg.BaseBranch = "dev"

	o := NewManager(cfg, log, gh, NewGatewayClient(gwSrv.URL), NewDaemonClient(dmSrv.URL), stateStore, broker, nil)

	if err := o.handlePRD(context.Background(), gh.issues[0]); err == nil {
		t.Fatal("expected handlePRD to return spawn failure")
	}
	processed, _ := stateStore.IsProcessed(context.Background(), 43)
	if processed {
		t.Fatal("PRD issue was marked processed despite spawn failure")
	}
}

func TestManager_LogIssueFailureDoesNotMarkProcessed(t *testing.T) {
	path := "test_manager_log_issue_failure.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 44, Title: "[qft] Build audited feature", Body: "Needs a durable log issue.\n\nTask ID: qft-task-44", Labels: []string{"PRD", "qft"}},
	}
	gh.failCreateIssue = true

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/runs" {
			var req contracts.SubmitRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(contracts.Run{
				ID:        "run-log-required",
				Status:    contracts.RunStatusQueued,
				Image:     req.Image,
				IssueRef:  req.IssueRef,
				BranchRef: req.BranchRef,
				CreatedAt: time.Now().UTC(),
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer daemon.Close()

	cfg := DefaultConfig()
	cfg.DaemonURL = daemon.URL
	cfg.DefaultImage = "agents/test:latest"
	o := NewManager(cfg, log, gh, nil, NewDaemonClient(daemon.URL), stateStore, broker, nil)
	ctx := context.Background()

	if err := o.handlePRD(ctx, gh.issues[0]); err == nil {
		t.Fatal("expected log issue failure to fail PRD handling")
	}
	processed, err := stateStore.IsProcessed(ctx, 44)
	if err != nil {
		t.Fatalf("processed state: %v", err)
	}
	if processed {
		t.Fatal("PRD issue was marked processed despite missing log issue")
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 44)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("run metadata must not be recorded without the required log issue: %#v", runs)
	}
}

func TestExtractTenant(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   string
	}{
		{"prd only is generic", []string{"prd"}, ""},
		{"no labels is generic", nil, ""},
		{"classification labels only", []string{"PRD", "log"}, ""},
		{"tenant label wins", []string{"prd", "qft"}, "qft"},
		{"tenant label lowercased", []string{"PRD", "QFT"}, "qft"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractTenant(tc.labels); got != tc.want {
				t.Fatalf("extractTenant(%v) = %q, want %q", tc.labels, got, tc.want)
			}
		})
	}
}

func TestManager_DraftPRFailureDefersToReconcileAndRecordsRun(t *testing.T) {
	path := "test_manager_draft_pr_failure.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 45, Title: "[qft] Build PR-bound feature", Body: "Needs a durable PR.\n\nTask ID: qft-task-45", Labels: []string{"PRD", "qft"}},
	}
	gh.failCreatePR = true

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/runs" {
			var req contracts.SubmitRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(contracts.Run{
				ID:        "run-pr-required",
				Status:    contracts.RunStatusQueued,
				Image:     req.Image,
				IssueRef:  req.IssueRef,
				BranchRef: req.BranchRef,
				CreatedAt: time.Now().UTC(),
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer daemon.Close()

	cfg := DefaultConfig()
	cfg.DaemonURL = daemon.URL
	cfg.DefaultImage = "agents/test:latest"
	o := NewManager(cfg, log, gh, nil, NewDaemonClient(daemon.URL), stateStore, broker, nil)
	ctx := context.Background()

	// A freshly created run branch has no commits ahead of base, so GitHub rejects
	// the draft PR with 422. This must NOT fail the spawn: the run is still recorded
	// and the PRD marked processed, while reconcilePRs (ListRunsNeedingPR) creates
	// the draft PR on a later tick once the run has pushed. Failing here would skip
	// RecordRun and re-spawn the run every tick (a run-leak loop).
	err := o.handlePRD(ctx, gh.issues[0])
	if err != nil {
		t.Fatalf("draft PR failure must not fail PRD handling (defers to reconcile): %v", err)
	}
	processed, err := stateStore.IsProcessed(ctx, 45)
	if err != nil {
		t.Fatalf("processed state: %v", err)
	}
	if !processed {
		t.Fatal("PRD issue must be marked processed even when the draft PR is deferred")
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 45)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("the run must be recorded even though the draft PR was deferred: %#v", runs)
	}
	if runs[0].PRURL != "" {
		t.Fatalf("deferred run must have an empty PR URL, got %q", runs[0].PRURL)
	}
	// The recorded run must be eligible for reconcilePRs to create the PR later.
	needing, err := stateStore.ListRunsNeedingPR(ctx)
	if err != nil {
		t.Fatalf("list runs needing PR: %v", err)
	}
	found := false
	for _, r := range needing {
		if r.RunID == runs[0].RunID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("deferred run must appear in ListRunsNeedingPR so reconcile can create the PR; got %#v", needing)
	}
}

func TestManager_QFTBridgePRDBypassesDecompositionAndFailsOverDaemon(t *testing.T) {
	path := "test_manager_qft_bridge.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	issueBody := "Improve the post-K8 strategy without touching frozen evaluator files.\n\n---\n*Task ID: qft-task-123 | Tenant: qft | Priority: high*"
	gh.issues = []Issue{
		{Number: 77, Title: "[qft] Advance post-K8 proof", Body: issueBody, Labels: []string{"PRD", "qft"}},
	}

	gwCalls := 0
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gwCalls++
		http.Error(w, "qft bridge issues must not decompose", http.StatusInternalServerError)
	}))
	defer gwSrv.Close()

	var submitted []contracts.SubmitRunRequest
	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not leader", http.StatusServiceUnavailable)
	}))
	defer standby.Close()
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/runs" {
			var req contracts.SubmitRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			submitted = append(submitted, req)
			run := contracts.Run{
				ID:        "run-qft",
				Status:    contracts.RunStatusQueued,
				Image:     req.Image,
				Command:   req.Command,
				Env:       req.Env,
				Labels:    req.Labels,
				IssueRef:  req.IssueRef,
				BranchRef: req.BranchRef,
				CreatedAt: time.Now().UTC(),
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(run)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer leader.Close()

	cfg := DefaultConfig()
	cfg.DaemonURLs = []string{standby.URL, leader.URL}
	cfg.GatewayURL = gwSrv.URL
	cfg.DefaultImage = "agents/test:latest"
	cfg.BaseBranch = "dev"
	agentsRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(agentsRoot, "projects", "qft"), 0o755); err != nil {
		t.Fatalf("mkdir qft runtime root: %v", err)
	}
	t.Setenv("AGENTS_ROOT", agentsRoot)

	o := NewManager(cfg, log, gh, NewGatewayClient(gwSrv.URL), NewDaemonClientMulti(cfg.DaemonURLs), stateStore, broker, nil)
	if err := o.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if gwCalls != 0 {
		t.Fatalf("gateway was called %d times for bridge QFT issue", gwCalls)
	}
	if len(submitted) != 1 {
		t.Fatalf("expected one submitted QFT run, got %d", len(submitted))
	}
	req := submitted[0]
	if got := req.Env["AGENTS_TENANT"]; got != "qft" {
		t.Fatalf("AGENTS_TENANT = %q, want qft", got)
	}
	if got := req.Env["AGENTS_TASK_ID"]; got != "qft-task-123" {
		t.Fatalf("AGENTS_TASK_ID = %q, want qft-task-123", got)
	}
	if got := req.Env["AGENTS_RUN_ID"]; got == "" {
		t.Fatal("AGENTS_RUN_ID was not set")
	}
	if got := req.Env["AGENTS_ROOT"]; got != agentsRoot {
		t.Fatalf("AGENTS_ROOT = %q, want %q", got, agentsRoot)
	}
	if got := req.Env["AGENTS_TASK"]; !strings.Contains(got, "Task ID: qft-task-123") || !strings.Contains(got, issueBody) {
		t.Fatalf("AGENTS_TASK did not preserve original bridge task body: %q", got)
	}
	if got := strings.Join(req.Command, " "); got != "bash /app/run-task.sh" {
		t.Fatalf("command = %q", got)
	}
	if got := req.Labels["tenant"]; got != "qft" {
		t.Fatalf("tenant label = %q, want qft", got)
	}
	if !strings.HasPrefix(req.BranchRef, "run/") {
		t.Fatalf("branch ref = %q, want run/*", req.BranchRef)
	}
	runs, err := stateStore.ListRunsForIssue(context.Background(), 77)
	if err != nil {
		t.Fatalf("list QFT runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one recorded run, got %#v", runs)
	}
	if runs[0].TaskID != "qft-task-123" {
		t.Fatalf("recorded task id = %q, want qft-task-123", runs[0].TaskID)
	}
	wantEvidence := filepath.Join(agentsRoot, "projects", "qft", "runs", "run-qft.json")
	if runs[0].EvidencePath != wantEvidence {
		t.Fatalf("evidence path = %q, want %q", runs[0].EvidencePath, wantEvidence)
	}
}

func TestManager_NonQFTDoesNotInventTaskID(t *testing.T) {
	iss := Issue{Number: 5, Title: "Normal PRD", Body: "Task ID: qft-task-nope", Labels: []string{"PRD"}}
	if got := bridgeQFTTaskID(iss); got != "" {
		t.Fatalf("non-QFT issue produced task id %q", got)
	}
}

func TestManager_ADRIssueWritesProposedADRFileBeforeDraftPR(t *testing.T) {
	o, gh, _, _, stateStore, ctx := setupManager(t)
	gh.issues = []Issue{
		{Number: 88, Title: "Adopt safer deploy contract", Body: "Deploys must be release-tagged.", Labels: []string{"ADR"}},
	}

	if err := o.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.files) != 1 {
		t.Fatalf("expected one ADR file write, got %d", len(gh.files))
	}
	file := gh.files[0]
	if file.Path != ".agents/context/adr-proposals/ADR-0088-adopt-safer-deploy-contract.md" {
		t.Fatalf("ADR path = %q", file.Path)
	}
	if !strings.Contains(file.Content, "Status: proposed") || !strings.Contains(file.Content, "Deploys must be release-tagged.") {
		t.Fatalf("ADR content missing proposal fields: %s", file.Content)
	}
	if len(gh.prCalls) != 1 {
		t.Fatalf("expected one draft PR, got %d", len(gh.prCalls))
	}
	if !strings.Contains(gh.prCalls[0].Body, file.Path) || !strings.Contains(gh.prCalls[0].Body, "human approval required") {
		t.Fatalf("draft PR body missing ADR path or approval language: %s", gh.prCalls[0].Body)
	}
	processed, _ := stateStore.IsProcessed(ctx, 88)
	if !processed {
		t.Fatal("expected ADR issue to be processed")
	}
}

func TestManager_RunOnceFiltersIssue(t *testing.T) {
	path := "test_manager_run_once.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 77, Title: "[qft] Target proof", Body: "Task ID: qft-task-77\nTenant: qft", Labels: []string{"PRD", "qft"}},
		{Number: 78, Title: "[qft] Other proof", Body: "Task ID: qft-task-78\nTenant: qft", Labels: []string{"PRD", "qft"}},
	}

	var submitted []contracts.SubmitRunRequest
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/runs" {
			var req contracts.SubmitRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			submitted = append(submitted, req)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(contracts.Run{
				ID:        fmt.Sprintf("run-%d", len(submitted)),
				Status:    contracts.RunStatusQueued,
				Image:     req.Image,
				IssueRef:  req.IssueRef,
				BranchRef: req.BranchRef,
				CreatedAt: time.Now().UTC(),
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer daemon.Close()

	cfg := DefaultConfig()
	cfg.DaemonURL = daemon.URL
	cfg.DaemonURLs = []string{daemon.URL}
	cfg.DefaultImage = "agents/test:latest"
	o := NewManager(cfg, log, gh, nil, NewDaemonClient(daemon.URL), stateStore, broker, nil)

	if err := o.RunOnce(context.Background(), 77); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if len(submitted) != 1 {
		t.Fatalf("expected one submitted run, got %d", len(submitted))
	}
	if submitted[0].IssueRef != "#77" {
		t.Fatalf("submitted wrong issue ref: %q", submitted[0].IssueRef)
	}
	processed77, err := stateStore.IsProcessed(context.Background(), 77)
	if err != nil {
		t.Fatalf("processed 77: %v", err)
	}
	processed78, err := stateStore.IsProcessed(context.Background(), 78)
	if err != nil {
		t.Fatalf("processed 78: %v", err)
	}
	if !processed77 || processed78 {
		t.Fatalf("unexpected processed state: issue77=%v issue78=%v", processed77, processed78)
	}
}

func TestManager_ReconcilePRsCreatesDraftPRForExistingBranch(t *testing.T) {
	path := "test_manager_reconcile.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	cfg := DefaultConfig()
	cfg.BaseBranch = "dev"
	o := NewManager(cfg, log, gh, nil, nil, stateStore, nil, nil)

	ctx := context.Background()
	branch := "run/run-1234/implement-core"
	if err := stateStore.RecordRun(ctx, "run-1234", 42, 1, "Implement core", branch, "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}
	gh.branches[branch] = true
	if err := o.reconcilePRs(ctx); err != nil {
		t.Fatalf("reconcile PRs: %v", err)
	}

	gh.mu.Lock()
	if len(gh.prCalls) != 1 {
		t.Fatalf("expected 1 draft PR call, got %d", len(gh.prCalls))
	}
	call := gh.prCalls[0]
	gh.mu.Unlock()
	if call.Head != branch {
		t.Fatalf("expected draft PR head %q, got %q", branch, call.Head)
	}
	if call.Base != "dev" {
		t.Fatalf("expected draft PR base dev, got %q", call.Base)
	}

	runs, err := stateStore.ListRunsForIssue(ctx, 42)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].PRURL != call.URL {
		t.Fatalf("expected stored PR URL %q, got %q", call.URL, runs[0].PRURL)
	}
}

func TestManager_ReconcilePRsDoesNotRecreateMissingRunBranch(t *testing.T) {
	path := "test_manager_reconcile_missing_branch.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	cfg := DefaultConfig()
	cfg.BaseBranch = "dev"
	o := NewManager(cfg, log, gh, nil, nil, stateStore, nil, nil)

	ctx := context.Background()
	branch := "run/stale/deleted-branch"
	if err := stateStore.RecordRun(ctx, "stale-run", 42, 1, "Deleted branch", branch, "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}
	if err := o.reconcilePRs(ctx); err != nil {
		t.Fatalf("reconcile PRs: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if gh.branches[branch] {
		t.Fatalf("reconcile recreated stale branch %q", branch)
	}
	if len(gh.prCalls) != 0 {
		t.Fatalf("expected no draft PR for missing branch, got %d", len(gh.prCalls))
	}
}

func TestManager_ReconcileRunResultsPostsEvidence(t *testing.T) {
	path := "test_manager_reconcile_results.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	runID := "run-terminal"
	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/runs/"+runID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		run := contracts.Run{
			ID:          runID,
			Status:      contracts.RunStatusSucceeded,
			Image:       "agents/test:latest",
			ExternalURL: "https://ci.example/runs/run-terminal",
			Logs:        "tests passed",
			CreatedAt:   time.Now().UTC(),
		}
		_ = json.NewEncoder(w).Encode(run)
	}))
	defer dmSrv.Close()

	ctx := context.Background()
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/terminal", "https://github.com/test/pr/1", 1000); err != nil {
		t.Fatalf("record run: %v", err)
	}
	if err := stateStore.SetRunTaskID(ctx, runID, "qft-task-result"); err != nil {
		t.Fatalf("set task id: %v", err)
	}
	if err := stateStore.SetRunEvidencePath(ctx, runID, "/tmp/agents/qft/runs/run-terminal.json"); err != nil {
		t.Fatalf("set evidence path: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	o := NewManager(DefaultConfig(), log, gh, nil, NewDaemonClient(dmSrv.URL), stateStore, ops.NewBroker(st), nil)

	if err := o.reconcileRunResults(ctx); err != nil {
		t.Fatalf("reconcile run results: %v", err)
	}

	gh.mu.Lock()
	parentComments := append([]string(nil), gh.comments[42]...)
	logComments := append([]string(nil), gh.comments[1000]...)
	labels := append([]string(nil), gh.labelsAdded[42]...)
	gh.mu.Unlock()
	if len(parentComments) != 1 || !strings.Contains(parentComments[0], "tests passed") || !strings.Contains(parentComments[0], "https://github.com/test/pr/1") || !strings.Contains(parentComments[0], "qft-task-result") || !strings.Contains(parentComments[0], "/tmp/agents/qft/runs/run-terminal.json") {
		t.Fatalf("parent comment missing run evidence: %#v", parentComments)
	}
	if len(logComments) != 1 || !strings.Contains(logComments[0], "run-terminal") {
		t.Fatalf("log issue comment missing run evidence: %#v", logComments)
	}
	if len(labels) != 1 || labels[0] != "run-succeeded" {
		t.Fatalf("expected run-succeeded label, got %#v", labels)
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 42)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].LastStatus != contracts.RunStatusSucceeded || runs[0].ResultIngestedAt == nil {
		t.Fatalf("run result not marked ingested: %#v", runs)
	}
	remaining, err := stateStore.ListRunsNeedingResult(ctx)
	if err != nil {
		t.Fatalf("list runs needing result: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no runs needing result, got %d", len(remaining))
	}
}

func TestManager_ReconcileRunResultsMissingEvidenceDoesNotGreenwash(t *testing.T) {
	path := "test_manager_missing_evidence.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	runID := "run-missing-evidence"
	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := contracts.Run{
			ID:        runID,
			Status:    contracts.RunStatusFailed,
			Image:     "agents/test:latest",
			Error:     "missing terminal evidence: open /tmp/missing.json: no such file",
			CreatedAt: time.Now().UTC(),
		}
		_ = json.NewEncoder(w).Encode(run)
	}))
	defer dmSrv.Close()

	ctx := context.Background()
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/missing-evidence", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	o := NewManager(DefaultConfig(), log, gh, nil, NewDaemonClient(dmSrv.URL), stateStore, ops.NewBroker(st), nil)

	if err := o.reconcileRunResults(ctx); err != nil {
		t.Fatalf("reconcile run results: %v", err)
	}

	gh.mu.Lock()
	labels := append([]string(nil), gh.labelsAdded[42]...)
	comments := append([]string(nil), gh.comments[42]...)
	gh.mu.Unlock()
	if len(labels) != 1 || labels[0] != "run-failed" {
		t.Fatalf("expected run-failed label, got %#v", labels)
	}
	if labels[0] == "run-succeeded" {
		t.Fatal("missing evidence must not be labeled as success")
	}
	if len(comments) != 1 || !strings.Contains(comments[0], "missing terminal evidence") {
		t.Fatalf("expected missing evidence comment, got %#v", comments)
	}
}

func TestManager_ReconcileRunResultsPersistsDaemonTaskAndEvidence(t *testing.T) {
	path := "test_manager_daemon_evidence.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	runID := "run-daemon-evidence"
	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := contracts.Run{
			ID:           runID,
			Status:       contracts.RunStatusFailed,
			Image:        "agents/test:latest",
			TaskID:       "qft-task-daemon",
			EvidencePath: "/tmp/evidence/run-daemon-evidence.json",
			Error:        "tenant failure",
			CreatedAt:    time.Now().UTC(),
		}
		_ = json.NewEncoder(w).Encode(run)
	}))
	defer dmSrv.Close()

	ctx := context.Background()
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/daemon-evidence", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	o := NewManager(DefaultConfig(), log, gh, nil, NewDaemonClient(dmSrv.URL), stateStore, ops.NewBroker(st), nil)
	if err := o.reconcileRunResults(ctx); err != nil {
		t.Fatalf("reconcile run results: %v", err)
	}

	run, err := stateStore.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.TaskID != "qft-task-daemon" || run.EvidencePath != "/tmp/evidence/run-daemon-evidence.json" {
		t.Fatalf("daemon evidence fields were not persisted: %#v", run)
	}
	gh.mu.Lock()
	comments := append([]string(nil), gh.comments[42]...)
	gh.mu.Unlock()
	if len(comments) != 1 || !strings.Contains(comments[0], "qft-task-daemon") || !strings.Contains(comments[0], "/tmp/evidence/run-daemon-evidence.json") {
		t.Fatalf("result comment missing daemon evidence fields: %#v", comments)
	}
}

func TestManager_ReconcileRunResultsPostsInfraFailedAsNonSuccess(t *testing.T) {
	path := "test_manager_reconcile_infra_failed.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	runID := "run-infra-failed"
	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := contracts.Run{
			ID:        runID,
			Status:    contracts.RunStatusInfraFailed,
			Image:     "agents/test:latest",
			Error:     "event bus publish failed",
			CreatedAt: time.Now().UTC(),
		}
		_ = json.NewEncoder(w).Encode(run)
	}))
	defer dmSrv.Close()

	ctx := context.Background()
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/infra-failed", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	o := NewManager(DefaultConfig(), log, gh, nil, NewDaemonClient(dmSrv.URL), stateStore, ops.NewBroker(st), nil)

	if err := o.reconcileRunResults(ctx); err != nil {
		t.Fatalf("reconcile run results: %v", err)
	}

	gh.mu.Lock()
	labels := append([]string(nil), gh.labelsAdded[42]...)
	comments := append([]string(nil), gh.comments[42]...)
	gh.mu.Unlock()
	if len(labels) != 1 || labels[0] != "run-infra-failed" {
		t.Fatalf("expected run-infra-failed label, got %#v", labels)
	}
	if len(comments) != 1 || !strings.Contains(comments[0], "event bus publish failed") {
		t.Fatalf("expected infra failure comment, got %#v", comments)
	}
	if labels[0] == "run-succeeded" {
		t.Fatal("infra-failed must not be labeled as success")
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 42)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].LastStatus != contracts.RunStatusInfraFailed || runs[0].ResultIngestedAt == nil {
		t.Fatalf("infra-failed run result not marked ingested: %#v", runs)
	}
}

func TestManager_ReconcileRunResultsDoesNotIngestUnknownStatus(t *testing.T) {
	path := "test_manager_reconcile_unknown_status.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	runID := "run-unknown"
	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := contracts.Run{
			ID:        runID,
			Status:    contracts.RunStatus("mystery-green"),
			Image:     "agents/test:latest",
			CreatedAt: time.Now().UTC(),
		}
		_ = json.NewEncoder(w).Encode(run)
	}))
	defer dmSrv.Close()

	ctx := context.Background()
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/unknown", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	o := NewManager(DefaultConfig(), log, gh, nil, NewDaemonClient(dmSrv.URL), stateStore, ops.NewBroker(st), nil)

	if err := o.reconcileRunResults(ctx); err != nil {
		t.Fatalf("reconcile run results: %v", err)
	}

	gh.mu.Lock()
	labels := append([]string(nil), gh.labelsAdded[42]...)
	comments := append([]string(nil), gh.comments[42]...)
	gh.mu.Unlock()
	if len(labels) != 0 || len(comments) != 0 {
		t.Fatalf("unknown status must not post labels/comments, got labels=%#v comments=%#v", labels, comments)
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 42)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].LastStatus != contracts.RunStatus("mystery-green") || runs[0].ResultIngestedAt != nil {
		t.Fatalf("unknown status should be tracked but not ingested: %#v", runs)
	}
}

func TestManager_ReconcileRunResultsRetriesWhenPostingFails(t *testing.T) {
	path := "test_manager_reconcile_results_retry.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	runID := "run-terminal"
	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := contracts.Run{
			ID:        runID,
			Status:    contracts.RunStatusFailed,
			Image:     "agents/test:latest",
			Error:     "candidate failed",
			CreatedAt: time.Now().UTC(),
		}
		_ = json.NewEncoder(w).Encode(run)
	}))
	defer dmSrv.Close()

	ctx := context.Background()
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/terminal", "", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	gh.failAddLabels = true
	o := NewManager(DefaultConfig(), log, gh, nil, NewDaemonClient(dmSrv.URL), stateStore, ops.NewBroker(st), nil)

	if err := o.reconcileRunResults(ctx); err != nil {
		t.Fatalf("reconcile run results: %v", err)
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 42)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ResultIngestedAt != nil {
		t.Fatalf("failed posting should remain retryable: %#v", runs)
	}
}

func TestManager_ReconcileRunReviewsMirrorsVerdict(t *testing.T) {
	path := "test_manager_reconcile_reviews.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	ctx := context.Background()
	runID := "run-reviewed"
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/reviewed", "https://github.com/test/repo/pull/12", 1000); err != nil {
		t.Fatalf("record run: %v", err)
	}
	if err := stateStore.SetRunStatus(ctx, runID, contracts.RunStatusSucceeded, true); err != nil {
		t.Fatalf("set run status: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	gh.issueLabels[12] = []string{"run-reviewed"}
	o := NewManager(DefaultConfig(), log, gh, nil, nil, stateStore, ops.NewBroker(st), nil)

	if err := o.reconcileRunReviews(ctx); err != nil {
		t.Fatalf("reconcile run reviews: %v", err)
	}

	gh.mu.Lock()
	parentComments := append([]string(nil), gh.comments[42]...)
	logComments := append([]string(nil), gh.comments[1000]...)
	labels := append([]string(nil), gh.labelsAdded[42]...)
	gh.mu.Unlock()
	if len(parentComments) != 1 || !strings.Contains(parentComments[0], "run-reviewed") || !strings.Contains(parentComments[0], "#12") {
		t.Fatalf("parent comment missing review evidence: %#v", parentComments)
	}
	if len(logComments) != 1 || !strings.Contains(logComments[0], "run-reviewed") {
		t.Fatalf("log issue comment missing review evidence: %#v", logComments)
	}
	if len(labels) != 1 || labels[0] != "run-reviewed" {
		t.Fatalf("expected run-reviewed label, got %#v", labels)
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 42)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ReviewStatus != "run-reviewed" || runs[0].ReviewIngestedAt == nil {
		t.Fatalf("run review not marked ingested: %#v", runs)
	}
	remaining, err := stateStore.ListRunsNeedingReview(ctx)
	if err != nil {
		t.Fatalf("list runs needing review: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no runs needing review, got %d", len(remaining))
	}
}

func TestManager_ReconcileRunReviewsWaitsForVerdict(t *testing.T) {
	path := "test_manager_reconcile_reviews_pending.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	ctx := context.Background()
	runID := "run-pending-review"
	if err := stateStore.RecordRun(ctx, runID, 42, 0, "Implement core", "run/pending-review", "https://github.com/test/repo/pull/12", 0); err != nil {
		t.Fatalf("record run: %v", err)
	}
	if err := stateStore.SetRunStatus(ctx, runID, contracts.RunStatusSucceeded, true); err != nil {
		t.Fatalf("set run status: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gh := newMockGitHub()
	gh.issueLabels[12] = []string{"bug"}
	o := NewManager(DefaultConfig(), log, gh, nil, nil, stateStore, ops.NewBroker(st), nil)

	if err := o.reconcileRunReviews(ctx); err != nil {
		t.Fatalf("reconcile run reviews: %v", err)
	}
	runs, err := stateStore.ListRunsForIssue(ctx, 42)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ReviewIngestedAt != nil {
		t.Fatalf("pending review should remain retryable: %#v", runs)
	}
}

// Test ADR flow creates a branch and draft PR but does not self-merge.
func TestManager_ADRFlow(t *testing.T) {
	path := "test_manager_adr.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 7, Title: "Use NATS for events", Body: "Decision: adopt NATS.", Labels: []string{"ADR"}},
	}

	cfg := DefaultConfig()
	o := NewManager(cfg, log, gh, nil, nil, stateStore, broker, nil)

	ctx := context.Background()
	if err := o.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	gh.mu.Lock()
	branchCount := len(gh.branches)
	prCount := len(gh.prs)
	gh.mu.Unlock()

	if branchCount != 1 {
		t.Fatalf("expected 1 branch, got %d", branchCount)
	}
	if prCount != 1 {
		t.Fatalf("expected 1 draft PR, got %d", prCount)
	}

	// PR body should contain human-approval warning.
	gh.mu.Lock()
	if !strings.Contains(gh.prs[0], "github.com/test/pr/") {
		t.Fatalf("unexpected pr url: %s", gh.prs[0])
	}
	gh.mu.Unlock()
}

// Test idempotency: processing the same issue twice should not duplicate side effects.
func TestManager_Idempotency(t *testing.T) {
	path := "test_manager_idem.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 99, Title: "Refactor store", Body: "Clean up store.", Labels: []string{"ADR"}},
	}

	cfg := DefaultConfig()
	o := NewManager(cfg, log, gh, nil, nil, stateStore, broker, nil)

	ctx := context.Background()
	if err := o.tick(ctx); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if err := o.tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	gh.mu.Lock()
	branchCount := len(gh.branches)
	prCount := len(gh.prs)
	gh.mu.Unlock()

	if branchCount != 1 {
		t.Fatalf("expected 1 branch (idempotent), got %d", branchCount)
	}
	if prCount != 1 {
		t.Fatalf("expected 1 PR (idempotent), got %d", prCount)
	}
}

// Test suggestion flow adds a comment and marks processed.
func TestManager_SuggestionFlow(t *testing.T) {
	path := "test_manager_sugg.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 3, Title: "Idea: dark mode", Body: "Would be nice.", Labels: []string{"suggestion"}},
	}

	cfg := DefaultConfig()
	o := NewManager(cfg, log, gh, nil, nil, stateStore, broker, nil)

	ctx := context.Background()
	if err := o.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	gh.mu.Lock()
	comments := gh.comments[3]
	gh.mu.Unlock()
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if !strings.Contains(comments[0], "Acknowledged suggestion") {
		t.Fatalf("unexpected comment: %s", comments[0])
	}
}

// Test health endpoint.
func TestManager_Health(t *testing.T) {
	path := "test_manager_health.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.NodeID = "manager-test"
	cfg.DaemonURLs = []string{"http://daemon-a:8080", "http://daemon-b:8080"}
	cfg.DaemonURL = cfg.DaemonURLs[0]
	o := NewManager(cfg, log, newMockGitHub(), nil, nil, stateStore, broker, nil)

	ctx := context.Background()
	if err := o.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer o.Stop(context.Background())

	// Wait for server to be ready.
	time.Sleep(50 * time.Millisecond)

	req := httptest.NewRequest("GET", "http://"+o.httpSrv.Addr+"/health", nil)
	w := httptest.NewRecorder()
	o.httpSrv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "healthy" {
		t.Fatalf("expected healthy, got %v", resp["status"])
	}
	if resp["node_id"] != "manager-test" || resp["leader"] != true {
		t.Fatalf("expected manager identity, got %#v", resp)
	}
	urls, ok := resp["daemon_urls"].([]any)
	if !ok || len(urls) != 2 {
		t.Fatalf("expected daemon_urls health field, got %#v", resp["daemon_urls"])
	}
}

// Test max runs truncation.
func TestManager_MaxRunsTruncation(t *testing.T) {
	path := "test_manager_max.db"
	statePath := path + "_state"
	st, _ := store.New(path)
	stateStore, _ := NewStateStore(statePath)
	defer func() {
		st.Close()
		stateStore.Close()
		os.Remove(path)
		os.Remove(statePath)
	}()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	broker := ops.NewBroker(st)
	gh := newMockGitHub()
	gh.issues = []Issue{
		{Number: 50, Title: "Mega PRD", Body: "Many tasks.", Labels: []string{"PRD"}},
	}

	// Gateway returns 10 sub-tasks.
	tasks := make([]SubTask, 10)
	for i := range tasks {
		tasks[i] = SubTask{Title: fmt.Sprintf("Task %d", i), Command: []string{"echo", fmt.Sprintf("%d", i)}}
	}
	raw, _ := json.Marshal(tasks)

	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": string(raw)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer gwSrv.Close()

	dmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/runs" {
			var req contracts.SubmitRunRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			run := contracts.Run{
				ID:        fmt.Sprintf("run-%d", time.Now().UnixNano()),
				Status:    contracts.RunStatusQueued,
				Image:     req.Image,
				Command:   req.Command,
				CreatedAt: time.Now().UTC(),
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(run)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer dmSrv.Close()

	cfg := DefaultConfig()
	cfg.DaemonURL = dmSrv.URL
	cfg.GatewayURL = gwSrv.URL
	cfg.MaxConcurrentRuns = 2

	gw := NewGatewayClient(gwSrv.URL)
	dm := NewDaemonClient(dmSrv.URL)
	o := NewManager(cfg, log, gh, gw, dm, stateStore, broker, nil)

	ctx := context.Background()
	if err := o.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	runs, _ := stateStore.ListRunsForIssue(ctx, 50)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs (truncated), got %d", len(runs))
	}
}

// Test ParseSubTasks handles markdown fences.
func TestParseSubTasks(t *testing.T) {
	raw := "```json\n[{\"title\":\"T1\",\"description\":\"D1\",\"command\":[\"echo\"],\"env\":{}}]\n```"
	tasks, err := ParseSubTasks(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "T1" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
}

