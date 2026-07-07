package orchestrator

import "testing"

func TestDetectNonProgressFlagsNoValidatedArtifact(t *testing.T) {
	resp, err := DetectNonProgress(NonProgressRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_1", WorkUnitID: "wu_1", State: StateRunning}},
		Config:     NonProgressConfig{MaxTurnsWithoutArtifact: 3},
		Samples: []ProgressSample{
			{WorkerRunID: "wr_1", Turn: 1, StateHash: "a"},
			{WorkerRunID: "wr_1", Turn: 2, StateHash: "b"},
			{WorkerRunID: "wr_1", Turn: 3, StateHash: "c"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Verdicts[0].State, "needs_review"; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	if got, want := resp.Verdicts[0].Reasons[0].Code, "no_validated_artifact"; got != want {
		t.Fatalf("reason = %s, want %s", got, want)
	}
	if got, want := resp.StatusEvents[0].Kind, "non_progress_suspected"; got != want {
		t.Fatalf("event kind = %s, want %s", got, want)
	}
}

func TestDetectNonProgressFlagsRepeatedHash(t *testing.T) {
	resp, err := DetectNonProgress(NonProgressRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_2", WorkUnitID: "wu_2", State: StateRunning}},
		Config:     NonProgressConfig{MaxTurnsWithoutArtifact: 10, RepeatedStateHashLimit: 3},
		Samples: []ProgressSample{
			{WorkerRunID: "wr_2", Turn: 1, StateHash: "same"},
			{WorkerRunID: "wr_2", Turn: 2, StateHash: "same"},
			{WorkerRunID: "wr_2", Turn: 3, StateHash: "same"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reasons := reasonCodes(resp.Verdicts[0].Reasons)
	if !reasons["repeated_state_hash"] {
		t.Fatalf("missing repeated_state_hash in %#v", reasons)
	}
}

func TestDetectNonProgressAllowsRecentArtifact(t *testing.T) {
	resp, err := DetectNonProgress(NonProgressRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_3", WorkUnitID: "wu_3", State: StateRunning}},
		Config:     NonProgressConfig{MaxTurnsWithoutArtifact: 3, RepeatedStateHashLimit: 3},
		Samples: []ProgressSample{
			{WorkerRunID: "wr_3", Turn: 1, StateHash: "a"},
			{WorkerRunID: "wr_3", Turn: 2, StateHash: "b", ValidatedArtifact: true},
			{WorkerRunID: "wr_3", Turn: 3, StateHash: "c"},
			{WorkerRunID: "wr_3", Turn: 4, StateHash: "d"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Verdicts[0].State, "progressing"; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
	if got, want := len(resp.StatusEvents), 0; got != want {
		t.Fatalf("status events = %d, want %d", got, want)
	}
}
