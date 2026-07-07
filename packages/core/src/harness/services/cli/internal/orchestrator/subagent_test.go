package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func makeTestWorker(artifact string) SubagentWorker {
	return func(h Handoff) (string, error) {
		if h.HandoffID == "" {
			return "", errors.New("missing handoff")
		}
		return artifact, nil
	}
}

func TestSubagentSpawnAndReturn(t *testing.T) {
	tmp := t.TempDir()
	store := NewSwarmStore(tmp)

	parent := WorkerRun{
		WorkerRunID: "wr_parent",
		WorkUnitID:  "wu_parent",
		Stream:      "engine",
		Repo:        "marius-patrik/agents-harness",
		WorkerKind:  "implementer",
		State:       StateRunning,
	}
	spec := HandoffSpec{
		Title:           "Add non-progress review types",
		Body:            "Define review request/response structs.",
		ExpectedOutputs: []string{"branch"},
		Validation:      []string{"go test"},
	}

	task, dispatch, childRun, err := SpawnSubagent(parent, spec, store)
	if err != nil {
		t.Fatal(err)
	}
	if task.WorkerKind != SubagentWorkerKind {
		t.Fatalf("task worker kind = %s, want %s", task.WorkerKind, SubagentWorkerKind)
	}
	if dispatch.ParentRunID != parent.WorkerRunID {
		t.Fatalf("dispatch parent = %s, want %s", dispatch.ParentRunID, parent.WorkerRunID)
	}
	if childRun.ParentRunID != parent.WorkerRunID {
		t.Fatalf("child parent = %s, want %s", childRun.ParentRunID, parent.WorkerRunID)
	}
	if childRun.HandoffID == "" {
		t.Fatal("child run missing handoff_id")
	}

	// Execute the subagent brain: it takes the handoff and returns a validated artifact.
	result, err := ExecuteSubagent(childRun, makeTestWorker("refs/pull/1373/head"), store)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateValidated {
		t.Fatalf("result state = %s, want %s", result.State, StateValidated)
	}

	returned, err := store.ReadHandoff(childRun.HandoffID)
	if err != nil {
		t.Fatal(err)
	}
	if returned.State != HandoffStateReturned {
		t.Fatalf("returned state = %s, want %s", returned.State, HandoffStateReturned)
	}
	if returned.ArtifactRef != "refs/pull/1373/head" {
		t.Fatalf("artifact = %s, want refs/pull/1373/head", returned.ArtifactRef)
	}
	if returned.ParentRunID != parent.WorkerRunID {
		t.Fatalf("returned parent = %s, want %s", returned.ParentRunID, parent.WorkerRunID)
	}
	if returned.Title != spec.Title {
		t.Fatalf("returned handoff dropped title: %s", returned.Title)
	}

	validatedRun, err := store.ReadRun(childRun.WorkerRunID)
	if err != nil {
		t.Fatal(err)
	}
	if validatedRun.State != StateValidated {
		t.Fatalf("run state = %s, want %s", validatedRun.State, StateValidated)
	}
}

