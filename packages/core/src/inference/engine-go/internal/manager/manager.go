package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ghauth"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// Manager is the single global issue watcher and run dispatcher.
type Manager struct {
	cfg           Config
	log           *slog.Logger
	github        GitHubClient
	gateway       *GatewayClient
	daemon        *DaemonClient
	store         *StateStore
	broker        *ops.Broker
	tokenProvider ghauth.TokenProvider
	ticker        *time.Ticker
	stopCh        chan struct{}
	wg            sync.WaitGroup
	httpSrv       *http.Server
	isLeader      func() bool
}

// NewManager wires up dependencies.
func NewManager(cfg Config, log *slog.Logger, gh GitHubClient, gw *GatewayClient, daemon *DaemonClient, store *StateStore, broker *ops.Broker, tokenProvider ghauth.TokenProvider) *Manager {
	return &Manager{
		cfg:           cfg,
		log:           log.With("component", "manager"),
		github:        gh,
		gateway:       gw,
		daemon:        daemon,
		store:         store,
		broker:        broker,
		tokenProvider: tokenProvider,
		stopCh:        make(chan struct{}),
	}
}

// SetLeaderCheck injects a leadership predicate. When nil or false, tick() is a no-op.
func (o *Manager) SetLeaderCheck(fn func() bool) {
	o.isLeader = fn
}

// Start begins the polling loop and optional HTTP health endpoint.
func (o *Manager) Start(ctx context.Context) error {
	o.ticker = time.NewTicker(o.cfg.PollDuration())
	o.wg.Add(1)
	go o.loop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", o.handleHealth)
	o.registerControlRoutes(mux)
	o.httpSrv = &http.Server{
		Addr:    o.cfg.ListenAddr,
		Handler: mux,
	}
	go func() {
		if err := o.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			o.log.Error("http server error", "err", err)
		}
	}()
	o.log.Info("manager ready", "addr", o.cfg.ListenAddr, "repo", o.cfg.FullRepo(), "poll", o.cfg.PollInterval)
	return nil
}

// Stop cleanly shuts down.
func (o *Manager) Stop(ctx context.Context) error {
	if o.ticker != nil {
		o.ticker.Stop()
	}
	close(o.stopCh)
	done := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	if o.httpSrv != nil {
		return o.httpSrv.Shutdown(ctx)
	}
	return nil
}

func (o *Manager) loop(ctx context.Context) {
	defer o.wg.Done()
	// Run immediately on start.
	if err := o.tick(ctx); err != nil {
		o.log.Error("initial tick failed", "err", err)
	}
	for {
		select {
		case <-o.stopCh:
			return
		case <-ctx.Done():
			return
		case <-o.ticker.C:
			if err := o.tick(ctx); err != nil {
				o.log.Error("tick failed", "err", err)
			}
		}
	}
}

func (o *Manager) tick(ctx context.Context) error {
	if o.isLeader != nil && !o.isLeader() {
		o.log.Info("tick skipped: not leader")
		return nil
	}
	o.log.Info("tick start")
	return o.processIssues(ctx, 0)
}

// RunOnce processes one polling pass. When issueNumber is non-zero, only that
// issue is eligible for classification; reconciliation still runs afterward.
func (o *Manager) RunOnce(ctx context.Context, issueNumber int) error {
	return o.processIssues(ctx, issueNumber)
}

