package contracts

import "testing"

func TestRunStatusVocabulary(t *testing.T) {
	cases := []struct {
		status         RunStatus
		known          bool
		terminal       bool
		success        bool
		reviewEligible bool
	}{
		{RunStatusQueued, true, false, false, false},
		{RunStatusRunning, true, false, false, false},
		{RunStatusSucceeded, true, true, true, true},
		{RunStatusFailed, true, true, false, false},
		{RunStatusInfraFailed, true, true, false, false},
		{RunStatusNoOp, true, true, false, false},
		{RunStatusBlocked, true, true, false, false},
		{RunStatusCancelled, true, true, false, false},
		{RunStatus("mystery-green"), false, false, false, false},
	}

	for _, tc := range cases {
		if got := tc.status.Known(); got != tc.known {
			t.Fatalf("%s Known()=%v want %v", tc.status, got, tc.known)
		}
		if got := tc.status.Terminal(); got != tc.terminal {
			t.Fatalf("%s Terminal()=%v want %v", tc.status, got, tc.terminal)
		}
		if got := tc.status.Success(); got != tc.success {
			t.Fatalf("%s Success()=%v want %v", tc.status, got, tc.success)
		}
		if got := tc.status.ReviewEligible(); got != tc.reviewEligible {
			t.Fatalf("%s ReviewEligible()=%v want %v", tc.status, got, tc.reviewEligible)
		}
	}
}
