package orchestrator

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Schedule selects runnable work for this tick without mutating caller state.
func Schedule(req ScheduleRequest) (ScheduleResponse, error) {
	now, err := parseNow(req.Now)
	if err != nil {
		return ScheduleResponse{}, err
	}

	streams := map[string]StreamDeclaration{}
	for _, stream := range req.Streams {
		if stream.StreamID == "" {
			return ScheduleResponse{}, fmt.Errorf("orchestrator: stream_id is required")
		}
		if stream.State == "" {
			stream.State = "open"
		}
		streams[stream.StreamID] = stream
	}

	known := map[string]WorkUnit{}
	for _, work := range req.WorkUnits {
		id := workID(work)
		work.WorkUnitID = id
		known[id] = work
		if work.ExternalID != "" {
			known[work.ExternalID] = work
		}
	}

	runningByStream, runningByRepo, runningKeys, globalRunning := runningState(req.WorkUnits)
	for streamID, stream := range streams {
		if stream.InFlightCount > runningByStream[streamID] {
			globalRunning += stream.InFlightCount - runningByStream[streamID]
			runningByStream[streamID] = stream.InFlightCount
		}
	}
	candidates := make([]WorkUnit, 0, len(req.WorkUnits))
	resp := ScheduleResponse{
		Dispatches:     []Dispatch{},
		Blocked:        []BlockedWork{},
		OwnerQuestions: []OwnerQuestion{},
		StatusEvents:   []StatusEvent{},
	}

	for _, work := range req.WorkUnits {
		work.WorkUnitID = workID(work)
		if isTerminal(work.State) || work.State == StateRunning {
			continue
		}
		if work.State != "" && work.State != StateQueued && work.State != StateReady && work.State != StateBlocked {
			continue
		}

		reasons := blockReasons(work, streams, known, runningKeys, now)
		if len(reasons) > 0 {
			resp.Blocked = append(resp.Blocked, blockedWork(work, reasons))
			resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
				Source:     "agents-harness",
				Kind:       "work_blocked",
				WorkUnitID: work.WorkUnitID,
				ExternalID: work.ExternalID,
				Stream:     work.Stream,
				Message:    reasons[0].Message,
			})
			continue
		}
		candidates = append(candidates, work)
	}

	sortWork(candidates, streams)
	globalLimit := req.Caps.MaxDispatches
	if globalLimit <= 0 {
		globalLimit = len(candidates)
	}
	globalInFlightCap := req.Caps.MaxInFlight

	for _, work := range candidates {
		if len(resp.Dispatches) >= globalLimit {
			resp.Blocked = append(resp.Blocked, blockedWork(work, []Reason{{
				Code:    "global_dispatch_cap",
				Message: "global dispatch cap reached for this tick",
			}}))
			continue
		}
		if globalInFlightCap > 0 && globalRunning+len(resp.Dispatches) >= globalInFlightCap {
			resp.Blocked = append(resp.Blocked, blockedWork(work, []Reason{{
				Code:    "global_in_flight_cap",
				Message: "global in-flight cap reached",
			}}))
			continue
		}

		stream := streams[work.Stream]
		streamLimit := stream.MaxInFlight
		if req.Caps.PerStream != nil && req.Caps.PerStream[work.Stream] > 0 {
			streamLimit = req.Caps.PerStream[work.Stream]
		}
		if streamLimit > 0 && runningByStream[work.Stream] >= streamLimit {
			resp.Blocked = append(resp.Blocked, blockedWork(work, []Reason{{
				Code:    "stream_in_flight_cap",
				Message: fmt.Sprintf("stream %s reached max_in_flight", work.Stream),
			}}))
			resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
				Source:     "agents-harness",
				Kind:       "stream_cap_reached",
				WorkUnitID: work.WorkUnitID,
				ExternalID: work.ExternalID,
				Stream:     work.Stream,
				Message:    "stream max_in_flight reached",
			})
			continue
		}
		if stream.MaxPerRepo > 0 && runningByRepo[streamRepoKey(work.Stream, work.Repo)] >= stream.MaxPerRepo {
			resp.Blocked = append(resp.Blocked, blockedWork(work, []Reason{{
				Code:    "repo_in_flight_cap",
				Message: fmt.Sprintf("repo %s reached max_per_repo in stream %s", work.Repo, work.Stream),
			}}))
			continue
		}

		resp.Dispatches = append(resp.Dispatches, dispatchFor(work))
		resp.StatusEvents = append(resp.StatusEvents, StatusEvent{
			Source:     "agents-harness",
			Kind:       "dispatch_selected",
			WorkUnitID: work.WorkUnitID,
			ExternalID: work.ExternalID,
			Stream:     work.Stream,
			Message:    "work selected for dispatch",
		})
		runningByStream[work.Stream]++
		runningByRepo[streamRepoKey(work.Stream, work.Repo)]++
		if work.ConcurrencyKey != "" {
			runningKeys[work.ConcurrencyKey] = true
		}
	}

	return resp, nil
}

