// Package contracts defines the public types shared across the daemon.
package contracts

import "time"

// RunStatus represents the lifecycle state of a run.
type RunStatus string

const (
	RunStatusQueued      RunStatus = "queued"
	RunStatusRunning     RunStatus = "running"
	RunStatusSucceeded   RunStatus = "succeeded"
	RunStatusFailed      RunStatus = "failed"
	RunStatusInfraFailed RunStatus = "infra-failed"
	RunStatusNoOp        RunStatus = "no-op"
	RunStatusBlocked     RunStatus = "blocked"
	RunStatusCancelled   RunStatus = "cancelled"
)

// Known reports whether status is part of the current public vocabulary.
func (s RunStatus) Known() bool {
	switch s {
	case RunStatusQueued,
		RunStatusRunning,
		RunStatusSucceeded,
		RunStatusFailed,
		RunStatusInfraFailed,
		RunStatusNoOp,
		RunStatusBlocked,
		RunStatusCancelled:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further runner progress is expected.
func (s RunStatus) Terminal() bool {
	switch s {
	case RunStatusSucceeded,
		RunStatusFailed,
		RunStatusInfraFailed,
		RunStatusNoOp,
		RunStatusBlocked,
		RunStatusCancelled:
		return true
	default:
		return false
	}
}

// Success reports whether the status represents a completed successful run.
func (s RunStatus) Success() bool {
	return s == RunStatusSucceeded
}

// ReviewEligible reports whether the run may proceed to PR review ingestion.
func (s RunStatus) ReviewEligible() bool {
	return s == RunStatusSucceeded
}

// Run is the atomic unit of execution: one container == one run.
type Run struct {
	ID           string            `json:"id"`
	Status       RunStatus         `json:"status"`
	Image        string            `json:"image"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	ContainerID  string            `json:"container_id,omitempty"`
	ExternalURL  string            `json:"external_url,omitempty"`
	ExitCode     int               `json:"exit_code,omitempty"`
	Logs         string            `json:"logs,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	FinishedAt   *time.Time        `json:"finished_at,omitempty"`
	IssueRef     string            `json:"issue_ref,omitempty"`
	BranchRef    string            `json:"branch_ref,omitempty"`
	PRRef        string            `json:"pr_ref,omitempty"`
	HeadSHA      string            `json:"head_sha,omitempty"`
	TaskID       string            `json:"task_id,omitempty"`
	EvidencePath string            `json:"evidence_path,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// Actor identifies who triggered an operation.
type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Source identifies the originating app.
type Source struct {
	App     string `json:"app"`
	Version string `json:"version"`
}

// Target identifies what the operation acts upon.
type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Retry tracks retry state.
type Retry struct {
	MaxAttempts    int `json:"maxAttempts"`
	CurrentAttempt int `json:"currentAttempt"`
}

// OperationEnvelope wraps every side-effecting operation with idempotency metadata.
type OperationEnvelope struct {
	OperationID    string    `json:"operationId"`
	IdempotencyKey string    `json:"idempotencyKey"`
	Actor          Actor     `json:"actor"`
	Source         Source    `json:"source"`
	Target         Target    `json:"target"`
	Intent         string    `json:"intent"`
	PayloadHash    string    `json:"payloadHash"`
	CorrelationID  string    `json:"correlationId"`
	TraceID        string    `json:"traceId"`
	CreatedAt      time.Time `json:"createdAt"`
	Retry          Retry     `json:"retry"`
}

// RunEvent is emitted on lifecycle transitions.
type RunEvent struct {
	TraceID   string    `json:"traceId"`
	RunID     string    `json:"runId"`
	From      RunStatus `json:"from"`
	To        RunStatus `json:"to"`
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason,omitempty"`
}

// SubmitRunRequest is the payload for creating a new run.
type SubmitRunRequest struct {
	Image          string            `json:"image"`
	Command        []string          `json:"command,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	IssueRef       string            `json:"issue_ref,omitempty"`
	BranchRef      string            `json:"branch_ref,omitempty"`
	PRRef          string            `json:"pr_ref,omitempty"`
	HeadSHA        string            `json:"head_sha,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
}

// HealthResponse is returned by the health endpoint.
type HealthResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	GitSHA    string            `json:"git_sha,omitempty"`
	ImageTag  string            `json:"image_tag,omitempty"`
	BuildTime string            `json:"build_time,omitempty"`
	NodeID    string            `json:"node_id,omitempty"`
	Leader    bool              `json:"leader"`
	Executor  string            `json:"executor,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Peers     map[string]string `json:"peers,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Capacity  Capacity          `json:"capacity"`
}

// Capacity shows current concurrency usage.
type Capacity struct {
	Max       int `json:"max"`
	Running   int `json:"running"`
	Queued    int `json:"queued"`
	Completed int `json:"completed"`
}
