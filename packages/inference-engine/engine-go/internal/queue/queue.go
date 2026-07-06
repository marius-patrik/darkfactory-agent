// Package queue implements a concurrency-capped run queue backed by the store.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/events"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/runner"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/store"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// Queue schedules runs with a hard concurrency cap.
type Queue struct {
	cap     int
	mu      sync.Mutex
	running map[string]*contracts.Run
	waiting []*contracts.Run
	store   *store.Store
	runner  runner.Interface
	events  events.Bus
	ops     *ops.Broker
	log     *slog.Logger
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// New creates a Queue. cap <=0 means 1.
func New(cap int, st *store.Store, r runner.Interface, bus events.Bus, broker *ops.Broker, log *slog.Logger) *Queue {
	if cap <= 0 {
		cap = 1
	}
	if log == nil {
		log = slog.Default()
	}
	return &Queue{
		cap:     cap,
		running: make(map[string]*contracts.Run),
		store:   st,
		runner:  r,
		events:  bus,
		ops:     broker,
		log:     log.With("component", "queue"),
		stopCh:  make(chan struct{}),
	}
}

// Cap returns the concurrency cap.
func (q *Queue) Cap() int { return q.cap }

// Start begins background supervision.
func (q *Queue) Start(ctx context.Context) {
	q.recoverPersisted(ctx)
	q.wg.Add(1)
	go q.loop(ctx)
}

// Stop waits for the queue to drain running work (does not cancel active runs).
func (q *Queue) Stop() {
	close(q.stopCh)
	q.wg.Wait()
}

func (q *Queue) recoverPersisted(ctx context.Context) {
	runs, err := q.store.ListRunsByStatus(ctx, contracts.RunStatusQueued, contracts.RunStatusRunning)
	if err != nil {
		q.log.Error("recover persisted runs failed", "err", err)
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	recoveredQueued := 0
	recoveredRunning := 0
	repinnedImage := 0
	closedStale := 0
	currentRunImage := strings.TrimSpace(os.Getenv("AGENTS_HARNESS_IMAGE"))
	if currentRunImage == "" {
		currentRunImage = strings.TrimSpace(os.Getenv("AGENTS_MANAGER_IMAGE"))
	}
	for i := range runs {
		r := runs[i]
		switch r.Status {
		case contracts.RunStatusQueued:
			if currentRunImage != "" && r.Image != "" && r.Image != currentRunImage {
				q.log.Info("re-pinning queued run image", "run_id", r.ID, "old_image", r.Image, "current_image", currentRunImage)
				r.Image = currentRunImage
				r.ExitCode = 0
				r.Error = ""
				r.FinishedAt = nil
				if err := q.store.SaveRun(ctx, &r); err != nil {
					q.log.Error("save re-pinned queued run failed", "run_id", r.ID, "err", err)
					continue
				}
				repinnedImage++
			}
			if !q.hasWaitingLocked(r.ID) {
				run := r
				q.waiting = append(q.waiting, &run)
				recoveredQueued++
			}
		case contracts.RunStatusRunning:
			if currentRunImage != "" && r.Image != "" && r.Image != currentRunImage {
				if r.ContainerID != "" {
					if err := q.runner.Stop(ctx, r.ContainerID); err != nil {
						q.log.Warn("stop stale running image failed", "run_id", r.ID, "executor_id", r.ContainerID, "err", err)
					}
				}
				q.log.Info("re-pinning running run image", "run_id", r.ID, "old_image", r.Image, "current_image", currentRunImage)
				r.Status = contracts.RunStatusQueued
				r.Image = currentRunImage
				r.ContainerID = ""
				r.ExternalURL = ""
				r.ExitCode = 0
				r.Logs = ""
				r.StartedAt = nil
				r.FinishedAt = nil
				r.Error = ""
				if err := q.store.SaveRun(ctx, &r); err != nil {
					q.log.Error("save re-pinned running image failed", "run_id", r.ID, "err", err)
					continue
				}
				if !q.hasWaitingLocked(r.ID) {
					run := r
					q.waiting = append(q.waiting, &run)
					recoveredQueued++
				}
				repinnedImage++
				continue
			}
			if r.ContainerID != "" {
				alive, err := q.runner.IsRunning(ctx, r.ContainerID)
				if err == nil && alive {
					run := r
					q.running[r.ID] = &run
					recoveredRunning++
					continue
				}
				if err != nil {
					q.log.Warn("recover running inspect failed", "run_id", r.ID, "executor_id", r.ContainerID, "err", err)
				}
			}
			now := time.Now().UTC()
			r.Status = contracts.RunStatusInfraFailed
			r.ExitCode = -1
			r.FinishedAt = &now
			if r.ContainerID == "" {
				r.Error = "daemon recovered running run without executor id"
			} else if r.Error == "" {
				r.Error = "daemon recovered stale running run with no live executor"
			}
			if err := q.store.SaveRun(ctx, &r); err != nil {
				q.log.Error("save recovered stale run failed", "run_id", r.ID, "err", err)
				continue
			}
			closedStale++
		}
	}
	if recoveredQueued > 0 || recoveredRunning > 0 || repinnedImage > 0 || closedStale > 0 {
		q.log.Info("recovered persisted runs", "queued", recoveredQueued, "running", recoveredRunning, "repinned_image", repinnedImage, "closed_stale", closedStale)
	}
	q.maybeStartLocked(ctx)
}

func (q *Queue) hasWaitingLocked(id string) bool {
	for _, run := range q.waiting {
		if run.ID == id {
			return true
		}
	}
	return false
}

// Submit queues a run. If the run already exists and is not terminal, it is returned unchanged.
func (q *Queue) Submit(ctx context.Context, r *contracts.Run) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if existing, ok := q.running[r.ID]; ok {
		q.log.Info("run already running", "run_id", r.ID, "status", existing.Status)
		return nil
	}
	for _, w := range q.waiting {
		if w.ID == r.ID {
			q.log.Info("run already queued", "run_id", r.ID)
			return nil
		}
	}

	r.Status = contracts.RunStatusQueued
	r.CreatedAt = time.Now().UTC()
	if err := q.store.SaveRun(ctx, r); err != nil {
		return fmt.Errorf("save run: %w", err)
	}

	if err := q.emit(ctx, r.ID, contracts.RunStatus(""), contracts.RunStatusQueued, "submitted"); err != nil {
		r.Status = contracts.RunStatusInfraFailed
		r.Error = fmt.Sprintf("queued event publish failed: %v", err)
		now := time.Now().UTC()
		r.FinishedAt = &now
		if saveErr := q.store.SaveRun(ctx, r); saveErr != nil {
			return fmt.Errorf("save queued publish failure: %w", saveErr)
		}
		return fmt.Errorf("%s", r.Error)
	}
	q.waiting = append(q.waiting, r)
	q.log.Info("run queued", "run_id", r.ID, "queue_len", len(q.waiting))
	q.maybeStartLocked(ctx)
	return nil
}

// Cancel attempts to cancel a queued or running run.
func (q *Queue) Cancel(ctx context.Context, runID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if r, ok := q.running[runID]; ok {
		q.log.Info("cancelling running run", "run_id", runID)
		if err := q.runner.Stop(ctx, r.ContainerID); err != nil {
			q.log.Warn("runner stop failed", "run_id", runID, "err", err)
		}
		r.Status = contracts.RunStatusCancelled
		now := time.Now().UTC()
		r.FinishedAt = &now
		if err := q.emit(ctx, runID, contracts.RunStatusRunning, contracts.RunStatusCancelled, "cancelled"); err != nil {
			r.Error = fmt.Sprintf("cancel event publish failed: %v", err)
		}
		if err := q.store.SaveRun(ctx, r); err != nil {
			return fmt.Errorf("save cancelled run: %w", err)
		}
		delete(q.running, runID)
		q.maybeStartLocked(ctx)
		if r.Error != "" {
			return fmt.Errorf("%s", r.Error)
		}
		return nil
	}

	for i, w := range q.waiting {
		if w.ID == runID {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			w.Status = contracts.RunStatusCancelled
			now := time.Now().UTC()
			w.FinishedAt = &now
			if err := q.emit(ctx, runID, contracts.RunStatusQueued, contracts.RunStatusCancelled, "cancelled"); err != nil {
				w.Error = fmt.Sprintf("cancel event publish failed: %v", err)
			}
			if err := q.store.SaveRun(ctx, w); err != nil {
				return fmt.Errorf("save cancelled run: %w", err)
			}
			if w.Error != "" {
				return fmt.Errorf("%s", w.Error)
			}
			return nil
		}
	}
	return fmt.Errorf("run %s not found", runID)
}

// Running returns a snapshot of currently running runs.
func (q *Queue) Running() []contracts.Run {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]contracts.Run, 0, len(q.running))
	for _, r := range q.running {
		out = append(out, *r)
	}
	return out
}

