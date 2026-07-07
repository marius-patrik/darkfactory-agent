// Package orchestrator implements the harness-owned adapter scheduling
// contract. It is intentionally transport-light: callers can submit the same
// JSON shape over the CLI today and promote it to agents-core protobuf later.
package orchestrator

import "encoding/json"

const (
	StateQueued             = "queued"
	StateReady              = "ready"
	StateRunning            = "running"
	StateBlocked            = "blocked"
	StateNeedsOwner         = "needs_owner"
	StateNeedsReview        = "needs_review"
	StateValidated          = "validated"
	StateMerged             = "merged"
	StateFailed             = "failed"
	StateKilledNonProgress  = "killed_non_progress"
	StateSucceeded          = "succeeded"
	StateHeartbeatMissing   = "heartbeat_missing"
	DefaultWorkerKind       = "implementer"
	DefaultExpectedArtifact = "branch"
	DefaultTargetBase       = "dev"
	DefaultHeartbeatSeconds = 300
	DefaultNoArtifactTurns  = 5
	DefaultRepeatHashLimit  = 3
)

type ScheduleRequest struct {
	Streams      []StreamDeclaration `json:"streams"`
	WorkUnits    []WorkUnit          `json:"work_units"`
	Observations []Observation       `json:"observations,omitempty"`
	Caps         ScheduleCaps        `json:"caps,omitempty"`
	Now          string              `json:"now,omitempty"`
}

type ScheduleCaps struct {
	MaxDispatches int            `json:"max_dispatches,omitempty"`
	MaxInFlight   int            `json:"max_in_flight,omitempty"`
	PerStream     map[string]int `json:"per_stream,omitempty"`
}

type StreamDeclaration struct {
	StreamID        string   `json:"stream_id"`
	RepoScope       string   `json:"repo_scope,omitempty"`
	PriorityOrder   []string `json:"priority_order,omitempty"`
	MaxInFlight     int      `json:"max_in_flight,omitempty"`
	MaxPerRepo      int      `json:"max_per_repo,omitempty"`
	WaveGates       []string `json:"wave_gates,omitempty"`
	ReadyLabels     []string `json:"ready_labels,omitempty"`
	BlockedLabels   []string `json:"blocked_labels,omitempty"`
	State           string   `json:"state,omitempty"`
	InFlightCount   int      `json:"in_flight_count,omitempty"`
	WaitingBlockers int      `json:"waiting_blockers,omitempty"`
	CurrentWave     string   `json:"current_wave,omitempty"`
}

type WorkUnit struct {
	WorkUnitID          string                     `json:"work_unit_id,omitempty"`
	Adapter             string                     `json:"adapter"`
	ExternalID          string                     `json:"external_id"`
	Repo                string                     `json:"repo"`
	Title               string                     `json:"title"`
	Body                string                     `json:"body,omitempty"`
	Acceptance          []string                   `json:"acceptance,omitempty"`
	Priority            string                     `json:"priority,omitempty"`
	Stream              string                     `json:"stream"`
	BlockedBy           []string                   `json:"blocked_by,omitempty"`
	Labels              []string                   `json:"labels,omitempty"`
	Branch              string                     `json:"branch,omitempty"`
	TargetBase          string                     `json:"target_base,omitempty"`
	Wave                string                     `json:"wave,omitempty"`
	ConcurrencyKey      string                     `json:"concurrency_key,omitempty"`
	Risk                string                     `json:"risk,omitempty"`
	OwnerQuestionPolicy string                     `json:"owner_question_policy,omitempty"`
	Deadline            string                     `json:"deadline,omitempty"`
	NotBefore           string                     `json:"not_before,omitempty"`
	WorkerKind          string                     `json:"worker_kind,omitempty"`
	ModelPolicy         map[string]any             `json:"model_policy,omitempty"`
	Validation          []string                   `json:"validation,omitempty"`
	ExpectedOutputs     []string                   `json:"expected_outputs,omitempty"`
	State               string                     `json:"state,omitempty"`
	Metadata            map[string]json.RawMessage `json:"metadata,omitempty"`
}

type Observation struct {
	Source          string          `json:"source"`
	ExternalEventID string          `json:"external_event_id"`
	Kind            string          `json:"kind"`
	WorkUnitID      string          `json:"work_unit_id,omitempty"`
	ExternalID      string          `json:"external_id,omitempty"`
	At              string          `json:"at,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type ScheduleResponse struct {
	Dispatches     []Dispatch      `json:"dispatches"`
	Blocked        []BlockedWork   `json:"blocked"`
	OwnerQuestions []OwnerQuestion `json:"owner_questions"`
	StatusEvents   []StatusEvent   `json:"status_events"`
}

type Dispatch struct {
	WorkUnitID      string         `json:"work_unit_id"`
	ExternalID      string         `json:"external_id"`
	Stream          string         `json:"stream"`
	Priority        string         `json:"priority,omitempty"`
	WorkerKind      string         `json:"worker_kind"`
	ModelPolicy     map[string]any `json:"model_policy,omitempty"`
	RepoCheckout    RepoCheckout   `json:"repo_checkout"`
	Brief           string         `json:"brief"`
	ExpectedOutputs []string       `json:"expected_outputs"`
	Validation      []string       `json:"validation,omitempty"`
	ParentRunID     string         `json:"parent_run_id,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type RepoCheckout struct {
	Repo   string `json:"repo"`
	Ref    string `json:"ref,omitempty"`
	Base   string `json:"base,omitempty"`
	Branch string `json:"branch,omitempty"`
}

