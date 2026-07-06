package orchestrator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	SubagentWorkerKind   = "subagent"
	HandoffStateIssued   = "issued"
	HandoffStateReturned = "returned"
)

// Handoff is the OR5 packet passed from a parent run to a subagent run.
type Handoff struct {
	HandoffID       string   `json:"handoff_id"`
	ParentRunID     string   `json:"parent_run_id"`
	ChildRunID      string   `json:"child_run_id,omitempty"`
	Title           string   `json:"title"`
	Body            string   `json:"body,omitempty"`
	ExpectedOutputs []string `json:"expected_outputs,omitempty"`
	Validation      []string `json:"validation,omitempty"`
	State           string   `json:"state,omitempty"`
	ArtifactRef     string   `json:"artifact_ref,omitempty"`
}

// SwarmStore is a minimal filesystem Kanban backing the orchestration role.
// It stores run, task, and handoff JSON under <root>/{runs,tasks,handoffs}
// so the orchestration role can manage the full swarm state.
type SwarmStore struct {
	Root string
}

// DefaultSwarmPath returns ~/.rommie/orchestration.
func DefaultSwarmPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".rommie", "orchestration")
}

// NewSwarmStore creates a store rooted at root.
func NewSwarmStore(root string) *SwarmStore {
	return &SwarmStore{Root: root}
}

func (s *SwarmStore) dir(name string) string {
	return filepath.Join(s.Root, name)
}

