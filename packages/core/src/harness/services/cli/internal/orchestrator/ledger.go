package orchestrator

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// ObserveRuns folds dispatches, existing worker runs, and worker results into a
// deterministic adapter-facing run ledger snapshot.
func ObserveRuns(req ObserveRunsRequest) (ObserveRunsResponse, error) {
	now, err := parseNow(req.Now)
	if err != nil {
		return ObserveRunsResponse{}, err
	}
	timeout := req.HeartbeatTimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultHeartbeatSeconds
	}

	runs := map[string]WorkerRun{}
	runByWork := map[string]string{}
	resp := ObserveRunsResponse{
		WorkerRuns:     []WorkerRun{},
		OwnerQuestions: []OwnerQuestion{},
		StatusEvents:   []StatusEvent{},
	}

	for _, run := range req.WorkerRuns {
		if run.WorkerRunID == "" {
			return ObserveRunsResponse{}, fmt.Errorf("orchestrator: worker_run_id is required for existing worker runs")
		}
		if run.State == "" {
			run.State = StateQueued
		}
		runs[run.WorkerRunID] = run
		if run.WorkUnitID != "" {
			runByWork[run.WorkUnitID] = run.WorkerRunID
		}
	}

	for _, dispatch := range req.Dispatches {
		runID := workerRunID(dispatch.WorkUnitID, dispatch.WorkerKind)
		if _, ok := runs[runID]; ok {
			continue
		}
		run := WorkerRun{
			WorkerRunID:     runID,
			WorkUnitID:      dispatch.WorkUnitID,
			ExternalID:      dispatch.ExternalID,
			Stream:          dispatch.Stream,
			Repo:            dispatch.RepoCheckout.Repo,
			WorkerKind:      dispatch.WorkerKind,
			ParentRunID:     dispatch.ParentRunID,
			State:           StateQueued,
			StartedAt:       formatTime(now),
			LastHeartbeatAt: formatTime(now),
		}
		runs[runID] = run
		runByWork[run.WorkUnitID] = runID
		resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
			Source:      "agents-harness",
			Kind:        "worker_run_materialized",
			WorkUnitID:  run.WorkUnitID,
			ExternalID:  run.ExternalID,
			Stream:      run.Stream,
			WorkerRunID: run.WorkerRunID,
			Message:     "worker run materialized from dispatch",
		})
	}

	for _, result := range req.Results {
		runID := result.WorkerRunID
		if runID == "" {
			runID = runByWork[result.WorkUnitID]
		}
		if runID == "" {
			return ObserveRunsResponse{}, fmt.Errorf("orchestrator: result for work_unit_id %s has no matching worker run", result.WorkUnitID)
		}
		run, ok := runs[runID]
		if !ok {
			return ObserveRunsResponse{}, fmt.Errorf("orchestrator: result references unknown worker_run_id %s", runID)
		}
		if result.State != "" {
			run.State = result.State
		}
		run.ArtifactRefs = append([]string{}, result.ArtifactRefs...)
		run.BlockedReason = result.BlockedReason
		run.ReviewVerdict = result.ReviewVerdict
		run.LastHeartbeatAt = formatTime(now)
		runs[runID] = run
		resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
			Source:      "agents-harness",
			Kind:        "worker_result_recorded",
			WorkUnitID:  run.WorkUnitID,
			ExternalID:  run.ExternalID,
			Stream:      run.Stream,
			WorkerRunID: run.WorkerRunID,
			Message:     "worker result recorded",
		})
		if result.OwnerQuestion != "" {
			resp.OwnerQuestions = append(resp.OwnerQuestions, OwnerQuestion{
				WorkUnitID: run.WorkUnitID,
				ExternalID: run.ExternalID,
				Question:   result.OwnerQuestion,
				Policy:     "issue",
			})
		}
	}

	for id, run := range runs {
		if run.State != StateRunning || run.LastHeartbeatAt == "" {
			continue
		}
		lastHeartbeat, err := time.Parse(time.RFC3339, run.LastHeartbeatAt)
		if err != nil {
			return ObserveRunsResponse{}, fmt.Errorf("orchestrator: parse last_heartbeat_at for %s: %w", run.WorkerRunID, err)
		}
		if now.Sub(lastHeartbeat) <= time.Duration(timeout)*time.Second {
			continue
		}
		run.State = StateHeartbeatMissing
		runs[id] = run
		resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
			Source:      "agents-harness",
			Kind:        "worker_heartbeat_missing",
			WorkUnitID:  run.WorkUnitID,
			ExternalID:  run.ExternalID,
			Stream:      run.Stream,
			WorkerRunID: run.WorkerRunID,
			Message:     "worker heartbeat missing",
		})
	}

	for _, run := range runs {
		resp.WorkerRuns = append(resp.WorkerRuns, run)
	}
	sort.Slice(resp.WorkerRuns, func(i, j int) bool {
		return resp.WorkerRuns[i].WorkerRunID < resp.WorkerRuns[j].WorkerRunID
	})
	sort.Slice(resp.StatusEvents, func(i, j int) bool {
		if resp.StatusEvents[i].WorkerRunID != resp.StatusEvents[j].WorkerRunID {
			return resp.StatusEvents[i].WorkerRunID < resp.StatusEvents[j].WorkerRunID
		}
		return resp.StatusEvents[i].Kind < resp.StatusEvents[j].Kind
	})
	sort.Slice(resp.OwnerQuestions, func(i, j int) bool {
		return resp.OwnerQuestions[i].WorkUnitID < resp.OwnerQuestions[j].WorkUnitID
	})
	return resp, nil
}

func workerRunID(workUnitID, workerKind string) string {
	if workerKind == "" {
		workerKind = DefaultWorkerKind
	}
	sum := sha1.Sum([]byte(workUnitID + "|" + workerKind))
	return "wr_" + hex.EncodeToString(sum[:])[:12]
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