func TestSubagentTwoLevelRecursion(t *testing.T) {
	tmp := t.TempDir()
	store := NewSwarmStore(tmp)

	root := WorkerRun{
		WorkerRunID: "wr_root",
		WorkUnitID:  "wu_root",
		Stream:      "engine",
		Repo:        "marius-patrik/agents-harness",
		WorkerKind:  "implementer",
		State:       StateRunning,
	}

	// Root spawns child.
	_, _, childRun, err := SpawnSubagent(root, HandoffSpec{
		Title: "Plan subagent store",
		Body:  "Design Kanban layout.",
	}, store)
	if err != nil {
		t.Fatal(err)
	}

	// Child spawns grandchild.
	_, _, grandchildRun, err := SpawnSubagent(childRun, HandoffSpec{
		Title: "Implement file store",
		Body:  "Write tasks and handoffs to disk.",
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if grandchildRun.ParentRunID != childRun.WorkerRunID {
		t.Fatalf("grandchild parent = %s, want %s", grandchildRun.ParentRunID, childRun.WorkerRunID)
	}

	// Execute grandchild -> child -> root.
	if _, err := ExecuteSubagent(grandchildRun, makeTestWorker("grandchild-artifact"), store); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteSubagent(childRun, makeTestWorker("child-artifact"), store); err != nil {
		t.Fatal(err)
	}

	handoffs, err := store.ListHandoffs()
	if err != nil {
		t.Fatal(err)
	}
	returned := 0
	for _, h := range handoffs {
		if h.State == HandoffStateReturned {
			returned++
		}
	}
	if returned != 2 {
		t.Fatalf("returned handoffs = %d, want 2", returned)
	}

	view := BuildSwarmView([]WorkerRun{root, childRun, grandchildRun})
	if len(view.RootRuns) != 1 || len(view.RootRuns[0].Children) != 1 || len(view.RootRuns[0].Children[0].Children) != 1 {
		t.Fatalf("brain view does not show 2-level recursion: %+v", view)
	}
}

func TestSwarmExecutorRunOnce(t *testing.T) {
	tmp := t.TempDir()
	store := NewSwarmStore(tmp)

	parent := WorkerRun{
		WorkerRunID: "wr_exec_parent",
		WorkUnitID:  "wu_exec_parent",
		Stream:      "engine",
		Repo:        "marius-patrik/agents-harness",
		WorkerKind:  "implementer",
		State:       StateRunning,
	}
	_, _, childRun, err := SpawnSubagent(parent, HandoffSpec{
		Title: "Executor task",
		Body:  "Run through scheduler.",
	}, store)
	if err != nil {
		t.Fatal(err)
	}

	exec := SwarmExecutor{
		Store:  store,
		Worker: makeTestWorker("executor-artifact"),
	}
	results, err := exec.RunOnce(StreamDeclaration{
		StreamID:    "engine",
		ReadyLabels: []string{"ready"},
		MaxInFlight: 10,
	}, ScheduleCaps{MaxDispatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].State != StateValidated {
		t.Fatalf("result state = %s, want %s", results[0].State, StateValidated)
	}

	returned, err := store.ReadHandoff(childRun.HandoffID)
	if err != nil {
		t.Fatal(err)
	}
	if returned.ArtifactRef != "executor-artifact" {
		t.Fatalf("artifact = %s, want executor-artifact", returned.ArtifactRef)
	}
}

func TestBuildSwarmView(t *testing.T) {
	runs := []WorkerRun{
		{WorkerRunID: "wr_root", WorkUnitID: "wu_root"},
		{WorkerRunID: "wr_c1", WorkUnitID: "wu_c1", ParentRunID: "wr_root"},
		{WorkerRunID: "wr_c2", WorkUnitID: "wu_c2", ParentRunID: "wr_root"},
		{WorkerRunID: "wr_g1", WorkUnitID: "wu_g1", ParentRunID: "wr_c1"},
	}
	view := BuildSwarmView(runs)
	if len(view.RootRuns) != 1 {
		t.Fatalf("roots = %d, want 1", len(view.RootRuns))
	}
	root := view.RootRuns[0]
	if root.WorkerRunID != "wr_root" || root.Depth != 0 {
		t.Fatalf("root = %+v", root)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d, want 2", len(root.Children))
	}
	c1 := root.Children[0]
	if c1.WorkerRunID != "wr_c1" || c1.Depth != 1 || len(c1.Children) != 1 {
		t.Fatalf("c1 = %+v", c1)
	}
	g1 := c1.Children[0]
	if g1.WorkerRunID != "wr_g1" || g1.Depth != 2 {
		t.Fatalf("g1 = %+v", g1)
	}
	c2 := root.Children[1]
	if c2.WorkerRunID != "wr_c2" || c2.Depth != 1 || len(c2.Children) != 0 {
		t.Fatalf("c2 = %+v", c2)
	}
}

func TestDefaultSwarmPathUsesHome(t *testing.T) {
	path := DefaultSwarmPath()
	if !filepath.IsAbs(path) {
		t.Fatalf("expected absolute path, got %s", path)
	}
	if _, last := filepath.Split(path); last != "orchestration" {
		t.Fatalf("path tail = %s, want orchestration", last)
	}
}

func TestSwarmStoreListEmpty(t *testing.T) {
	tmp := t.TempDir()
	store := NewSwarmStore(tmp)
	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0", len(tasks))
	}
	runs, err := store.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0", len(runs))
	}
}

func TestSpawnSubagentRequiresRepo(t *testing.T) {
	_, _, _, err := SpawnSubagent(WorkerRun{WorkerRunID: "wr_x"}, HandoffSpec{Title: "x"}, nil)
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
}

func TestSpawnSubagentNilStore(t *testing.T) {
	parent := WorkerRun{
		WorkerRunID: "wr_nil",
		WorkUnitID:  "wu_nil",
		Stream:      "engine",
		Repo:        "marius-patrik/agents-harness",
	}
	task, dispatch, childRun, err := SpawnSubagent(parent, HandoffSpec{Title: "No store"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.WorkUnitID == "" || dispatch.WorkUnitID == "" || childRun.WorkerRunID == "" {
		t.Fatal("expected IDs to be generated")
	}
	if childRun.State != StateQueued {
		t.Fatalf("child state = %s, want %s", childRun.State, StateQueued)
	}
	if _, err := os.Stat(filepath.Join(DefaultSwarmPath(), "tasks")); err == nil {
		t.Fatal("nil store should not persist files")
	}
}

func TestCollectSubagentHandoffRejectsInvalidResults(t *testing.T) {
	child := WorkerRun{
		WorkerRunID: "wr_child",
		WorkUnitID:  "wu_child",
		ParentRunID: "wr_parent",
		HandoffID:   "hf_child",
	}
	if _, err := CollectSubagentHandoff(child, WorkerResult{
		WorkUnitID:  child.WorkUnitID,
		WorkerRunID: child.WorkerRunID,
		State:       StateFailed,
	}, nil); err == nil {
		t.Fatal("expected error for failed result")
	}
	if _, err := CollectSubagentHandoff(child, WorkerResult{
		WorkUnitID:  child.WorkUnitID,
		WorkerRunID: child.WorkerRunID,
		State:       StateValidated,
	}, nil); err == nil {
		t.Fatal("expected error for missing artifact")
	}
	if _, err := CollectSubagentHandoff(child, WorkerResult{
		WorkUnitID:  child.WorkUnitID,
		WorkerRunID: "wr_other",
		State:       StateValidated,
		ArtifactRefs: []string{"x"},
	}, nil); err == nil {
		t.Fatal("expected error for mismatched worker_run_id")
	}
}

func TestExecuteSubagentFailsOnWorkerError(t *testing.T) {
	tmp := t.TempDir()
	store := NewSwarmStore(tmp)
	parent := WorkerRun{
		WorkerRunID: "wr_fail",
		WorkUnitID:  "wu_fail",
		Stream:      "engine",
		Repo:        "marius-patrik/agents-harness",
		WorkerKind:  "implementer",
		State:       StateRunning,
	}
	_, _, childRun, err := SpawnSubagent(parent, HandoffSpec{Title: "Fail"}, store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ExecuteSubagent(childRun, func(Handoff) (string, error) {
		return "", errors.New("simulated failure")
	}, store)
	if err == nil {
		t.Fatal("expected error from failing worker")
	}
	failedRun, err := store.ReadRun(childRun.WorkerRunID)
	if err != nil {
		t.Fatal(err)
	}
	if failedRun.State != StateFailed {
		t.Fatalf("run state = %s, want %s", failedRun.State, StateFailed)
	}
}

func TestSubagentUnboundedFanOut(t *testing.T) {
	tmp := t.TempDir()
	store := NewSwarmStore(tmp)
	parent := WorkerRun{
		WorkerRunID: "wr_fan",
		WorkUnitID:  "wu_fan",
		Stream:      "engine",
		Repo:        "marius-patrik/agents-harness",
		WorkerKind:  "implementer",
		State:       StateRunning,
	}
	spec := HandoffSpec{Title: "Parallel task", Body: "Do work"}

	ids := map[string]bool{}
	for i := 0; i < 5; i++ {
		_, _, childRun, err := SpawnSubagent(parent, spec, store)
		if err != nil {
			t.Fatal(err)
		}
		if ids[childRun.WorkerRunID] {
			t.Fatalf("duplicate child run ID %s", childRun.WorkerRunID)
		}
		ids[childRun.WorkerRunID] = true
	}
	if len(ids) != 5 {
		t.Fatalf("spawned %d unique children, want 5", len(ids))
	}
	runs, err := store.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 5 {
		t.Fatalf("persisted runs = %d, want 5", len(runs))
	}
}

func TestSubagentRetryUsesDifferentNonce(t *testing.T) {
	tmp := t.TempDir()
	store := NewSwarmStore(tmp)
	parent := WorkerRun{
		WorkerRunID: "wr_retry",
		WorkUnitID:  "wu_retry",
		Stream:      "engine",
		Repo:        "marius-patrik/agents-harness",
		WorkerKind:  "implementer",
		State:       StateRunning,
	}
	spec := HandoffSpec{Title: "Retry task", Body: "Same body", Nonce: "first"}
	_, _, first, err := SpawnSubagent(parent, spec, store)
	if err != nil {
		t.Fatal(err)
	}
	spec.Nonce = "second"
	_, _, second, err := SpawnSubagent(parent, spec, store)
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkerRunID == second.WorkerRunID {
		t.Fatal("expected different run IDs for different nonces")
	}
}