func parseNow(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("orchestrator: parse now: %w", err)
	}
	return t, nil
}

func workID(work WorkUnit) string {
	if work.WorkUnitID != "" {
		return work.WorkUnitID
	}
	if work.ExternalID != "" {
		sum := sha1.Sum([]byte(work.Adapter + "|" + work.ExternalID))
		return "wu_" + hex.EncodeToString(sum[:])[:12]
	}
	return ""
}

func runningState(units []WorkUnit) (map[string]int, map[string]int, map[string]bool, int) {
	byStream := map[string]int{}
	byRepo := map[string]int{}
	keys := map[string]bool{}
	global := 0
	for _, work := range units {
		if work.State != StateRunning {
			continue
		}
		byStream[work.Stream]++
		byRepo[streamRepoKey(work.Stream, work.Repo)]++
		if work.ConcurrencyKey != "" {
			keys[work.ConcurrencyKey] = true
		}
		global++
	}
	return byStream, byRepo, keys, global
}

func blockReasons(work WorkUnit, streams map[string]StreamDeclaration, known map[string]WorkUnit, runningKeys map[string]bool, now time.Time) []Reason {
	reasons := []Reason{}
	if work.WorkUnitID == "" {
		reasons = append(reasons, Reason{Code: "missing_work_unit_id", Message: "work_unit_id or external_id is required"})
	}
	if work.Adapter == "" {
		reasons = append(reasons, Reason{Code: "missing_adapter", Message: "adapter is required"})
	}
	if work.ExternalID == "" {
		reasons = append(reasons, Reason{Code: "missing_external_id", Message: "external_id is required"})
	}
	if work.Repo == "" {
		reasons = append(reasons, Reason{Code: "missing_repo", Message: "repo is required"})
	}
	if work.Stream == "" {
		reasons = append(reasons, Reason{Code: "missing_stream", Message: "stream is required"})
		return reasons
	}
	stream, ok := streams[work.Stream]
	if !ok {
		reasons = append(reasons, Reason{Code: "unknown_stream", Message: fmt.Sprintf("stream %s is not declared", work.Stream)})
		return reasons
	}
	if stream.State != "" && stream.State != "open" {
		reasons = append(reasons, Reason{Code: "stream_not_open", Message: fmt.Sprintf("stream %s is %s", work.Stream, stream.State)})
	}
	if missing := missingReadyLabel(work.Labels, stream.ReadyLabels); len(missing) > 0 {
		reasons = append(reasons, Reason{Code: "missing_ready_label", Message: "work unit does not carry a ready label", Details: missing})
	}
	if blocked := intersect(work.Labels, stream.BlockedLabels); len(blocked) > 0 {
		reasons = append(reasons, Reason{Code: "blocked_label", Message: "work unit carries a blocked label", Details: blocked})
	}
	if currentWave := activeWave(stream); work.Wave != "" && currentWave != "" && work.Wave != currentWave {
		reasons = append(reasons, Reason{Code: "wave_gate", Message: fmt.Sprintf("wave %s is waiting for stream gate %s", work.Wave, currentWave)})
	}
	if work.NotBefore != "" {
		notBefore, err := time.Parse(time.RFC3339, work.NotBefore)
		if err != nil {
			reasons = append(reasons, Reason{Code: "invalid_not_before", Message: "not_before must be RFC3339"})
		} else if notBefore.After(now) {
			reasons = append(reasons, Reason{Code: "not_before", Message: fmt.Sprintf("work is not dispatchable before %s", work.NotBefore)})
		}
	}
	if work.ConcurrencyKey != "" && runningKeys[work.ConcurrencyKey] {
		reasons = append(reasons, Reason{Code: "concurrency_key_busy", Message: fmt.Sprintf("concurrency key %s already has running work", work.ConcurrencyKey)})
	}
	if blocked := unresolvedBlockers(work.BlockedBy, known); len(blocked) > 0 {
		reasons = append(reasons, Reason{Code: "blocked_by", Message: "work unit has unresolved blockers", Details: blocked})
	}
	return reasons
}