// Waiting returns a snapshot of queued runs.
func (q *Queue) Waiting() []contracts.Run {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]contracts.Run, len(q.waiting))
	for i, r := range q.waiting {
		out[i] = *r
	}
	return out
}

func (q *Queue) loop(ctx context.Context) {
	defer q.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.reconcile(ctx)
		}
	}
}

func (q *Queue) reconcile(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, r := range q.running {
		// Grace period for runs with unresolved executor IDs.
		if r.ContainerID == "" {
			if r.StartedAt != nil && time.Since(*r.StartedAt) < 5*time.Minute {
				continue
			}
			// After grace period, mark as infra-failed since we can't track it.
			r.Status = contracts.RunStatusInfraFailed
			r.ExitCode = -1
			r.Error = "unresolved executor id after grace period"
			now := time.Now().UTC()
			r.FinishedAt = &now
			if err := q.emit(ctx, id, contracts.RunStatusRunning, contracts.RunStatusInfraFailed, r.Error); err != nil {
				r.Error = fmt.Sprintf("%s; terminal failure event publish failed: %v", r.Error, err)
			}
			if err := q.store.SaveRun(ctx, r); err != nil {
				q.log.Error("save terminal run failed", "run_id", id, "err", err)
				continue
			}
			delete(q.running, id)
			q.log.Info("run finished", "run_id", id, "exit_code", -1, "reason", "unresolved container id")
			continue
		}

		alive, err := q.runner.IsRunning(ctx, r.ContainerID)
		if err != nil {
			q.log.Warn("runner inspect failed", "run_id", id, "err", err)
			continue
		}
		if !alive {
			exit, err := q.runner.ExitCode(ctx, r.ContainerID)
			if err != nil {
				q.log.Warn("runner exit code failed", "run_id", id, "err", err)
				exit = -1
				r.Error = fmt.Sprintf("runner exit code failed: %v", err)
			}
			r.ExitCode = exit
			if err != nil {
				r.Status = contracts.RunStatusInfraFailed
				if err := q.emit(ctx, id, contracts.RunStatusRunning, contracts.RunStatusInfraFailed, r.Error); err != nil {
					r.Error = fmt.Sprintf("%s; terminal failure event publish failed: %v", r.Error, err)
				}
			} else if exit == 0 {
				status, reason := q.classifyTerminalExit(r, exit)
				r.Status = status
				populateRunEvidenceFields(r)
				if err := q.emit(ctx, id, contracts.RunStatusRunning, status, reason); err != nil {
					r.Status = contracts.RunStatusInfraFailed
					r.Error = fmt.Sprintf("terminal event publish failed: %v", err)
				} else {
					r.Error = terminalErrorForStatus(status, reason)
				}
			} else {
				status, reason := q.classifyTerminalExit(r, exit)
				r.Status = status
				populateRunEvidenceFields(r)
				if err := q.emit(ctx, id, contracts.RunStatusRunning, status, reason); err != nil {
					r.Status = contracts.RunStatusInfraFailed
					r.Error = fmt.Sprintf("terminal failure event publish failed: %v", err)
				} else {
					r.Error = terminalErrorForStatus(status, reason)
				}
			}
			now := time.Now().UTC()
			r.FinishedAt = &now
			logs, _ := q.runner.Logs(ctx, r.ContainerID)
			r.Logs = logs
			if url, ok := r.Labels["external_url"]; ok {
				r.ExternalURL = url
			}
			if err := q.store.SaveRun(ctx, r); err != nil {
				q.log.Error("save terminal run failed", "run_id", id, "err", err)
				continue
			}
			if err := q.runner.Remove(ctx, r.ContainerID); err != nil {
				q.log.Warn("runner remove failed", "run_id", id, "err", err)
			}
			delete(q.running, id)
			q.log.Info("run finished", "run_id", id, "exit_code", exit)
		}
	}
	q.maybeStartLocked(ctx)
}