type BlockedWork struct {
	WorkUnitID string   `json:"work_unit_id"`
	ExternalID string   `json:"external_id"`
	Stream     string   `json:"stream,omitempty"`
	Reasons    []Reason `json:"reasons"`
}

type Reason struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

type OwnerQuestion struct {
	WorkUnitID string `json:"work_unit_id"`
	ExternalID string `json:"external_id"`
	Question   string `json:"question"`
	Policy     string `json:"policy,omitempty"`
}

type StatusEvent struct {
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	WorkUnitID  string `json:"work_unit_id,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	Stream      string `json:"stream,omitempty"`
	WorkerRunID string `json:"worker_run_id,omitempty"`
	Message     string `json:"message,omitempty"`
}

type ObserveRunsRequest struct {
	Dispatches              []Dispatch     `json:"dispatches,omitempty"`
	WorkerRuns              []WorkerRun    `json:"worker_runs,omitempty"`
	Results                 []WorkerResult `json:"results,omitempty"`
	Now                     string         `json:"now,omitempty"`
	HeartbeatTimeoutSeconds int            `json:"heartbeat_timeout_seconds,omitempty"`
}

type ObserveRunsResponse struct {
	WorkerRuns     []WorkerRun     `json:"worker_runs"`
	OwnerQuestions []OwnerQuestion `json:"owner_questions"`
	StatusEvents   []StatusEvent   `json:"status_events"`
}

type WorkerRun struct {
	WorkerRunID     string   `json:"worker_run_id"`
	WorkUnitID      string   `json:"work_unit_id"`
	ExternalID      string   `json:"external_id,omitempty"`
	Stream          string   `json:"stream,omitempty"`
	Repo            string   `json:"repo,omitempty"`
	WorkerKind      string   `json:"worker_kind"`
	State           string   `json:"state"`
	StartedAt       string   `json:"started_at,omitempty"`
	LastHeartbeatAt string   `json:"last_heartbeat_at,omitempty"`
	ArtifactRefs    []string `json:"artifact_refs,omitempty"`
	BlockedReason   string   `json:"blocked_reason,omitempty"`
	ReviewVerdict   string   `json:"review_verdict,omitempty"`
	ParentRunID     string   `json:"parent_run_id,omitempty"`
	HandoffID       string   `json:"handoff_id,omitempty"`
}

type WorkerResult struct {
	WorkUnitID    string   `json:"work_unit_id"`
	WorkerRunID   string   `json:"worker_run_id,omitempty"`
	State         string   `json:"state"`
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`
	Validation    []string `json:"validation,omitempty"`
	ReviewVerdict string   `json:"review_verdict,omitempty"`
	BlockedReason string   `json:"blocked_reason,omitempty"`
	OwnerQuestion string   `json:"owner_question,omitempty"`
}

type NonProgressRequest struct {
	WorkerRuns []WorkerRun       `json:"worker_runs,omitempty"`
	Samples    []ProgressSample  `json:"samples"`
	Config     NonProgressConfig `json:"config,omitempty"`
	Now        string            `json:"now,omitempty"`
}

type NonProgressConfig struct {
	MaxTurnsWithoutArtifact int `json:"max_turns_without_artifact,omitempty"`
	RepeatedStateHashLimit  int `json:"repeated_state_hash_limit,omitempty"`
}

type ProgressSample struct {
	WorkerRunID       string `json:"worker_run_id"`
	WorkUnitID        string `json:"work_unit_id,omitempty"`
	Turn              int    `json:"turn"`
	StateHash         string `json:"state_hash,omitempty"`
	ValidatedArtifact bool   `json:"validated_artifact,omitempty"`
	ArtifactRef       string `json:"artifact_ref,omitempty"`
}

type NonProgressResponse struct {
	Verdicts     []NonProgressVerdict `json:"verdicts"`
	StatusEvents []StatusEvent        `json:"status_events"`
}

type NonProgressVerdict struct {
	WorkerRunID string   `json:"worker_run_id"`
	WorkUnitID  string   `json:"work_unit_id,omitempty"`
	State       string   `json:"state"`
	Reasons     []Reason `json:"reasons,omitempty"`
}

// NonProgressReviewRequest carries the review-role decision for suspected runs.
// Decisions map a worker run ID to "kill" (confirm stuck) or "allow" (legitimate).
type NonProgressReviewRequest struct {
	WorkerRuns []WorkerRun          `json:"worker_runs,omitempty"`
	Verdicts   []NonProgressVerdict `json:"verdicts"`
	Decisions  map[string]string    `json:"decisions"`
}

// NonProgressReviewResponse emits kill results for confirmed stuck runs.
type NonProgressReviewResponse struct {
	Results      []WorkerResult `json:"results"`
	StatusEvents []StatusEvent  `json:"status_events"`
}