func (o *Manager) processIssues(ctx context.Context, issueNumber int) error {
	issues, err := o.github.ListOpenIssues(ctx)
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}
	o.log.Info("tick issues fetched", "count", len(issues))
	var failures []error
	for _, iss := range issues {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if issueNumber != 0 && iss.Number != issueNumber {
			continue
		}
		processed, err := o.store.IsProcessed(ctx, iss.Number)
		if err != nil {
			o.log.Warn("process check failed", "issue", iss.Number, "err", err)
			continue
		}
		if processed {
			continue
		}
		kind := classifyLabels(iss.Labels)
		switch kind {
		case "PRD":
			if err := o.handlePRD(ctx, iss); err != nil {
				o.log.Error("handle PRD failed", "issue", iss.Number, "err", err)
				failures = append(failures, fmt.Errorf("handle PRD issue %d: %w", iss.Number, err))
			}
		case "ADR":
			if err := o.handleADR(ctx, iss); err != nil {
				o.log.Error("handle ADR failed", "issue", iss.Number, "err", err)
				failures = append(failures, fmt.Errorf("handle ADR issue %d: %w", iss.Number, err))
			}
		case "suggestion":
			if err := o.handleSuggestion(ctx, iss); err != nil {
				o.log.Error("handle suggestion failed", "issue", iss.Number, "err", err)
				failures = append(failures, fmt.Errorf("handle suggestion issue %d: %w", iss.Number, err))
			}
		case "log":
			// Log issues are created by the manager; skip.
			_ = o.store.MarkProcessed(ctx, iss.Number, "log", iss.Title, nil)
		default:
			// Unclassified: mark processed to avoid re-processing.
			_ = o.store.MarkProcessed(ctx, iss.Number, "unknown", iss.Title, nil)
		}
	}
	if err := o.reconcilePRs(ctx); err != nil {
		o.log.Warn("reconcile PRs failed", "err", err)
		failures = append(failures, fmt.Errorf("reconcile PRs: %w", err))
	}
	if err := o.reconcileRunResults(ctx); err != nil {
		o.log.Warn("reconcile run results failed", "err", err)
		failures = append(failures, fmt.Errorf("reconcile run results: %w", err))
	}
	if err := o.reconcileRunReviews(ctx); err != nil {
		o.log.Warn("reconcile run reviews failed", "err", err)
		failures = append(failures, fmt.Errorf("reconcile run reviews: %w", err))
	}
	return errors.Join(failures...)
}

func (o *Manager) reconcilePRs(ctx context.Context) error {
	runs, err := o.store.ListRunsNeedingPR(ctx)
	if err != nil {
		return fmt.Errorf("list runs needing PR: %w", err)
	}
	for _, run := range runs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		prTitle := fmt.Sprintf("[run] %s (#%d subtask %d)", run.SubtaskTitle, run.IssueNumber, run.SubtaskIndex)
		prBody := fmt.Sprintf("Orchestrated run for PRD #%d.\n\n**Sub-task:** %s\n\n**Run ID:** %s", run.IssueNumber, run.SubtaskTitle, run.RunID)
		url, err := o.github.CreateDraftPR(ctx, prTitle, prBody, run.Branch, o.cfg.BaseBranch)
		if err != nil {
			o.log.Info("draft PR not ready", "run", run.RunID, "branch", run.Branch, "err", err)
			continue
		}
		if err := o.store.SetRunPR(ctx, run.RunID, url); err != nil {
			return fmt.Errorf("set run PR: %w", err)
		}
		o.log.Info("draft PR created", "run", run.RunID, "branch", run.Branch, "url", url)
	}
	return nil
}

