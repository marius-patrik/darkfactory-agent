package orchestrator

import "testing"

func TestReviewNonProgressKillsConfirmedStuckRun(t *testing.T) {
	detect, err := DetectNonProgress(NonProgressRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_stuck", WorkUnitID: "wu_stuck", State: StateRunning}},
		Config:     NonProgressConfig{MaxTurnsWithoutArtifact: 3, RepeatedStateHashLimit: 3},
		Samples: []ProgressSample{
			{WorkerRunID: "wr_stuck", Turn: 1, StateHash: "loop"},
			{WorkerRunID: "wr_stuck", Turn: 2, StateHash: "loop"},
			{WorkerRunID: "wr_stuck", Turn: 3, StateHash: "loop"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detect.Verdicts[0].State != "needs_review" {
		t.Fatalf("expected needs_review, got %s", detect.Verdicts[0].State)
	}

	review, err := ReviewNonProgress(NonProgressReviewRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_stuck", WorkUnitID: "wu_stuck", State: StateRunning}},
		Verdicts:   detect.Verdicts,
		Decisions:  map[string]string{"wr_stuck": "kill"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(review.Results))
	}
	result := review.Results[0]
	if result.State != StateKilledNonProgress {
		t.Fatalf("state = %s, want %s", result.State, StateKilledNonProgress)
	}
	if result.ReviewVerdict != "stuck" {
		t.Fatalf("review verdict = %s, want stuck", result.ReviewVerdict)
	}
	if review.StatusEvents[0].Kind != "non_progress_killed" {
		t.Fatalf("event kind = %s, want non_progress_killed", review.StatusEvents[0].Kind)
	}
}

func TestReviewNonProgressAllowsLegitimateDeepWork(t *testing.T) {
	detect, err := DetectNonProgress(NonProgressRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_deep", WorkUnitID: "wu_deep", State: StateRunning}},
		Config:     NonProgressConfig{MaxTurnsWithoutArtifact: 3, RepeatedStateHashLimit: 3},
		Samples: []ProgressSample{
			{WorkerRunID: "wr_deep", Turn: 1, StateHash: "a"},
			{WorkerRunID: "wr_deep", Turn: 2, StateHash: "b"},
			{WorkerRunID: "wr_deep", Turn: 3, StateHash: "c"},
			{WorkerRunID: "wr_deep", Turn: 4, StateHash: "d", ValidatedArtifact: true},
			{WorkerRunID: "wr_deep", Turn: 5, StateHash: "e"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detect.Verdicts[0].State != "progressing" {
		t.Fatalf("expected progressing, got %s", detect.Verdicts[0].State)
	}

	review, err := ReviewNonProgress(NonProgressReviewRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_deep", WorkUnitID: "wu_deep", State: StateRunning}},
		Verdicts:   detect.Verdicts,
		Decisions:  map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Results) != 0 {
		t.Fatalf("expected no results for progressing run, got %d", len(review.Results))
	}
}

func TestReviewNonProgressDisabledDetectorLetsLoopRunAway(t *testing.T) {
	// Detector thresholds are disabled/infinite, so the loop is never flagged.
	detect, err := DetectNonProgress(NonProgressRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_loop", WorkUnitID: "wu_loop", State: StateRunning}},
		Config:     NonProgressConfig{MaxTurnsWithoutArtifact: 100, RepeatedStateHashLimit: 100},
		Samples: []ProgressSample{
			{WorkerRunID: "wr_loop", Turn: 1, StateHash: "same"},
			{WorkerRunID: "wr_loop", Turn: 2, StateHash: "same"},
			{WorkerRunID: "wr_loop", Turn: 3, StateHash: "same"},
			{WorkerRunID: "wr_loop", Turn: 4, StateHash: "same"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detect.Verdicts[0].State != "progressing" {
		t.Fatalf("expected progressing when detector disabled, got %s", detect.Verdicts[0].State)
	}

	review, err := ReviewNonProgress(NonProgressReviewRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_loop", WorkUnitID: "wu_loop", State: StateRunning}},
		Verdicts:   detect.Verdicts,
		Decisions:  map[string]string{"wr_loop": "kill"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Results) != 0 {
		t.Fatalf("expected no kill without detection, got %d", len(review.Results))
	}
}

func TestReviewNonProgressAllowsExplicitAllowDecision(t *testing.T) {
	detect, err := DetectNonProgress(NonProgressRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_edge", WorkUnitID: "wu_edge", State: StateRunning}},
		Config:     NonProgressConfig{MaxTurnsWithoutArtifact: 2, RepeatedStateHashLimit: 10},
		Samples: []ProgressSample{
			{WorkerRunID: "wr_edge", Turn: 1, StateHash: "a"},
			{WorkerRunID: "wr_edge", Turn: 2, StateHash: "b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detect.Verdicts[0].State != "needs_review" {
		t.Fatalf("expected needs_review, got %s", detect.Verdicts[0].State)
	}

	review, err := ReviewNonProgress(NonProgressReviewRequest{
		WorkerRuns: []WorkerRun{{WorkerRunID: "wr_edge", WorkUnitID: "wu_edge", State: StateRunning}},
		Verdicts:   detect.Verdicts,
		Decisions:  map[string]string{"wr_edge": "allow"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Results) != 1 {
		t.Fatalf("expected 1 allow result, got %d", len(review.Results))
	}
	if review.Results[0].ReviewVerdict != "legitimate" {
		t.Fatalf("review verdict = %s, want legitimate", review.Results[0].ReviewVerdict)
	}
	if review.Results[0].State != "" {
		t.Fatalf("allowed run should not change state, got %s", review.Results[0].State)
	}
}