func (q *Queue) maybeStartLocked(ctx context.Context) {
	for len(q.waiting) > 0 && len(q.running) < q.cap {
		r := q.waiting[0]
		q.waiting = q.waiting[1:]

		// Idempotent container start via operation broker.
		var cid string
		op := ops.Envelope("system", "daemon", "engine", "0.1.0", "run", r.ID, "runner-start", r.Image, 1)
		startErr := q.ops.Do(ctx, op, func(innerCtx context.Context) (string, error) {
			if r.Env == nil {
				r.Env = map[string]string{}
			}
			r.Env["AGENTS_RUN_ID"] = r.ID
			if r.HeadSHA == "" {
				r.HeadSHA = strings.TrimSpace(r.Env["AGENTS_HEAD_SHA"])
			}
			if r.HeadSHA != "" {
				r.Env["AGENTS_HEAD_SHA"] = r.HeadSHA
			}
			if r.Env["AGENTS_ROOT"] == "" && usesManagedRunContract(r) {
				r.Env["AGENTS_ROOT"] = os.Getenv("AGENTS_ROOT")
			}
			c, err := q.runner.Start(innerCtx, r.Image, r.Command, r.Env, r.Labels)
			if err != nil {
				return "", err
			}
			cid = c
			return c, nil
		})
		if startErr != nil {
			q.log.Error("failed to start container", "run_id", r.ID, "err", startErr)
			r.Status = contracts.RunStatusInfraFailed
			r.Error = startErr.Error()
			now := time.Now().UTC()
			r.FinishedAt = &now
			if err := q.emit(ctx, r.ID, contracts.RunStatusQueued, contracts.RunStatusInfraFailed, startErr.Error()); err != nil {
				r.Error = fmt.Sprintf("%s; start failure event publish failed: %v", r.Error, err)
			}
			if err := q.store.SaveRun(ctx, r); err != nil {
				q.log.Error("save start failure failed", "run_id", r.ID, "err", err)
				q.waiting = append([]*contracts.Run{r}, q.waiting...)
				return
			}
			continue
		}

		r.ContainerID = cid
		r.Status = contracts.RunStatusRunning
		now := time.Now().UTC()
		r.StartedAt = &now
		if url, err := q.runner.URL(ctx, cid); err == nil && url != "" {
			r.ExternalURL = url
		}
		if err := q.store.SaveRun(ctx, r); err != nil {
			q.log.Error("save running run failed", "run_id", r.ID, "err", err)
			q.waiting = append([]*contracts.Run{r}, q.waiting...)
			return
		}
		q.running[r.ID] = r
		if err := q.emit(ctx, r.ID, contracts.RunStatusQueued, contracts.RunStatusRunning, "container started"); err != nil {
			r.Status = contracts.RunStatusInfraFailed
			r.Error = fmt.Sprintf("running event publish failed: %v", err)
			now := time.Now().UTC()
			r.FinishedAt = &now
			if saveErr := q.store.SaveRun(ctx, r); saveErr != nil {
				q.log.Error("save running event failure failed", "run_id", r.ID, "err", saveErr)
				delete(q.running, r.ID)
				q.waiting = append([]*contracts.Run{r}, q.waiting...)
				return
			}
			if err := q.runner.Stop(ctx, r.ContainerID); err != nil {
				q.log.Warn("runner stop after event failure failed", "run_id", r.ID, "err", err)
			}
			delete(q.running, r.ID)
			continue
		}
		q.log.Info("run started", "run_id", r.ID, "container_id", cid)
	}
}

