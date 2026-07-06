// Package githubactions dispatches agent runs as GitHub Actions workflow runs.
package githubactions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ghauth"
)

// Runner dispatches and tracks GitHub Actions workflow runs.
type Runner struct {
	tokenProvider ghauth.TokenProvider
	owner         string
	repo          string
	workflow      string
	ref           string
	client        *http.Client
	log           *slog.Logger
}

// NewRunner creates a GitHub Actions runner.
func NewRunner(tokenProvider ghauth.TokenProvider, owner, repo, workflow, ref string, log *slog.Logger) *Runner {
	if workflow == "" {
		workflow = "agent-run.yml"
	}
	if ref == "" {
		ref = "dev"
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		tokenProvider: tokenProvider,
		owner:         owner,
		repo:          repo,
		workflow:      workflow,
		ref:           ref,
		client:        &http.Client{Timeout: 30 * time.Second},
		log:           log.With("component", "githubactions"),
	}
}

// Close is a no-op for the GitHub Actions runner.
func (r *Runner) Close() error { return nil }

func (r *Runner) getToken() (string, error) {
	if r.tokenProvider == nil {
		return "", fmt.Errorf("no token provider configured")
	}
	return r.tokenProvider.GetToken()
}

// Start dispatches the workflow and returns the workflow run ID.
func (r *Runner) Start(ctx context.Context, image string, cmd []string, env, labels map[string]string) (string, error) {
	runID := env["AGENTS_RUN_ID"]
	if runID == "" {
		runID = labels["run_id"]
	}
	if runID == "" {
		return "", fmt.Errorf("AGENTS_RUN_ID or run_id label is required")
	}

	token, err := r.getToken()
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}

	inputs := map[string]any{
		"run_id":      runID,
		"tenant":      firstNonEmpty(env["AGENTS_TENANT"], labels["tenant"], "default"),
		"task":        firstNonEmpty(env["AGENTS_TASK"], labels["task"], ""),
		"model":       firstNonEmpty(env["AGENTS_MODEL"], labels["model"], "coding"),
		"branch":      firstNonEmpty(env["AGENTS_BRANCH"], labels["branch"], ""),
		"gateway_url": firstNonEmpty(env["AGENTS_GATEWAY_URL"], labels["gateway_url"], "http://s001:4000"),
	}

	body := map[string]any{
		"ref":    r.ref,
		"inputs": inputs,
	}
	b, _ := json.Marshal(body)

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/dispatches", r.owner, r.repo, r.workflow)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	r.log.Info("dispatching workflow", "run_id", runID, "url", url)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("dispatch failed: %s: %s", resp.Status, string(bodyBytes))
	}

	// Resolve the workflow run id by polling.
	workflowRunID, err := r.resolveRun(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("resolve workflow run: %w", err)
	}

	r.log.Info("workflow run resolved", "run_id", runID, "workflow_run_id", workflowRunID)
	return workflowRunID, nil
}

// Stop cancels a workflow run.
func (r *Runner) Stop(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	token, err := r.getToken()
	if err != nil {
		return fmt.Errorf("get token: %w", err)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs/%s/cancel", r.owner, r.repo, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("build cancel request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("cancel request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusUnprocessableEntity {
		// Already completed or cannot cancel.
		return nil
	}
	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cancel failed: %s: %s", resp.Status, string(bodyBytes))
	}
	return nil
}

// Remove is a no-op for GitHub Actions runs.
func (r *Runner) Remove(ctx context.Context, id string) error { return nil }

// IsRunning returns true if the workflow run is still in progress.
func (r *Runner) IsRunning(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	status, conclusion, err := r.runStatus(ctx, id)
	if err != nil {
		return false, err
	}
	if status == "completed" {
		return false, nil
	}
	if conclusion != "" {
		return false, nil
	}
	return status == "in_progress" || status == "queued" || status == "waiting" || status == "pending" || status == "requested", nil
}

// ExitCode maps workflow conclusion to an exit code.
func (r *Runner) ExitCode(ctx context.Context, id string) (int, error) {
	if id == "" {
		return -1, nil
	}
	_, conclusion, err := r.runStatus(ctx, id)
	if err != nil {
		return -1, err
	}
	switch conclusion {
	case "success":
		return 0, nil
	case "failure", "cancelled", "timed_out", "action_required", "startup_failure":
		return 1, nil
	default:
		return -1, nil
	}
}

// URL returns the workflow run HTML URL.
func (r *Runner) URL(ctx context.Context, id string) (string, error) {
	return r.Logs(ctx, id)
}

// Logs returns the workflow run URL as the "logs" reference.
func (r *Runner) Logs(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	token, err := r.getToken()
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs/%s", r.owner, r.repo, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get run failed: %s", resp.Status)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.HTMLURL, nil
}

func (r *Runner) runStatus(ctx context.Context, id string) (status, conclusion string, err error) {
	token, err := r.getToken()
	if err != nil {
		return "", "", fmt.Errorf("get token: %w", err)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs/%s", r.owner, r.repo, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("build status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "completed", "failure", nil
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("status failed: %s: %s", resp.Status, string(bodyBytes))
	}

	var result struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode status: %w", err)
	}
	return result.Status, result.Conclusion, nil
}

func (r *Runner) resolveRun(ctx context.Context, runID string) (string, error) {
	// GitHub eventual consistency: wait briefly then poll workflow runs.
	listURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?event=workflow_dispatch&per_page=10", r.owner, r.repo, r.workflow)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		token, err := r.getToken()
		if err != nil {
			return "", fmt.Errorf("get token: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := r.client.Do(req)
		if err != nil {
			if err := sleepContext(ctx, 1*time.Second); err != nil {
				return "", err
			}
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			if err := sleepContext(ctx, 1*time.Second); err != nil {
				return "", err
			}
			continue
		}

		var result struct {
			WorkflowRuns []struct {
				ID        int64  `json:"id"`
				RunNumber int    `json:"run_number"`
				CreatedAt string `json:"created_at"`
			} `json:"workflow_runs"`
		}
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			if err := sleepContext(ctx, 1*time.Second); err != nil {
				return "", err
			}
			continue
		}

		// Check the most recent runs for matching inputs.
		for _, wr := range result.WorkflowRuns {
			inputs, err := r.runInputs(ctx, wr.ID)
			if err != nil {
				continue
			}
			if inputs != nil && inputs["run_id"] == runID {
				return fmt.Sprintf("%d", wr.ID), nil
			}
		}

		if err := sleepContext(ctx, 1*time.Second); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("timed out resolving workflow run for run_id=%s", runID)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Runner) runInputs(ctx context.Context, workflowRunID int64) (map[string]any, error) {
	token, err := r.getToken()
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/runs/%d", r.owner, r.repo, workflowRunID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get run %d failed: %s", workflowRunID, resp.Status)
	}

	var result struct {
		Inputs map[string]any `json:"inputs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Inputs, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