func (o *Manager) reconcileRunResults(ctx context.Context) error {
	if o.daemon == nil {
		return nil
	}
	runs, err := o.store.ListRunsNeedingResult(ctx)
	if err != nil {
		return fmt.Errorf("list runs needing result: %w", err)
	}
	for _, recorded := range runs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		run, err := o.daemon.GetRun(ctx, recorded.RunID)
		if err != nil {
			o.log.Info("run result not ready", "run", recorded.RunID, "err", err)
			continue
		}
		terminal := isTerminalRunStatus(run.Status)
		if !terminal {
			if err := o.store.SetRunStatus(ctx, recorded.RunID, run.Status, false); err != nil {
				return fmt.Errorf("set run status: %w", err)
			}
			continue
		}
		if recorded.EvidencePath == "" {
			if run.EvidencePath != "" {
				if err := o.store.SetRunEvidencePath(ctx, recorded.RunID, run.EvidencePath); err != nil {
					return fmt.Errorf("set run evidence path: %w", err)
				}
				recorded.EvidencePath = run.EvidencePath
			} else if path := managedEvidencePath(os.Getenv("AGENTS_ROOT"), runTenant(run), recorded.RunID); path != "" {
				if err := o.store.SetRunEvidencePath(ctx, recorded.RunID, path); err != nil {
					return fmt.Errorf("set run evidence path: %w", err)
				}
				recorded.EvidencePath = path
			}
		}
		if recorded.TaskID == "" && run.TaskID != "" {
			if err := o.store.SetRunTaskID(ctx, recorded.RunID, run.TaskID); err != nil {
				return fmt.Errorf("set run task id: %w", err)
			}
			recorded.TaskID = run.TaskID
		}
		if run.Status == contracts.RunStatusSucceeded && !runResultHasRequiredBindings(recorded, run) {
			o.log.Info("run success not yet bound to required result evidence", "run", recorded.RunID)
			continue
		}
		if err := o.postRunResult(ctx, recorded, run); err != nil {
			o.log.Warn("post run result failed", "run", recorded.RunID, "err", err)
			continue
		}
		if err := o.store.SetRunStatus(ctx, recorded.RunID, run.Status, true); err != nil {
			return fmt.Errorf("set run status: %w", err)
		}
	}
	return nil
}

func runResultHasRequiredBindings(recorded ManagerRun, run *contracts.Run) bool {
	if strings.TrimSpace(recorded.EvidencePath) == "" {
		return false
	}
	if recorded.LogIssueNumber == 0 {
		return false
	}
	if strings.TrimSpace(recorded.PRURL) == "" {
		return false
	}
	if runTenant(run) == "qft" || strings.HasPrefix(recorded.TaskID, "qft-task-") {
		return strings.TrimSpace(recorded.TaskID) != ""
	}
	return true
}

func runTenant(run *contracts.Run) string {
	if run == nil {
		return ""
	}
	if run.Labels != nil && run.Labels["tenant"] != "" {
		return run.Labels["tenant"]
	}
	if run.Env != nil && run.Env["AGENTS_TENANT"] != "" {
		return run.Env["AGENTS_TENANT"]
	}
	return ""
}

func isTerminalRunStatus(status contracts.RunStatus) bool {
	return status.Terminal()
}

func (o *Manager) postRunResult(ctx context.Context, recorded ManagerRun, run *contracts.Run) error {
	body := runResultComment(recorded, run)
	targets := []int{recorded.IssueNumber}
	if recorded.LogIssueNumber != 0 {
		targets = append(targets, recorded.LogIssueNumber)
	}
	for _, issueNumber := range targets {
		op := ops.Envelope("system", "manager", "manager", o.cfg.Version, "issue", fmt.Sprintf("%d", issueNumber), "post-run-result", recorded.RunID, 1)
		if err := o.broker.Do(ctx, op, func(_ context.Context) (string, error) {
			return "", o.github.AddComment(ctx, issueNumber, body)
		}); err != nil {
			return err
		}
	}
	label := "run-" + string(run.Status)
	labelOp := ops.Envelope("system", "manager", "manager", o.cfg.Version, "issue", fmt.Sprintf("%d", recorded.IssueNumber), "label-run-result", label, 1)
	return o.broker.Do(ctx, labelOp, func(_ context.Context) (string, error) {
		return "", o.github.AddLabels(ctx, recorded.IssueNumber, []string{label})
	})
}

