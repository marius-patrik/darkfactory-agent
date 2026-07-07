package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

func (o *Manager) registerControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/control/health", o.handleControlHealth)
	mux.HandleFunc("GET /v1/control/runs", o.handleControlListRuns)
	mux.HandleFunc("GET /v1/control/runs/{run_id}", o.handleControlGetRun)
	mux.HandleFunc("GET /v1/control/runs/{run_id}/logs", o.handleControlRunLogs)
	mux.HandleFunc("POST /v1/control/runs/{run_id}/cancel", o.handleControlCancelRun)
	mux.HandleFunc("POST /v1/control/runs/{run_id}/touch", o.handleControlTouchRun)
	mux.HandleFunc("POST /v1/control/qft/tasks", o.handleControlCreateQFTTask)
}

func (o *Manager) handleControlHealth(w http.ResponseWriter, r *http.Request) {
	writeControlJSON(w, http.StatusOK, map[string]any{
		"status":       "healthy",
		"leader":       o.isLeader == nil || o.isLeader(),
		"daemon_url":   o.cfg.DaemonURL,
		"daemon_urls":  o.cfg.DaemonURLs,
		"daemon_count": len(o.cfg.DaemonURLs),
	})
}

func (o *Manager) handleControlListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := o.store.ListRuns(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	enriched := make([]controlRunDTO, 0, len(runs))
	for _, run := range runs {
		enriched = append(enriched, o.runDTO(r.Context(), run))
	}
	writeControlJSON(w, http.StatusOK, map[string]any{"runs": enriched})
}

func (o *Manager) handleControlGetRun(w http.ResponseWriter, r *http.Request) {
	run, err := o.store.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}
	writeControlJSON(w, http.StatusOK, o.runDTO(r.Context(), run))
}

func (o *Manager) handleControlRunLogs(w http.ResponseWriter, r *http.Request) {
	if o.daemon == nil {
		http.Error(w, `{"error":"daemon unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	logs, err := o.daemon.GetLogs(r.Context(), r.PathValue("run_id"))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(logs))
}

func (o *Manager) handleControlCancelRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	if _, err := o.store.GetRun(r.Context(), runID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}
	if o.daemon == nil {
		http.Error(w, `{"error":"daemon unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	if err := o.daemon.CancelRun(r.Context(), runID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	if err := o.store.SetCancellationRequested(r.Context(), runID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]string{"status": "cancellation-requested", "run_id": runID})
}

func (o *Manager) handleControlTouchRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	err := o.store.TouchRun(r.Context(), runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]string{"status": "touched", "run_id": runID})
}

type controlQFTTaskRequest struct {
	Title             string `json:"title"`
	Description       string `json:"description"`
	Priority          string `json:"priority"`
	ParentRunID       string `json:"parent_run_id,omitempty"`
	ParentIssueNumber int    `json:"parent_issue_number,omitempty"`
}

func (o *Manager) handleControlCreateQFTTask(w http.ResponseWriter, r *http.Request) {
	var req controlQFTTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	record, err := buildControlQFTTaskRecord(req, time.Now().UTC())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	queue, helper, err := resolveQFTQueueStore()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusServiceUnavailable)
		return
	}
	if err := appendQFTTaskViaQueueStore(r.Context(), helper, queue, record); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadGateway)
		return
	}
	writeControlJSON(w, http.StatusAccepted, record)
}

func buildControlQFTTaskRecord(req controlQFTTaskRequest, now time.Time) (map[string]any, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		return nil, fmt.Errorf("description is required")
	}
	priority := strings.TrimSpace(req.Priority)
	if priority == "" {
		priority = "normal"
	}
	if priority != "low" && priority != "normal" && priority != "high" {
		return nil, fmt.Errorf("priority must be low, normal, or high")
	}
	taskID := "qft-task-" + uuid.Must(uuid.NewV7()).String()
	timestamp := now.Format(time.RFC3339Nano)
	record := map[string]any{
		"id":          taskID,
		"title":       title,
		"description": description,
		"tenant":      "qft",
		"status":      "pending",
		"priority":    priority,
		"created_at":  timestamp,
		"updated_at":  timestamp,
		"created_by":  "tui",
	}
	if strings.TrimSpace(req.ParentRunID) != "" {
		record["parent_run_id"] = strings.TrimSpace(req.ParentRunID)
	}
	if req.ParentIssueNumber > 0 {
		record["parent_issue_number"] = req.ParentIssueNumber
	}
	return record, nil
}

