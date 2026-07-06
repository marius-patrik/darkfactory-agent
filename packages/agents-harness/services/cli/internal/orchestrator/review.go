package orchestrator

import (
	"fmt"
	"sort"
)

// ReviewNonProgress applies review-role decisions to non-progress verdicts.
// A confirmed-stuck run is escalated to StateKilledNonProgress; an allowed run
// is left in place with a legitimate review marker so deep work is not capped.
func ReviewNonProgress(req NonProgressReviewRequest) (NonProgressReviewResponse, error) {
	runs := map[string]WorkerRun{}
	for _, run := range req.WorkerRuns {
		if run.WorkerRunID == "" {
			return NonProgressReviewResponse{}, fmt.Errorf("orchestrator: worker_run_id is required")
		}
		runs[run.WorkerRunID] = run
	}

	resp := NonProgressReviewResponse{
		Results:      []WorkerResult{},
		StatusEvents: []StatusEvent{},
	}
	for _, verdict := range req.Verdicts {
		if verdict.State != "needs_review" {
			continue
		}
		decision := req.Decisions[verdict.WorkerRunID]
		run := runs[verdict.WorkerRunID]
		switch decision {
		case "kill":
			resp.Results = append(resp.Results, WorkerResult{
				WorkUnitID:    verdict.WorkUnitID,
				WorkerRunID:   verdict.WorkerRunID,
				State:         StateKilledNonProgress,
				BlockedReason: "non_progress_confirmed",
				ReviewVerdict: "stuck",
			})
			resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
				Source:      "agents-harness",
				Kind:        "non_progress_killed",
				WorkUnitID:  verdict.WorkUnitID,
				ExternalID:  run.ExternalID,
				Stream:      run.Stream,
				WorkerRunID: verdict.WorkerRunID,
				Message:     "review confirmed non-progress; run killed",
			})
		default:
			// Default policy is conservative: without an explicit kill decision,
			// treat the run as legitimately deep and record the allow verdict.
			resp.Results = append(resp.Results, WorkerResult{
				WorkUnitID:    verdict.WorkUnitID,
				WorkerRunID:   verdict.WorkerRunID,
				ReviewVerdict: "legitimate",
			})
			resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
				Source:      "agents-harness",
				Kind:        "non_progress_allowed",
				WorkUnitID:  verdict.WorkUnitID,
				ExternalID:  run.ExternalID,
				Stream:      run.Stream,
				WorkerRunID: verdict.WorkerRunID,
				Message:     "review allowed legitimate deep work",
			})
		}
	}
	sort.Slice(resp.Results, func(i, j int) bool {
		return resp.Results[i].WorkerRunID < resp.Results[j].WorkerRunID
	})
	sort.Slice(resp.StatusEvents, func(i, j int) bool {
		return resp.StatusEvents[i].WorkerRunID < resp.StatusEvents[j].WorkerRunID
	})
	return resp, nil
}