func runResultComment(recorded ManagerRun, run *contracts.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run `%s` finished with status `%s`.\n\n", recorded.RunID, run.Status)
	fmt.Fprintf(&b, "**Sub-task:** %s\n", recorded.SubtaskTitle)
	if recorded.TaskID != "" {
		fmt.Fprintf(&b, "**Task ID:** %s\n", recorded.TaskID)
	}
	if recorded.PRURL != "" {
		fmt.Fprintf(&b, "**Draft PR:** %s\n", recorded.PRURL)
	}
	if recorded.EvidencePath != "" {
		fmt.Fprintf(&b, "**Evidence:** `%s`\n", recorded.EvidencePath)
	}
	if run.ExternalURL != "" {
		fmt.Fprintf(&b, "**External URL:** %s\n", run.ExternalURL)
	}
	if run.ExitCode != 0 {
		fmt.Fprintf(&b, "**Exit code:** %d\n", run.ExitCode)
	}
	if strings.TrimSpace(run.Error) != "" {
		fmt.Fprintf(&b, "\n**Error:** %s\n", strings.TrimSpace(run.Error))
	}
	if logs := strings.TrimSpace(run.Logs); logs != "" {
		if len(logs) > 4000 {
			logs = logs[len(logs)-4000:]
		}
		fmt.Fprintf(&b, "\n**Log tail:**\n```text\n%s\n```\n", logs)
	}
	return b.String()
}

func (o *Manager) reconcileRunReviews(ctx context.Context) error {
	runs, err := o.store.ListRunsNeedingReview(ctx)
	if err != nil {
		return fmt.Errorf("list runs needing review: %w", err)
	}
	for _, run := range runs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		prNumber, err := parseIssueNumberFromURL(run.PRURL)
		if err != nil {
			o.log.Info("run PR URL not parseable", "run", run.RunID, "pr_url", run.PRURL, "err", err)
			continue
		}
		labels, err := o.github.GetIssueLabels(ctx, prNumber)
		if err != nil {
			o.log.Info("run PR review labels not ready", "run", run.RunID, "pr", prNumber, "err", err)
			continue
		}
		verdict := runReviewVerdict(labels)
		if verdict == "" {
			o.log.Info("run PR review verdict not ready", "run", run.RunID, "pr", prNumber)
			continue
		}
		if err := o.postRunReview(ctx, run, prNumber, verdict); err != nil {
			o.log.Warn("post run review failed", "run", run.RunID, "err", err)
			continue
		}
		if err := o.store.SetRunReview(ctx, run.RunID, verdict); err != nil {
			return fmt.Errorf("set run review: %w", err)
		}
	}
	return nil
}

func runReviewVerdict(labels []string) string {
	for _, label := range labels {
		if strings.EqualFold(label, "needs-human-review") {
			return "needs-human-review"
		}
	}
	for _, label := range labels {
		if strings.EqualFold(label, "run-reviewed") {
			return "run-reviewed"
		}
	}
	return ""
}

func parseIssueNumberFromURL(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty URL")
	}
	parts := strings.Split(strings.TrimRight(raw, "/"), "/")
	if len(parts) == 0 {
		return 0, fmt.Errorf("invalid URL")
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid issue number %q", parts[len(parts)-1])
	}
	return n, nil
}

func (o *Manager) postRunReview(ctx context.Context, recorded ManagerRun, prNumber int, verdict string) error {
	body := runReviewComment(recorded, prNumber, verdict)
	targets := []int{recorded.IssueNumber}
	if recorded.LogIssueNumber != 0 {
		targets = append(targets, recorded.LogIssueNumber)
	}
	for _, issueNumber := range targets {
		op := ops.Envelope("system", "manager", "manager", o.cfg.Version, "issue", fmt.Sprintf("%d", issueNumber), "post-run-review", recorded.RunID+"-"+verdict, 1)
		if err := o.broker.Do(ctx, op, func(_ context.Context) (string, error) {
			return "", o.github.AddComment(ctx, issueNumber, body)
		}); err != nil {
			return err
		}
	}
	labelOp := ops.Envelope("system", "manager", "manager", o.cfg.Version, "issue", fmt.Sprintf("%d", recorded.IssueNumber), "label-run-review", verdict, 1)
	return o.broker.Do(ctx, labelOp, func(_ context.Context) (string, error) {
		return "", o.github.AddLabels(ctx, recorded.IssueNumber, []string{verdict})
	})
}