func (s *SwarmStore) writeJSON(name, id string, v any) error {
	path := s.dir(name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("orchestrator: create %s dir: %w", name, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("orchestrator: marshal %s: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(path, id+".json"), data, 0o644); err != nil {
		return fmt.Errorf("orchestrator: write %s: %w", name, err)
	}
	return nil
}

func (s *SwarmStore) readJSON(name, id string, v any) error {
	data, err := os.ReadFile(filepath.Join(s.dir(name), id+".json"))
	if err != nil {
		return fmt.Errorf("orchestrator: read %s %s: %w", name, id, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("orchestrator: unmarshal %s %s: %w", name, id, err)
	}
	return nil
}

// listJSON loads all JSON files of a given kind and decodes them into out,
// which must be a pointer to a slice of the target type.
func (s *SwarmStore) listJSON(name string, out any) error {
	entries, err := os.ReadDir(s.dir(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rv, ok := out.(*[]WorkerRun)
	if !ok {
		return fmt.Errorf("orchestrator: unsupported list kind %s", name)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		var run WorkerRun
		if err := s.readJSON(name, id, &run); err != nil {
			return err
		}
		*rv = append(*rv, run)
	}
	return nil
}

// WriteRun persists a worker run under runs/.
func (s *SwarmStore) WriteRun(run WorkerRun) error {
	return s.writeJSON("runs", run.WorkerRunID, run)
}

// ReadRun loads a worker run by ID.
func (s *SwarmStore) ReadRun(id string) (WorkerRun, error) {
	var run WorkerRun
	if err := s.readJSON("runs", id, &run); err != nil {
		return WorkerRun{}, err
	}
	return run, nil
}

// ListRuns returns all persisted worker runs sorted by ID.
func (s *SwarmStore) ListRuns() ([]WorkerRun, error) {
	runs := []WorkerRun{}
	if err := s.listJSON("runs", &runs); err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].WorkerRunID < runs[j].WorkerRunID
	})
	return runs, nil
}

// WriteTask persists a work unit under tasks/.
func (s *SwarmStore) WriteTask(task WorkUnit) error {
	return s.writeJSON("tasks", task.WorkUnitID, task)
}

// WriteHandoff persists a handoff packet under handoffs/.
func (s *SwarmStore) WriteHandoff(h Handoff) error {
	return s.writeJSON("handoffs", h.HandoffID, h)
}

// ReadHandoff loads a handoff packet by ID.
func (s *SwarmStore) ReadHandoff(id string) (Handoff, error) {
	var h Handoff
	if err := s.readJSON("handoffs", id, &h); err != nil {
		return Handoff{}, err
	}
	return h, nil
}

// ListTasks returns all persisted task work units sorted by ID.
func (s *SwarmStore) ListTasks() ([]WorkUnit, error) {
	entries, err := os.ReadDir(s.dir("tasks"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []WorkUnit
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		var task WorkUnit
		if err := s.readJSON("tasks", id, &task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].WorkUnitID < tasks[j].WorkUnitID
	})
	return tasks, nil
}

// ListHandoffs returns all persisted handoff packets sorted by ID.
func (s *SwarmStore) ListHandoffs() ([]Handoff, error) {
	entries, err := os.ReadDir(s.dir("handoffs"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var handoffs []Handoff
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		var h Handoff
		if err := s.readJSON("handoffs", id, &h); err != nil {
			return nil, err
		}
		handoffs = append(handoffs, h)
	}
	sort.Slice(handoffs, func(i, j int) bool {
		return handoffs[i].HandoffID < handoffs[j].HandoffID
	})
	return handoffs, nil
}

// HandoffSpec describes work delegated from a parent run to a subagent.
type HandoffSpec struct {
	Title           string   `json:"title"`
	Body            string   `json:"body,omitempty"`
	ExpectedOutputs []string `json:"expected_outputs,omitempty"`
	Validation      []string `json:"validation,omitempty"`
	Nonce           string   `json:"nonce,omitempty"`
}

func newNonce() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("orchestrator: generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// subagentIDs returns a unique work-unit ID and the ledger-aligned run ID.
func subagentIDs(spec HandoffSpec) (workUnitID, runID string, err error) {
	nonce := spec.Nonce
	if nonce == "" {
		var err error
		nonce, err = newNonce()
		if err != nil {
			return "", "", err
		}
	}
	workUnitID = "wu_sub_" + nonce
	runID = workerRunID(workUnitID, SubagentWorkerKind)
	return workUnitID, runID, nil
}

// SpawnSubagent creates a subagent work unit, materializes it through the
// existing dispatch ledger (ObserveRuns), persists the issued task/handoff/run
// under the swarm store, and returns the child run.
func SpawnSubagent(parent WorkerRun, spec HandoffSpec, store *SwarmStore) (WorkUnit, Dispatch, WorkerRun, error) {
	if parent.WorkerRunID == "" {
		return WorkUnit{}, Dispatch{}, WorkerRun{}, fmt.Errorf("orchestrator: parent worker_run_id is required")
	}
	if parent.Repo == "" {
		return WorkUnit{}, Dispatch{}, WorkerRun{}, fmt.Errorf("orchestrator: parent repo is required to spawn subagent")
	}

	workUnitID, runID, err := subagentIDs(spec)
	if err != nil {
		return WorkUnit{}, Dispatch{}, WorkerRun{}, err
	}

	expected := spec.ExpectedOutputs
	if len(expected) == 0 {
		expected = []string{"artifact"}
	}

	task := WorkUnit{
		WorkUnitID:      workUnitID,
		ExternalID:      runID,
		Adapter:         SubagentWorkerKind,
		Repo:            parent.Repo,
		Stream:          parent.Stream,
		Title:           spec.Title,
		Body:            spec.Body,
		Labels:          []string{"ready"},
		WorkerKind:      SubagentWorkerKind,
		ExpectedOutputs: expected,
		Validation:      spec.Validation,
		State:           StateQueued,
		Metadata: map[string]json.RawMessage{
			"parent_run_id": []byte(fmt.Sprintf("%q", parent.WorkerRunID)),
			"handoff_id":    []byte(fmt.Sprintf("%q", runID)),
		},
	}

	dispatch := Dispatch{
		WorkUnitID:      task.WorkUnitID,
		ExternalID:      task.ExternalID,
		Stream:          task.Stream,
		WorkerKind:      SubagentWorkerKind,
		RepoCheckout:    RepoCheckout{Repo: task.Repo},
		Brief:           task.Title + "\n\n" + task.Body,
		ExpectedOutputs: task.ExpectedOutputs,
		Validation:      task.Validation,
		ParentRunID:     parent.WorkerRunID,
		Metadata: map[string]any{
			"handoff_id": runID,
		},
	}

	now := time.Now().UTC()
	preliminary := WorkerRun{
		WorkerRunID:     runID,
		WorkUnitID:      workUnitID,
		ExternalID:      runID,
		Stream:          task.Stream,
		Repo:            task.Repo,
		WorkerKind:      SubagentWorkerKind,
		State:           StateQueued,
		StartedAt:       formatTime(now),
		LastHeartbeatAt: formatTime(now),
		ParentRunID:     parent.WorkerRunID,
		HandoffID:       runID,
	}

	// Materialize through the existing dispatch ledger so the child is
	// observable by scheduler/heartbeat/result paths.
	observed, err := ObserveRuns(ObserveRunsRequest{
		Dispatches:              []Dispatch{dispatch},
		WorkerRuns:              []WorkerRun{preliminary},
		Now:                     formatTime(now),
		HeartbeatTimeoutSeconds: DefaultHeartbeatSeconds,
	})
	if err != nil {
		return WorkUnit{}, Dispatch{}, WorkerRun{}, fmt.Errorf("orchestrator: observe subagent run: %w", err)
	}
	if len(observed.WorkerRuns) == 0 {
		return WorkUnit{}, Dispatch{}, WorkerRun{}, fmt.Errorf("orchestrator: subagent run was not materialized")
	}
	childRun := observed.WorkerRuns[0]

	if store != nil {
		if err := store.WriteTask(task); err != nil {
			return WorkUnit{}, Dispatch{}, WorkerRun{}, err
		}
		if err := store.WriteHandoff(Handoff{
			HandoffID:       runID,
			ParentRunID:     parent.WorkerRunID,
			ChildRunID:      childRun.WorkerRunID,
			Title:           spec.Title,
			Body:            spec.Body,
			ExpectedOutputs: expected,
			Validation:      spec.Validation,
			State:           HandoffStateIssued,
		}); err != nil {
			return WorkUnit{}, Dispatch{}, WorkerRun{}, err
		}
		if err := store.WriteRun(childRun); err != nil {
			return WorkUnit{}, Dispatch{}, WorkerRun{}, err
		}
	}
	return task, dispatch, childRun, nil
}

// CollectSubagentHandoff records a returned handoff from a completed subagent.
// The result must be validated and must contain at least one artifact.
func CollectSubagentHandoff(child WorkerRun, result WorkerResult, store *SwarmStore) (Handoff, error) {
	if child.WorkerRunID == "" {
		return Handoff{}, fmt.Errorf("orchestrator: child worker_run_id is required")
	}
	if child.ParentRunID == "" {
		return Handoff{}, fmt.Errorf("orchestrator: child has no parent_run_id")
	}
	if result.WorkerRunID != "" && result.WorkerRunID != child.WorkerRunID {
		return Handoff{}, fmt.Errorf("orchestrator: result worker_run_id %s does not match child %s", result.WorkerRunID, child.WorkerRunID)
	}
	if result.WorkUnitID != "" && result.WorkUnitID != child.WorkUnitID {
		return Handoff{}, fmt.Errorf("orchestrator: result work_unit_id %s does not match child %s", result.WorkUnitID, child.WorkUnitID)
	}
	if result.State != StateValidated {
		return Handoff{}, fmt.Errorf("orchestrator: subagent result must be %s, got %s", StateValidated, result.State)
	}
	if len(result.ArtifactRefs) == 0 || result.ArtifactRefs[0] == "" {
		return Handoff{}, fmt.Errorf("orchestrator: subagent must return a validated artifact")
	}

	handoffID := child.HandoffID
	if handoffID == "" {
		handoffID = child.WorkerRunID
	}

	var existing Handoff
	if store != nil {
		if h, err := store.ReadHandoff(handoffID); err == nil {
			existing = h
		}
	}
	if existing.HandoffID == "" {
		existing.HandoffID = handoffID
		existing.ParentRunID = child.ParentRunID
		existing.ChildRunID = child.WorkerRunID
	}

	returned := Handoff{
		HandoffID:       handoffID,
		ParentRunID:     child.ParentRunID,
		ChildRunID:      child.WorkerRunID,
		Title:           existing.Title,
		Body:            existing.Body,
		ExpectedOutputs: existing.ExpectedOutputs,
		Validation:      existing.Validation,
		State:           HandoffStateReturned,
		ArtifactRef:     result.ArtifactRefs[0],
	}

	if store != nil {
		if err := store.WriteHandoff(returned); err != nil {
			return Handoff{}, err
		}
		if run, err := store.ReadRun(child.WorkerRunID); err == nil {
			run.State = StateValidated
			run.ArtifactRefs = append([]string{}, result.ArtifactRefs...)
			if err := store.WriteRun(run); err != nil {
				return Handoff{}, err
			}
		}
	}
	return returned, nil
}

// SubagentWorker performs the actual sub-work for a handoff and returns a
// validated artifact reference (e.g. a branch name or commit ref).
type SubagentWorker func(Handoff) (string, error)

// ExecuteSubagent runs the subagent brain for a spawned child: it loads the
// issued handoff, marks the run running, performs the sub-work, validates the
// returned artifact, and records the result in the store.
func ExecuteSubagent(child WorkerRun, worker SubagentWorker, store *SwarmStore) (WorkerResult, error) {
	if child.WorkerRunID == "" {
		return WorkerResult{}, fmt.Errorf("orchestrator: child worker_run_id is required")
	}
	if child.ParentRunID == "" {
		return WorkerResult{}, fmt.Errorf("orchestrator: child has no parent_run_id")
	}
	if worker == nil {
		return WorkerResult{}, fmt.Errorf("orchestrator: subagent worker is required")
	}

	handoffID := child.HandoffID
	if handoffID == "" {
		handoffID = child.WorkerRunID
	}

	var handoff Handoff
	if store != nil {
		if h, err := store.ReadHandoff(handoffID); err == nil {
			handoff = h
		}
	}
	if handoff.HandoffID == "" {
		handoff.HandoffID = handoffID
		handoff.ParentRunID = child.ParentRunID
		handoff.ChildRunID = child.WorkerRunID
	}

	if store != nil {
		if run, err := store.ReadRun(child.WorkerRunID); err == nil {
			run.State = StateRunning
			run.LastHeartbeatAt = formatTime(time.Now().UTC())
			if err := store.WriteRun(run); err != nil {
				return WorkerResult{}, err
			}
		}
	}

	artifact, err := worker(handoff)
	if err != nil || artifact == "" {
		result := WorkerResult{
			WorkUnitID:    child.WorkUnitID,
			WorkerRunID:   child.WorkerRunID,
			State:         StateFailed,
			BlockedReason: "subagent_work_failed",
		}
		if store != nil {
			if run, err := store.ReadRun(child.WorkerRunID); err == nil {
				run.State = StateFailed
				run.BlockedReason = result.BlockedReason
				_ = store.WriteRun(run)
			}
		}
		if err != nil {
			return result, err
		}
		return result, fmt.Errorf("orchestrator: subagent returned empty artifact")
	}

	result := WorkerResult{
		WorkUnitID:    child.WorkUnitID,
		WorkerRunID:   child.WorkerRunID,
		State:         StateValidated,
		ArtifactRefs:  []string{artifact},
	}
	if _, err := CollectSubagentHandoff(child, result, store); err != nil {
		return WorkerResult{}, err
	}
	return result, nil
}

// SwarmExecutor drives spawned subagents through the scheduler/execution path.
type SwarmExecutor struct {
	Store  *SwarmStore
	Worker SubagentWorker
}

// RunOnce schedules all queued subagent tasks and executes those selected by
// the scheduler. It returns the worker results produced this tick.
func (e *SwarmExecutor) RunOnce(stream StreamDeclaration, caps ScheduleCaps) ([]WorkerResult, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("orchestrator: executor store is required")
	}
	if e.Worker == nil {
		return nil, fmt.Errorf("orchestrator: executor worker is required")
	}

	tasks, err := e.Store.ListTasks()
	if err != nil {
		return nil, err
	}
	units := make([]WorkUnit, 0, len(tasks))
	for _, task := range tasks {
		if task.WorkerKind != SubagentWorkerKind {
			continue
		}
		// Only consider work that has not already finished.
		if run, err := e.Store.ReadRun(task.ExternalID); err == nil && isTerminal(run.State) {
			continue
		}
		unit := task
		unit.State = StateQueued
		units = append(units, unit)
	}

	resp, err := Schedule(ScheduleRequest{
		Streams:   []StreamDeclaration{stream},
		WorkUnits: units,
		Caps:      caps,
	})
	if err != nil {
		return nil, err
	}

	var results []WorkerResult
	for _, d := range resp.Dispatches {
		if d.WorkerKind != SubagentWorkerKind {
			continue
		}
		run, err := e.Store.ReadRun(d.ExternalID)
		if err != nil {
			continue
		}
		result, err := ExecuteSubagent(run, e.Worker, e.Store)
		if err != nil {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// SwarmNode is one node in the recursive swarm brain view.
type SwarmNode struct {
	WorkerRunID string      `json:"worker_run_id"`
	WorkUnitID  string      `json:"work_unit_id,omitempty"`
	ParentRunID string      `json:"parent_run_id,omitempty"`
	Depth       int         `json:"depth"`
	Children    []SwarmNode `json:"children,omitempty"`
}

// SwarmView is the root-level brain view of the swarm.
type SwarmView struct {
	RootRuns []SwarmNode `json:"root_runs"`
}

// BuildSwarmView builds a parent-child tree from worker runs.
func BuildSwarmView(runs []WorkerRun) SwarmView {
	children := map[string][]WorkerRun{}
	var roots []WorkerRun
	for _, run := range runs {
		if run.ParentRunID == "" {
			roots = append(roots, run)
		} else {
			children[run.ParentRunID] = append(children[run.ParentRunID], run)
		}
	}
	var build func(parentID string, depth int) []SwarmNode
	build = func(parentID string, depth int) []SwarmNode {
		kids := children[parentID]
		sort.Slice(kids, func(i, j int) bool {
			return kids[i].WorkerRunID < kids[j].WorkerRunID
		})
		var nodes []SwarmNode
		for _, kid := range kids {
			nodes = append(nodes, SwarmNode{
				WorkerRunID: kid.WorkerRunID,
				WorkUnitID:  kid.WorkUnitID,
				ParentRunID: kid.ParentRunID,
				Depth:       depth,
				Children:    build(kid.WorkerRunID, depth+1),
			})
		}
		return nodes
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].WorkerRunID < roots[j].WorkerRunID
	})
	view := SwarmView{RootRuns: make([]SwarmNode, 0, len(roots))}
	for _, root := range roots {
		view.RootRuns = append(view.RootRuns, SwarmNode{
			WorkerRunID: root.WorkerRunID,
			WorkUnitID:  root.WorkUnitID,
			ParentRunID: root.ParentRunID,
			Depth:       0,
			Children:    build(root.WorkerRunID, 1),
		})
	}
	return view
}
