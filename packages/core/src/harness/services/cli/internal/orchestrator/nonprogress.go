package orchestrator

import (
	"fmt"
	"sort"
)

// DetectNonProgress flags candidate stuck runs for review confirmation. It does
// not kill work; later slices can attach a reviewer and escalation policy.
func DetectNonProgress(req NonProgressRequest) (NonProgressResponse, error) {
	if _, err := parseNow(req.Now); err != nil {
		return NonProgressResponse{}, err
	}
	maxTurns := req.Config.MaxTurnsWithoutArtifact
	if maxTurns <= 0 {
		maxTurns = DefaultNoArtifactTurns
	}
	repeatLimit := req.Config.RepeatedStateHashLimit
	if repeatLimit <= 0 {
		repeatLimit = DefaultRepeatHashLimit
	}

	runs := map[string]WorkerRun{}
	for _, run := range req.WorkerRuns {
		if run.WorkerRunID == "" {
			return NonProgressResponse{}, fmt.Errorf("orchestrator: worker_run_id is required")
		}
		runs[run.WorkerRunID] = run
	}

	samplesByRun := map[string][]ProgressSample{}
	for _, sample := range req.Samples {
		if sample.WorkerRunID == "" {
			return NonProgressResponse{}, fmt.Errorf("orchestrator: sample worker_run_id is required")
		}
		samplesByRun[sample.WorkerRunID] = append(samplesByRun[sample.WorkerRunID], sample)
	}

	resp := NonProgressResponse{
		Verdicts:     []NonProgressVerdict{},
		StatusEvents: []StatusEvent{},
	}
	for runID, samples := range samplesByRun {
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Turn < samples[j].Turn
		})
		run := runs[runID]
		if run.State != "" && run.State != StateRunning {
			continue
		}
		reasons := detectReasons(samples, maxTurns, repeatLimit)
		if len(reasons) == 0 {
			resp.Verdicts = append(resp.Verdicts, NonProgressVerdict{
				WorkerRunID: runID,
				WorkUnitID:  workUnitFor(run, samples),
				State:       "progressing",
			})
			continue
		}
		workUnitID := workUnitFor(run, samples)
		resp.Verdicts = append(resp.Verdicts, NonProgressVerdict{
			WorkerRunID: runID,
			WorkUnitID:  workUnitID,
			State:       "needs_review",
			Reasons:     reasons,
		})
		resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
			Source:      "agents-harness",
			Kind:        "non_progress_suspected",
			WorkUnitID:  workUnitID,
			ExternalID:  run.ExternalID,
			Stream:      run.Stream,
			WorkerRunID: runID,
			Message:     reasons[0].Message,
		})
	}
	sort.Slice(resp.Verdicts, func(i, j int) bool {
		return resp.Verdicts[i].WorkerRunID < resp.Verdicts[j].WorkerRunID
	})
	sort.Slice(resp.StatusEvents, func(i, j int) bool {
		return resp.StatusEvents[i].WorkerRunID < resp.StatusEvents[j].WorkerRunID
	})
	return resp, nil
}

func detectReasons(samples []ProgressSample, maxTurns, repeatLimit int) []Reason {
	reasons := []Reason{}
	if turns := turnsWithoutArtifact(samples); turns >= maxTurns {
		reasons = append(reasons, Reason{
			Code:    "no_validated_artifact",
			Message: fmt.Sprintf("no validated artifact in %d turns", turns),
			Details: []string{fmt.Sprintf("threshold=%d", maxTurns)},
		})
	}
	if hash, count := repeatedHash(samples); hash != "" && count >= repeatLimit {
		reasons = append(reasons, Reason{
			Code:    "repeated_state_hash",
			Message: fmt.Sprintf("state hash repeated for %d consecutive samples", count),
			Details: []string{fmt.Sprintf("state_hash=%s", hash), fmt.Sprintf("threshold=%d", repeatLimit)},
		})
	}
	return reasons
}

func turnsWithoutArtifact(samples []ProgressSample) int {
	if len(samples) == 0 {
		return 0
	}
	lastArtifactTurn := samples[0].Turn - 1
	lastTurn := samples[len(samples)-1].Turn
	for _, sample := range samples {
		if sample.ValidatedArtifact {
			lastArtifactTurn = sample.Turn
		}
	}
	return lastTurn - lastArtifactTurn
}

func repeatedHash(samples []ProgressSample) (string, int) {
	var currentHash string
	currentCount := 0
	bestHash := ""
	bestCount := 0
	for _, sample := range samples {
		if sample.StateHash == "" {
			currentHash = ""
			currentCount = 0
			continue
		}
		if sample.StateHash == currentHash {
			currentCount++
		} else {
			currentHash = sample.StateHash
			currentCount = 1
		}
		if currentCount > bestCount {
			bestHash = currentHash
			bestCount = currentCount
		}
	}
	return bestHash, bestCount
}

func workUnitFor(run WorkerRun, samples []ProgressSample) string {
	if run.WorkUnitID != "" {
		return run.WorkUnitID
	}
	for _, sample := range samples {
		if sample.WorkUnitID != "" {
			return sample.WorkUnitID
		}
	}
	return ""
}