func runReviewComment(recorded ManagerRun, prNumber int, verdict string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run `%s` PR review verdict: `%s`.\n\n", recorded.RunID, verdict)
	fmt.Fprintf(&b, "**Sub-task:** %s\n", recorded.SubtaskTitle)
	fmt.Fprintf(&b, "**Pull request:** #%d\n", prNumber)
	if recorded.PRURL != "" {
		fmt.Fprintf(&b, "**PR URL:** %s\n", recorded.PRURL)
	}
	payload := map[string]any{
		"decision":  strings.TrimPrefix(verdict, "run-"),
		"run_id":    recorded.RunID,
		"task_id":   recorded.TaskID,
		"pr_number": prNumber,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Fprintf(&b, "\nRun PR Review Verdict\n```json\n%s\n```\n", string(data))
	return b.String()
}

func classifyLabels(labels []string) string {
	for _, l := range labels {
		lower := strings.ToLower(l)
		if lower == "prd" {
			return "PRD"
		}
		if lower == "adr" {
			return "ADR"
		}
		if lower == "log" {
			return "log"
		}
		if lower == "suggestion" {
			return "suggestion"
		}
	}
	return ""
}

// extractTenant returns the tenant a PRD's runs belong to: the first label that
// is not a classification label. A PRD with no such label is a generic repository
// task and must run tenant-less ("") so run-task.sh executes against the repo root
// (artifact_kind=code-edit) rather than failing on a missing tenant config. It must
// NOT fabricate a "default" tenant, which has no .user/projects/default config and
// makes every generic run fail "missing tenant config: default".
func extractTenant(labels []string) string {
	for _, l := range labels {
		lower := strings.ToLower(l)
		if lower != "prd" && lower != "adr" && lower != "log" && lower != "suggestion" {
			return lower
		}
	}
	return ""
}

func (o *Manager) handlePRD(ctx context.Context, iss Issue) error {
	o.log.Info("processing PRD", "issue", iss.Number, "title", iss.Title)

	tasks, err := o.tasksForPRD(ctx, iss)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return fmt.Errorf("decomposition produced zero tasks")
	}

	// For each sub-task, create run invariant: branch + draft PR + log issue + daemon run.
	// Mark the parent processed only after all intended runs were recorded; otherwise
	// a partial branch/run failure would make missing work non-retriable.
	maxRuns := o.cfg.ResolveMaxConcurrentRuns()
	spawnErrors := 0
	for i, task := range tasks {
		if i >= maxRuns {
			o.log.Warn("PRD sub-tasks exceed max runs; truncating", "issue", iss.Number, "total", len(tasks), "max", maxRuns)
			break
		}
		if err := o.spawnRun(ctx, iss, i, task); err != nil {
			o.log.Error("spawn run failed", "issue", iss.Number, "task", i, "err", err)
			spawnErrors++
		}
	}
	if spawnErrors > 0 {
		return fmt.Errorf("spawned with %d subtask error(s)", spawnErrors)
	}
	if err := o.store.MarkProcessed(ctx, iss.Number, "PRD", iss.Title, tasks); err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	return nil
}

func (o *Manager) tasksForPRD(ctx context.Context, iss Issue) ([]SubTask, error) {
	if isBridgeQFTIssue(iss) {
		return []SubTask{{
			Title:       strings.TrimSpace(strings.TrimPrefix(iss.Title, "[qft]")),
			Description: iss.Body,
		}}, nil
	}

	messages := DecomposePrompt(iss.Title, iss.Body)
	raw, err := o.gateway.ChatCompletion(ctx, "coding", messages, false)
	if err != nil {
		return nil, fmt.Errorf("decompose: %w", err)
	}
	tasks, err := ParseSubTasks(raw)
	if err != nil {
		return nil, fmt.Errorf("parse decomposition: %w", err)
	}
	return tasks, nil
}

