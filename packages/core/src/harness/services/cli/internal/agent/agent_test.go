package agent

import (
	"testing"
)

func TestRegistrySeedsAllAgents(t *testing.T) {
	want := []string{"rommie", ProviderClaude, ProviderKimi, ProviderCodex, ProviderAgy}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("expected %d agents, got %d: %v", len(want), len(got), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("agent %d: want %q, got %q", i, name, got[i])
		}
		a, ok := Get(name)
		if !ok {
			t.Errorf("agent %q missing from registry", name)
			continue
		}
		if a.Name != name {
			t.Errorf("agent %q has mismatched Name %q", name, a.Name)
		}
	}
}

func TestProviderAgentsDefaultToNamesake(t *testing.T) {
	for _, name := range Providers {
		a, ok := Get(name)
		if !ok {
			t.Fatalf("provider agent %q not found", name)
		}
		if a.DefaultProvider != name {
			t.Errorf("provider agent %q default provider = %q, want %q", name, a.DefaultProvider, name)
		}
	}
}

func TestRommieDefaultProvider(t *testing.T) {
	a, ok := Get("rommie")
	if !ok {
		t.Fatal("rommie agent not found")
	}
	if !ValidProvider(a.DefaultProvider) {
		t.Errorf("rommie default provider %q is not a valid provider", a.DefaultProvider)
	}
	if len(a.Skills) == 0 {
		t.Error("rommie agent should be heavily populated with skills")
	}
	if len(a.Memory) == 0 {
		t.Error("rommie agent should be seeded with memory")
	}
	if len(a.History) == 0 {
		t.Error("rommie agent should be seeded with history")
	}
}

func TestProviderAgentsInheritGlobalPersona(t *testing.T) {
	base, ok := Get("rommie")
	if !ok {
		t.Fatal("rommie agent not found")
	}
	for _, name := range Providers {
		a, ok := Get(name)
		if !ok {
			t.Fatalf("provider agent %q not found", name)
		}
		if len(a.Skills) == 0 {
			t.Errorf("provider agent %q should inherit skills", name)
		}
		if len(a.Memory) == 0 {
			t.Errorf("provider agent %q should inherit memory", name)
		}
		if len(a.History) == 0 {
			t.Errorf("provider agent %q should inherit history", name)
		}
		if len(a.Skills) != len(base.Skills) {
			t.Errorf("provider agent %q skills length = %d, want %d", name, len(a.Skills), len(base.Skills))
		}
	}
}

func TestValidProvider(t *testing.T) {
	for _, p := range Providers {
		if !ValidProvider(p) {
			t.Errorf("expected %q to be a valid provider", p)
		}
	}
	if ValidProvider("unknown") {
		t.Error("expected 'unknown' to be invalid")
	}
}