func (q *Queue) classifyTerminalExit(r *contracts.Run, exitCode int) (contracts.RunStatus, string) {
	if !requiresTerminalEvidence(r) {
		if exitCode == 0 {
			return contracts.RunStatusSucceeded, "container exited 0"
		}
		return contracts.RunStatusFailed, fmt.Sprintf("container exited %d", exitCode)
	}
	evidence, path, err := readTerminalEvidence(r)
	if err != nil {
		if exitCode != 0 {
			return contracts.RunStatusFailed, fmt.Sprintf("container exited %d", exitCode)
		}
		return contracts.RunStatusFailed, fmt.Sprintf("missing terminal evidence: %v", err)
	}
	status := contracts.RunStatus(evidence.Status)
	if !status.Known() {
		return contracts.RunStatusFailed, fmt.Sprintf("unknown terminal evidence status %q in %s", evidence.Status, path)
	}
	if !status.Terminal() {
		return contracts.RunStatusFailed, fmt.Sprintf("non-terminal evidence status %q in %s", evidence.Status, path)
	}
	if evidence.RunID != "" && evidence.RunID != r.ID {
		return contracts.RunStatusFailed, fmt.Sprintf("terminal evidence run_id %q does not match %q", evidence.RunID, r.ID)
	}
	if r.HeadSHA != "" && evidence.HeadSHA != r.HeadSHA {
		return contracts.RunStatusFailed, fmt.Sprintf("terminal evidence head_sha %q does not match %q", evidence.HeadSHA, r.HeadSHA)
	}
	if status == contracts.RunStatusSucceeded {
		if reason := validateSuccessfulTerminalEvidence(r, evidence); reason != "" {
			return contracts.RunStatusFailed, fmt.Sprintf("invalid terminal success evidence in %s: %s", path, reason)
		}
	}
	return status, fmt.Sprintf("terminal evidence %s from %s", status, path)
}

