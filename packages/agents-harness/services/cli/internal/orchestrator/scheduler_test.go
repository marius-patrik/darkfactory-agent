package orchestrator

import "testing"

func TestScheduleSelectsDeterministicReadyWorkWithinCaps(t *testing.T) {
	resp, err := Schedule(ScheduleRequest{
		Streams: []StreamDeclaration{{
			StreamID:      "features",
			PriorityOrder: []string{"P0", "P1", "P2"},
			MaxInFlight:   2,
			ReadyLabels:   []string{"df:ready"},
		}},
		WorkUnits: []WorkUnit{
			work("github:marius-patrik/repo#3", "P2"),
			work("github:marius-patrik/repo#1", "P0"),
			work("github:marius-patrik/repo#2", "P1"),
		},
		Caps: ScheduleCaps{MaxDispatches: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(resp.Dispatches), 2; got != want {
		t.Fatalf("dispatch count = %d, want %d", got, want)
	}
	if got := resp.Dispatches[0].ExternalID; got != "github:marius-patrik/repo#1" {
		t.Fatalf("first dispatch = %s", got)
	}
	if got := resp.Dispatches[1].ExternalID; got != "github:marius-patrik/repo#2" {
		t.Fatalf("second dispatch = %s", got)
	}
	if got, want := resp.Dispatches[0].WorkerKind, DefaultWorkerKind; got != want {
		t.Fatalf("worker kind = %s, want %s", got, want)
	}
	if got, want := len(resp.Blocked), 1; got != want {
		t.Fatalf("blocked count = %d, want %d", got, want)
	}
	if got := resp.Blocked[0].Reasons[0].Code; got != "global_dispatch_cap" {
		t.Fatalf("blocked reason = %s", got)
	}
}

func TestScheduleBlocksDependenciesLabelsAndWaves(t *testing.T) {
	blocker := work("github:marius-patrik/repo#10", "P1")
	blocker.State = StateReady
	blocked := work("github:marius-patrik/repo#11", "P0")
	blocked.BlockedBy = []string{blocker.ExternalID}
	blocked.Labels = append(blocked.Labels, "df:blocked")
	blocked.Wave = "features"

	resp, err := Schedule(ScheduleRequest{
		Streams: []StreamDeclaration{{
			StreamID:      "features",
			ReadyLabels:   []string{"df:ready"},
			BlockedLabels: []string{"df:blocked"},
			WaveGates:     []string{"hygiene", "features"},
		}},
		WorkUnits: []WorkUnit{blocker, blocked},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(resp.Dispatches), 1; got != want {
		t.Fatalf("dispatch count = %d, want %d", got, want)
	}
	if got, want := len(resp.Blocked), 1; got != want {
		t.Fatalf("blocked count = %d, want %d", got, want)
	}
	reasons := reasonCodes(resp.Blocked[0].Reasons)
	for _, want := range []string{"blocked_by", "blocked_label", "wave_gate"} {
		if !reasons[want] {
			t.Fatalf("missing reason %s in %#v", want, reasons)
		}
	}
}

func TestScheduleHonorsNotBeforeAndConcurrencyKey(t *testing.T) {
	running := work("github:marius-patrik/repo#20", "P0")
	running.State = StateRunning
	running.ConcurrencyKey = "repo:marius-patrik/repo"
	busy := work("github:marius-patrik/repo#21", "P0")
	busy.ConcurrencyKey = running.ConcurrencyKey
	future := work("github:marius-patrik/repo#22", "P0")
	future.NotBefore = "2026-07-04T12:00:00Z"

	resp, err := Schedule(ScheduleRequest{
		Now: "2026-07-04T11:00:00Z",
		Streams: []StreamDeclaration{{
			StreamID:    "features",
			ReadyLabels: []string{"df:ready"},
		}},
		WorkUnits: []WorkUnit{running, busy, future},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(resp.Dispatches), 0; got != want {
		t.Fatalf("dispatch count = %d, want %d", got, want)
	}
	if got, want := len(resp.Blocked), 2; got != want {
		t.Fatalf("blocked count = %d, want %d", got, want)
	}
	reasonsByExternal := map[string]string{}
	for _, item := range resp.Blocked {
		reasonsByExternal[item.ExternalID] = item.Reasons[0].Code
	}
	if got := reasonsByExternal[busy.ExternalID]; got != "concurrency_key_busy" {
		t.Fatalf("busy reason = %s", got)
	}
	if got := reasonsByExternal[future.ExternalID]; got != "not_before" {
		t.Fatalf("future reason = %s", got)
	}
}

func work(externalID, priority string) WorkUnit {
	return WorkUnit{
		Adapter:    "darkfactory",
		ExternalID: externalID,
		Repo:       "marius-patrik/repo",
		Title:      "Test work",
		Priority:   priority,
		Stream:     "features",
		Labels:     []string{"df:ready"},
		TargetBase: "dev",
	}
}

func reasonCodes(reasons []Reason) map[string]bool {
	out := map[string]bool{}
	for _, reason := range reasons {
		out[reason.Code] = true
	}
	return out
}