func resolveQFTQueueStore() (queue string, helper string, err error) {
	if explicit := os.Getenv("QFT_QUEUE_PATH"); explicit != "" {
		queue = explicit
	}
	agentsRoot := os.Getenv("AGENTS_ROOT")
	if agentsRoot == "" {
		return "", "", fmt.Errorf("AGENTS_ROOT is required for QFT task creation")
	}
	if queue == "" {
		queue = filepath.Join(agentsRoot, ".user", "projects", "qft", "tasks", "queue.jsonl")
	}
	helper = filepath.Join(agentsRoot, ".user", "projects", "qft", "queue_store.py")
	if _, statErr := os.Stat(helper); statErr == nil {
		return queue, helper, nil
	}
	env := os.Getenv("AGENTS_ENV")
	if env == "dev" || env == "test" {
		repo := os.Getenv("AGENTS_REPO")
		if repo != "" {
			candidate := filepath.Join(repo, ".user", "projects", "qft", "queue_store.py")
			if _, statErr := os.Stat(candidate); statErr == nil {
				return queue, candidate, nil
			}
		}
	}
	return "", "", fmt.Errorf("QFT queue_store.py not found under AGENTS_ROOT")
}

func appendQFTTaskViaQueueStore(ctx context.Context, helper, queue string, record map[string]any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, pythonExecutable(), helper, "append", "--queue", queue, "--json", string(data))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("queue append failed: %s", message)
	}
	return nil
}

func pythonExecutable() string {
	if explicit := os.Getenv("PYTHON"); explicit != "" {
		return explicit
	}
	if runtime.GOOS == "windows" {
		if path, err := exec.LookPath("python"); err == nil {
			return path
		}
	}
	if path, err := exec.LookPath("python3"); err == nil {
		return path
	}
	return "python3"
}

type controlRunDTO struct {
	RunID                   string              `json:"run_id"`
	IssueNumber             int                 `json:"issue_number"`
	SubtaskIndex            int                 `json:"subtask_index"`
	SubtaskTitle            string              `json:"subtask_title"`
	Branch                  string              `json:"branch"`
	PRURL                   string              `json:"pr_url"`
	LogIssueNumber          int                 `json:"log_issue_number"`
	LastStatus              contracts.RunStatus `json:"last_status,omitempty"`
	DaemonStatus            contracts.RunStatus `json:"daemon_status,omitempty"`
	DaemonError             string              `json:"daemon_error,omitempty"`
	ResultIngestedAt        *time.Time          `json:"result_ingested_at,omitempty"`
	ReviewStatus            string              `json:"review_status,omitempty"`
	ReviewIngestedAt        *time.Time          `json:"review_ingested_at,omitempty"`
	CreatedAt               time.Time           `json:"created_at"`
	UpdatedAt               time.Time           `json:"updated_at"`
	TaskID                  string              `json:"task_id,omitempty"`
	EvidencePath            string              `json:"evidence_path,omitempty"`
	CancellationRequestedAt *time.Time          `json:"cancellation_requested_at,omitempty"`
}

func (o *Manager) runDTO(ctx context.Context, run ManagerRun) controlRunDTO {
	out := controlRunDTO{
		RunID:                   run.RunID,
		IssueNumber:             run.IssueNumber,
		SubtaskIndex:            run.SubtaskIndex,
		SubtaskTitle:            run.SubtaskTitle,
		Branch:                  run.Branch,
		PRURL:                   run.PRURL,
		LogIssueNumber:          run.LogIssueNumber,
		LastStatus:              run.LastStatus,
		ResultIngestedAt:        run.ResultIngestedAt,
		ReviewStatus:            run.ReviewStatus,
		ReviewIngestedAt:        run.ReviewIngestedAt,
		CreatedAt:               run.CreatedAt,
		UpdatedAt:               run.UpdatedAt,
		TaskID:                  run.TaskID,
		EvidencePath:            run.EvidencePath,
		CancellationRequestedAt: run.CancellationRequestedAt,
	}
	if o.daemon == nil {
		return out
	}
	daemonRun, err := o.daemon.GetRun(ctx, run.RunID)
	if err != nil {
		out.DaemonError = err.Error()
		return out
	}
	out.DaemonStatus = daemonRun.Status
	return out
}

func writeControlJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