func isBridgeQFTIssue(iss Issue) bool {
	hasQFT := false
	for _, label := range iss.Labels {
		if strings.EqualFold(label, "qft") {
			hasQFT = true
			break
		}
	}
	return hasQFT && strings.Contains(iss.Body, "Task ID: qft-task-")
}

func bridgeQFTTaskID(iss Issue) string {
	if !isBridgeQFTIssue(iss) {
		return ""
	}
	idx := strings.Index(iss.Body, "Task ID:")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(iss.Body[idx+len("Task ID:"):])
	fields := strings.FieldsFunc(rest, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '|' || r == '*' || r == ','
	})
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "qft-task-") {
		return ""
	}
	return fields[0]
}

func (o *Manager) spawnRun(ctx context.Context, iss Issue, idx int, task SubTask) error {
	runID := uuid.Must(uuid.NewV7()).String()
	slug := slugify(task.Title)
	branch := fmt.Sprintf("run/%s/%s", runID[:8], slug)
	tenant := extractTenant(iss.Labels)
	taskID := bridgeQFTTaskID(iss)
	agentsRoot := os.Getenv("AGENTS_ROOT")

	// Create branch (idempotent).
	branchOp := ops.Envelope("system", "manager", "manager", o.cfg.Version, "issue", fmt.Sprintf("%d", iss.Number), "create-branch", branch, 1)
	if err := o.broker.Do(ctx, branchOp, func(_ context.Context) (string, error) {
		return "", o.github.CreateBranch(ctx, branch, o.cfg.BaseBranch)
	}); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	// Submit run to daemon (idempotent via runID as key segment).
	submitOp := ops.Envelope("system", "manager", "manager", o.cfg.Version, "run", runID, "daemon-submit", task.Title, 1)
	var submittedRunID string
	if cachedRunID, err := o.broker.DoResult(ctx, submitOp, func(_ context.Context) (string, error) {
		env := map[string]string{
			"AGENTS_PRD_ISSUE":    fmt.Sprintf("%d", iss.Number),
			"AGENTS_ISSUE_NUMBER": fmt.Sprintf("%d", iss.Number),
			"AGENTS_TASK":         task.Title + "\n" + task.Description,
			"AGENTS_TASK_TITLE":   task.Title,
			"AGENTS_TENANT":       tenant,
			"AGENTS_REPO":         o.cfg.FullRepo(),
			"AGENTS_BRANCH":       branch,
			"AGENTS_GATEWAY_URL":  o.cfg.ResolveRunGatewayURL(),
			"AGENTS_MODEL":        "coding",
			"AGENTS_RUN_ID":       runID,
		}
		if agentsRoot != "" {
			env["AGENTS_ROOT"] = agentsRoot
		}
		if taskID != "" {
			env["AGENTS_TASK_ID"] = taskID
		}
		if o.tokenProvider == nil {
			o.log.Warn("github token provider unavailable; submitting run without GH_TOKEN")
		} else if token, err := o.tokenProvider.GetToken(); err != nil {
			o.log.Warn("github token unavailable; submitting run without GH_TOKEN", "err", err)
		} else {
			env["GH_TOKEN"] = token
		}
		req := contracts.SubmitRunRequest{
			Image:   o.cfg.DefaultImage,
			Command: []string{"bash", "/app/run-task.sh"},
			Env:     mergeEnv(task.Env, env),
			Labels: map[string]string{
				"orchestrated": "true",
				"prd":          fmt.Sprintf("%d", iss.Number),
				"task":         task.Title,
				"tenant":       tenant,
			},
			IssueRef:       fmt.Sprintf("#%d", iss.Number),
			BranchRef:      branch,
			IdempotencyKey: submitOp.IdempotencyKey,
		}
		run, err := o.daemon.SubmitRun(ctx, req)
		if err != nil {
			return "", err
		}
		return run.ID, nil
	}); err != nil {
		return fmt.Errorf("submit run: %w", err)
	} else {
		submittedRunID = strings.TrimSpace(cachedRunID)
	}
	if submittedRunID == "" {
		return fmt.Errorf("submit run returned empty run id")
	}

	// Create draft PR (best-effort). A freshly created run branch has no commits
	// ahead of base yet (the dispatched run pushes its first commit asynchronously),
	// so GitHub rejects the PR with 422 "No commits between ...". That is expected:
	// reconcilePRs (driven by ListRunsNeedingPR) creates the draft PR on a later
	// tick once the run has pushed. Failing the spawn here would skip RecordRun and
	// the PRD would never be marked processed, re-spawning the run every tick.
	prTitle := fmt.Sprintf("[run] %s (#%d subtask %d)", task.Title, iss.Number, idx)
	prBody := fmt.Sprintf("Orchestrated run for PRD #%d.\n\n**Sub-task:** %s\n\n**Run ID:** %s\n\n**Description:**\n%s", iss.Number, task.Title, submittedRunID, task.Description)
	prOp := ops.Envelope("system", "manager", "manager", o.cfg.Version, "run", submittedRunID, "create-draft-pr", branch, 1)
	var prURL string
	if url, err := o.broker.DoResult(ctx, prOp, func(_ context.Context) (string, error) {
		return o.github.CreateDraftPR(ctx, prTitle, prBody, branch, o.cfg.BaseBranch)
	}); err != nil {
		o.log.Info("draft PR deferred to reconcile (branch has no commits yet)", "run", submittedRunID, "branch", branch, "err", err)
	} else {
		prURL = url
	}

	// Create log issue (idempotent).
	logTitle := fmt.Sprintf("log: %s (#%d.%d)", task.Title, iss.Number, idx)
	logBody := fmt.Sprintf("Run log for `%s` (run %s, branch `%s`).\n\nStatus will be updated on completion.", task.Title, submittedRunID, branch)
	logOp := ops.Envelope("system", "manager", "manager", o.cfg.Version, "run", submittedRunID, "create-log-issue", logTitle, 1)
	var logIssueNum int
	if num, err := o.broker.DoResult(ctx, logOp, func(_ context.Context) (string, error) {
		num, err := o.github.CreateIssue(ctx, logTitle, logBody, []string{"log"})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d", num), nil
	}); err != nil {
		return fmt.Errorf("create log issue: %w", err)
	} else if num != "" {
		if parsed, err := strconv.Atoi(num); err == nil {
			logIssueNum = parsed
		} else {
			return fmt.Errorf("stored log issue result was not numeric for run %s: %q: %w", submittedRunID, num, err)
		}
	}
	if logIssueNum == 0 {
		return fmt.Errorf("create log issue returned empty issue number for run %s", submittedRunID)
	}

	// Record manager run metadata.
	if err := o.store.RecordRun(ctx, submittedRunID, iss.Number, idx, task.Title, branch, prURL, logIssueNum); err != nil {
		return fmt.Errorf("record run: %w", err)
	}
	if taskID != "" {
		if err := o.store.SetRunTaskID(ctx, submittedRunID, taskID); err != nil {
			return fmt.Errorf("record run task id: %w", err)
		}
	}
	if path := managedEvidencePath(agentsRoot, tenant, submittedRunID); path != "" {
		if err := o.store.SetRunEvidencePath(ctx, submittedRunID, path); err != nil {
			return fmt.Errorf("record run evidence path: %w", err)
		}
	}

	return nil
}