func requiresTerminalEvidence(r *contracts.Run) bool {
	if r == nil {
		return false
	}
	if r.Env["AGENTS_ROOT"] != "" {
		return true
	}
	if r.Env["AGENTS_TENANT"] != "" || r.Labels["tenant"] != "" {
		return true
	}
	for _, item := range r.Command {
		if item == "/app/run-task.sh" || item == "deploy/run-task.sh" {
			return true
		}
	}
	return false
}

func usesManagedRunContract(r *contracts.Run) bool {
	if r == nil {
		return false
	}
	if r.Env["AGENTS_TENANT"] != "" || r.Labels["tenant"] != "" {
		return true
	}
	for _, item := range r.Command {
		if item == "/app/run-task.sh" || item == "deploy/run-task.sh" {
			return true
		}
	}
	return false
}

type terminalEvidence struct {
	RunID       string   `json:"run_id"`
	TaskID      string   `json:"task_id"`
	Tenant      string   `json:"tenant"`
	IssueNumber int      `json:"issue_number"`
	Branch      string   `json:"branch"`
	PRURL       string   `json:"pr_url"`
	LogIssue    int      `json:"log_issue_number"`
	HeadSHA     string   `json:"head_sha"`
	Status      string   `json:"status"`
	Artifact    artifact `json:"artifact"`
	Kubernetes  k8sInfo  `json:"kubernetes"`
	Failure     struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"failure"`
}

type artifact struct {
	Kind  string   `json:"kind"`
	Paths []string `json:"paths"`
}

type k8sInfo struct {
	Namespace     string `json:"namespace"`
	JobName       string `json:"job_name"`
	PodName       string `json:"pod_name"`
	ContainerName string `json:"container_name"`
	LogRef        string `json:"log_ref"`
}

func validateSuccessfulTerminalEvidence(r *contracts.Run, evidence terminalEvidence) string {
	if r == nil {
		return "missing run"
	}
	tenant := r.Env["AGENTS_TENANT"]
	if tenant == "" && r.Labels != nil {
		tenant = r.Labels["tenant"]
	}
	if evidence.Tenant != "" && tenant != "" && evidence.Tenant != tenant {
		return fmt.Sprintf("tenant %q does not match %q", evidence.Tenant, tenant)
	}
	if expected := r.Env["AGENTS_TASK_ID"]; expected != "" && evidence.TaskID != expected {
		return fmt.Sprintf("task_id %q does not match %q", evidence.TaskID, expected)
	}
	if r.IssueRef != "" {
		expectedIssue := strings.TrimPrefix(strings.TrimSpace(r.IssueRef), "#")
		if fmt.Sprintf("%d", evidence.IssueNumber) != expectedIssue {
			return fmt.Sprintf("issue_number %d does not match %q", evidence.IssueNumber, r.IssueRef)
		}
	}
	if r.BranchRef != "" && evidence.Branch != r.BranchRef {
		return fmt.Sprintf("branch %q does not match %q", evidence.Branch, r.BranchRef)
	}
	if evidence.HeadSHA == "" {
		return "missing head_sha"
	}
	if tenant == "qft" {
		if evidence.Artifact.Kind != "proof-certificate" {
			return fmt.Sprintf("artifact kind %q is not proof-certificate", evidence.Artifact.Kind)
		}
		if len(evidence.Artifact.Paths) == 0 {
			return "missing proof artifact paths"
		}
		for _, path := range evidence.Artifact.Paths {
			if !strings.HasPrefix(path, ".user/projects/qft/research/proof_certificates/") &&
				!strings.HasPrefix(path, "projects/qft/research/proof_certificates/") {
				return fmt.Sprintf("proof artifact path %q is outside QFT proof certificates", path)
			}
		}
		if evidence.Kubernetes.JobName == "" {
			return "missing kubernetes.job_name"
		}
		if evidence.Kubernetes.ContainerName == "" {
			return "missing kubernetes.container_name"
		}
		if evidence.Kubernetes.LogRef == "" && evidence.Kubernetes.PodName == "" {
			return "missing kubernetes log reference"
		}
	}
	return ""
}

func populateRunEvidenceFields(r *contracts.Run) {
	evidence, path, err := readTerminalEvidence(r)
	if err != nil {
		return
	}
	if evidence.RunID != "" && evidence.RunID != r.ID {
		return
	}
	if evidence.TaskID != "" {
		r.TaskID = evidence.TaskID
	}
	// Never overwrite a bound head SHA with the run-reported one: when a run is
	// started with an expected/bound head SHA, the run record must retain it so
	// a mismatch fails closed (the bound SHA is the identity, not the report).
	if evidence.HeadSHA != "" && r.HeadSHA == "" {
		r.HeadSHA = evidence.HeadSHA
	}
	r.EvidencePath = path
}

func readTerminalEvidence(r *contracts.Run) (terminalEvidence, string, error) {
	var evidence terminalEvidence
	root := r.Env["AGENTS_ROOT"]
	if root == "" {
		return evidence, "", fmt.Errorf("AGENTS_ROOT is empty")
	}
	tenant := r.Env["AGENTS_TENANT"]
	if tenant == "" && r.Labels != nil {
		tenant = r.Labels["tenant"]
	}
	path := filepath.Join(root, "telemetry", "runs", r.ID+".json")
	if tenant != "" {
		tenantPath := filepath.Join(root, "projects", tenant)
		info, err := os.Stat(tenantPath)
		if err != nil {
			return evidence, filepath.Join(tenantPath, "runs", r.ID+".json"), fmt.Errorf("missing tenant config %q: %w", tenant, err)
		}
		if !info.IsDir() {
			return evidence, filepath.Join(tenantPath, "runs", r.ID+".json"), fmt.Errorf("missing tenant config %q: %s is not a directory", tenant, tenantPath)
		}
		path = filepath.Join(tenantPath, "runs", r.ID+".json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return evidence, path, err
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		return evidence, path, err
	}
	return evidence, path, nil
}

func terminalErrorForStatus(status contracts.RunStatus, reason string) string {
	if status.Success() || status == contracts.RunStatusNoOp {
		return ""
	}
	if reason == "" {
		return fmt.Sprintf("terminal status %s", status)
	}
	return reason
}

func (q *Queue) emit(ctx context.Context, runID string, from, to contracts.RunStatus, reason string) error {
	if q.events == nil {
		return nil
	}
	ev := contracts.RunEvent{
		TraceID:   ops.NewKey("event", runID, string(to)),
		RunID:     runID,
		From:      from,
		To:        to,
		Timestamp: time.Now().UTC(),
		Reason:    reason,
	}
	if err := q.events.PublishRunEvent(ctx, ev); err != nil {
		q.log.Warn("event publish failed", "err", err)
		return err
	}
	return nil
}