func missingReadyLabel(labels, ready []string) []string {
	if len(ready) == 0 {
		return nil
	}
	if len(intersect(labels, ready)) > 0 {
		return nil
	}
	return ready
}

func intersect(left, right []string) []string {
	set := map[string]bool{}
	for _, item := range right {
		set[strings.ToLower(item)] = true
	}
	var out []string
	for _, item := range left {
		if set[strings.ToLower(item)] {
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func activeWave(stream StreamDeclaration) string {
	if stream.CurrentWave != "" {
		return stream.CurrentWave
	}
	if len(stream.WaveGates) > 0 {
		return stream.WaveGates[0]
	}
	return ""
}

func unresolvedBlockers(blockers []string, known map[string]WorkUnit) []string {
	var unresolved []string
	for _, blocker := range blockers {
		work, ok := known[blocker]
		if !ok || !isTerminal(work.State) {
			unresolved = append(unresolved, blocker)
		}
	}
	sort.Strings(unresolved)
	return unresolved
}

func isTerminal(state string) bool {
	switch state {
	case StateValidated, StateMerged, StateFailed, StateKilledNonProgress:
		return true
	default:
		return false
	}
}

func blockedWork(work WorkUnit, reasons []Reason) BlockedWork {
	return BlockedWork{
		WorkUnitID: work.WorkUnitID,
		ExternalID: work.ExternalID,
		Stream:     work.Stream,
		Reasons:    reasons,
	}
}

func dispatchFor(work WorkUnit) Dispatch {
	workerKind := work.WorkerKind
	if workerKind == "" {
		workerKind = DefaultWorkerKind
	}
	targetBase := work.TargetBase
	if targetBase == "" {
		targetBase = DefaultTargetBase
	}
	expected := work.ExpectedOutputs
	if len(expected) == 0 {
		expected = []string{DefaultExpectedArtifact, "pr", "validation"}
	}
	return Dispatch{
		WorkUnitID:  work.WorkUnitID,
		ExternalID:  work.ExternalID,
		Stream:      work.Stream,
		Priority:    work.Priority,
		WorkerKind:  workerKind,
		ModelPolicy: work.ModelPolicy,
		RepoCheckout: RepoCheckout{
			Repo:   work.Repo,
			Ref:    work.ExternalID,
			Base:   targetBase,
			Branch: work.Branch,
		},
		Brief:           strings.TrimSpace(work.Title + "\n\n" + work.Body),
		ExpectedOutputs: expected,
		Validation:      work.Validation,
	}
}

func sortWork(units []WorkUnit, streams map[string]StreamDeclaration) {
	sort.SliceStable(units, func(i, j int) bool {
		left := units[i]
		right := units[j]
		if left.Stream != right.Stream {
			return left.Stream < right.Stream
		}
		if priorityRank(left, streams[left.Stream]) != priorityRank(right, streams[right.Stream]) {
			return priorityRank(left, streams[left.Stream]) < priorityRank(right, streams[right.Stream])
		}
		if left.Deadline != right.Deadline {
			if left.Deadline == "" {
				return false
			}
			if right.Deadline == "" {
				return true
			}
			return left.Deadline < right.Deadline
		}
		return left.ExternalID < right.ExternalID
	})
}

func priorityRank(work WorkUnit, stream StreamDeclaration) int {
	if len(stream.PriorityOrder) > 0 {
		for idx, priority := range stream.PriorityOrder {
			if strings.EqualFold(priority, work.Priority) {
				return idx
			}
		}
		return len(stream.PriorityOrder) + 100
	}
	switch strings.ToUpper(work.Priority) {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	default:
		return 100
	}
}

func streamRepoKey(stream, repo string) string {
	return stream + "\x00" + repo
}