func managedEvidencePath(root, tenant, runID string) string {
	if root == "" || runID == "" {
		return ""
	}
	if tenant != "" {
		tenantRoot := filepath.Join(root, "projects", tenant)
		if info, err := os.Stat(tenantRoot); err == nil && info.IsDir() {
			return filepath.Join(tenantRoot, "runs", runID+".json")
		}
	}
	return filepath.Join(root, "telemetry", "runs", runID+".json")
}

func (o *Manager) handleADR(ctx context.Context, iss Issue) error {
	o.log.Info("processing ADR", "issue", iss.Number, "title", iss.Title)

	branch := fmt.Sprintf("adr/%d-%s", iss.Number, slugify(iss.Title))
	adrOp := ops.Envelope("system", "manager", "manager", o.cfg.Version, "issue", fmt.Sprintf("%d", iss.Number), "create-adr-branch-pr", branch, 1)

	if err := o.broker.Do(ctx, adrOp, func(_ context.Context) (string, error) {
		if err := o.github.CreateBranch(ctx, branch, o.cfg.BaseBranch); err != nil {
			return "", err
		}
		adrPath := fmt.Sprintf(".agents/context/adr-proposals/ADR-%04d-%s.md", iss.Number, slugify(iss.Title))
		adrContent := proposedADRContent(iss)
		if err := o.github.CreateOrUpdateFile(ctx, adrPath, branch, fmt.Sprintf("docs(adr): propose ADR for issue #%d", iss.Number), adrContent); err != nil {
			return "", err
		}
		prTitle := fmt.Sprintf("[ADR] %s (#%d)", iss.Title, iss.Number)
		prBody := fmt.Sprintf("Proposed ADR from issue #%d.\n\nFile: `%s`\n\n%s\n\n*Do not self-merge core/infra changes; human approval required.*", iss.Number, adrPath, iss.Body)
		_, err := o.github.CreateDraftPR(ctx, prTitle, prBody, branch, o.cfg.BaseBranch)
		return "", err
	}); err != nil {
		return fmt.Errorf("adr branch/pr: %w", err)
	}

	return o.store.MarkProcessed(ctx, iss.Number, "ADR", iss.Title, nil)
}

