package orchestrator

import "testing"

func TestObserveRunsMaterializesDispatches(t *testing.T) {
	dispatch := Dispatch{
		WorkUnitID: "wu_1",
		ExternalID: "github:marius-patrik/repo#1",
		Stream:     "features",
		WorkerKind: DefaultWorkerKind,
	}
	resp, err := ObserveRuns(ObserveRunsRequest{
		Now:        "2026-07-04T12:00:00Z",
		Dispatches: []Dispatch{dispatch},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(resp.WorkerRuns), 1; got != want {
		t.Fatalf("worker runs = %d, want %d", got, want)
	}
	run := resp.WorkerRuns[0]
	if run.WorkerRunID == "" {
		t.Fatal("worker_run_id was empty")
	}
	if got, want := run.State, StateQueued; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	if got, want := run.StartedAt, "2026-07-04T12:00:00Z"; got != want {
		t.Fatalf("started_at = %s, want %s", got, want)
	}
	if got, want := resp.StatusEvents[0].Kind, "worker_run_materialized"; got != want {
		t.Fatalf("event kind = %s, want %s", got, want)
	}
}

func TestObserveRunsFoldsWorkerResults(t *testing.T) {
	runID := workerRunID("wu_2", DefaultWorkerKind)
	resp, err := ObserveRuns(ObserveRunsRequest{
		Now: "2026-07-04T12:05:00Z",
		WorkerRuns: []WorkerRun{{
			WorkerRunID: runID,
			WorkUnitID:  "wu_2",
			ExternalID:  "github:marius-patrik/repo#2",
			WorkerKind:  DefaultWorkerKind,
			State:       StateRunning,
		}},
		Results: []WorkerResult{{
			WorkUnitID:    "wu_2",
			State:         StateSucceeded,
			ArtifactRefs:  []string{"https://github.com/marius-patrik/repo/pull/2"},
			OwnerQuestion: "Approve merge?",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.WorkerRuns[0].State, StateSucceeded; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	if got, want := resp.WorkerRuns[0].ArtifactRefs[0], "https://github.com/marius-patrik/repo/pull/2"; got != want {
		t.Fatalf("artifact = %s, want %s", got, want)
	}
	if got, want := len(resp.OwnerQuestions), 1; got != want {
		t.Fatalf("owner questions = %d, want %d", got, want)
	}
	if got, want := resp.StatusEvents[0].Kind, "worker_result_recorded"; got != want {
		t.Fatalf("event kind = %s, want %s", got, want)
	}
}

func TestObserveRunsDetectsMissingHeartbeat(t *testing.T) {
	resp, err := ObserveRuns(ObserveRunsRequest{
		Now:                     "2026-07-04T12:10:01Z",
		HeartbeatTimeoutSeconds: 300,
		WorkerRuns: []WorkerRun{{
			WorkerRunID:     "wr_stale",
			WorkUnitID:      "wu_3",
			ExternalID:      "github:marius-patrik/repo#3",
			WorkerKind:      DefaultWorkerKind,
			State:           StateRunning,
			LastHeartbeatAt: "2026-07-04T12:05:00Z",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.WorkerRuns[0].State, StateHeartbeatMissing; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	if got, want := resp.StatusEvents[0].Kind, "worker_heartbeat_missing"; got != want {
		t.Fatalf("event kind = %s, want %s", got, want)
	}
}