func proposedADRContent(iss Issue) string {
	return fmt.Sprintf(`# Proposed ADR: %s

Source issue: #%d

Status: proposed

## Context

%s

## Decision

TBD by human review.

## Consequences

TBD by human review.

## Approval

Core/infra ADRs require human approval before merge. Automation may draft this proposal but must not self-merge it.
`, iss.Title, iss.Number, strings.TrimSpace(iss.Body))
}

func (o *Manager) handleSuggestion(ctx context.Context, iss Issue) error {
	comment := fmt.Sprintf("Acknowledged suggestion from issue #%d. If promoted to a PRD or ADR, it will be auto-processed.", iss.Number)
	if err := o.github.AddComment(ctx, iss.Number, comment); err != nil {
		return fmt.Errorf("add comment: %w", err)
	}
	return o.store.MarkProcessed(ctx, iss.Number, "suggestion", iss.Title, nil)
}

func (o *Manager) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status":      "healthy",
		"version":     o.cfg.Version,
		"git_sha":     os.Getenv("AGENTS_GIT_SHA"),
		"image_tag":   os.Getenv("AGENTS_IMAGE_TAG"),
		"build_time":  os.Getenv("AGENTS_BUILD_TIME"),
		"node_id":     o.cfg.NodeID,
		"leader":      o.isLeader == nil || o.isLeader(),
		"daemon_url":  o.cfg.DaemonURL,
		"daemon_urls": o.cfg.DaemonURLs,
		"gateway_url": o.cfg.GatewayURL,
		"repo":        o.cfg.FullRepo(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var out strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
		} else {
			out.WriteRune('-')
		}
	}
	res := out.String()
	for strings.Contains(res, "--") {
		res = strings.ReplaceAll(res, "--", "-")
	}
	res = strings.Trim(res, "-")
	if len(res) > 40 {
		res = res[:40]
	}
	if res == "" {
		res = "task"
	}
	return res
}

func mergeEnv(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

