package switcher

import (
	"os"
	"path/filepath"
	"testing"
)

// chdirToTemp moves the test into a temp directory so project-scope config does
// not leak in from the repository working directory.
func chdirToTemp(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

func TestResolveDefaultsToRommie(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	agentName, provider, model, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if agentName != "rommie" {
		t.Errorf("default agent = %q, want rommie", agentName)
	}
	if provider != "claude" {
		t.Errorf("default provider = %q, want claude", provider)
	}
	if model != "" {
		t.Errorf("default model = %q, want empty", model)
	}
}

func TestSetProviderSwitchesRommieProvider(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	if err := SetProvider(root, ScopeGlobal, "kimi"); err != nil {
		t.Fatalf("SetProvider failed: %v", err)
	}
	agentName, provider, _, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if agentName != "rommie" {
		t.Errorf("agent = %q, want rommie", agentName)
	}
	if provider != "kimi" {
		t.Errorf("provider = %q, want kimi", provider)
	}
}

func TestSetAgentSwitchesToProviderAgent(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	for _, name := range []string{"claude", "kimi", "codex", "agy"} {
		if err := SetAgent(root, ScopeGlobal, name); err != nil {
			t.Fatalf("SetAgent %s failed: %v", name, err)
		}
		agentName, provider, _, err := Resolve(root)
		if err != nil {
			t.Fatalf("Resolve %s failed: %v", name, err)
		}
		if agentName != name {
			t.Errorf("agent = %q, want %q", agentName, name)
		}
		// Provider agent should default to its namesake provider.
		if provider != name {
			t.Errorf("provider = %q, want %q", provider, name)
		}
	}
}

func TestProviderAgentCanBeSwitched(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	cases := []struct {
		agent    string
		provider string
	}{
		{"claude", "kimi"},
		{"kimi", "codex"},
		{"codex", "agy"},
		{"agy", "claude"},
	}
	for _, tc := range cases {
		if err := SetAgent(root, ScopeGlobal, tc.agent); err != nil {
			t.Fatalf("SetAgent %s failed: %v", tc.agent, err)
		}
		if err := SetProvider(root, ScopeGlobal, tc.provider); err != nil {
			t.Fatalf("SetProvider %s failed: %v", tc.provider, err)
		}
		agentName, provider, _, err := Resolve(root)
		if err != nil {
			t.Fatalf("Resolve %s/%s failed: %v", tc.agent, tc.provider, err)
		}
		if agentName != tc.agent {
			t.Errorf("agent = %q, want %q", agentName, tc.agent)
		}
		if provider != tc.provider {
			t.Errorf("provider = %q, want %q", provider, tc.provider)
		}
	}
}

func TestSessionScopeOverridesGlobal(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	if err := SetAgent(root, ScopeGlobal, "claude"); err != nil {
		t.Fatalf("SetAgent global failed: %v", err)
	}
	if err := SetProvider(root, ScopeGlobal, "claude"); err != nil {
		t.Fatalf("SetProvider global failed: %v", err)
	}
	if err := SetAgent(root, ScopeSession, "kimi"); err != nil {
		t.Fatalf("SetAgent session failed: %v", err)
	}
	if err := SetProvider(root, ScopeSession, "codex"); err != nil {
		t.Fatalf("SetProvider session failed: %v", err)
	}
	agentName, provider, _, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if agentName != "kimi" {
		t.Errorf("session override agent = %q, want kimi", agentName)
	}
	if provider != "codex" {
		t.Errorf("session override provider = %q, want codex", provider)
	}
}

func TestProjectScopeOverridesGlobal(t *testing.T) {
	root := t.TempDir()
	if err := SetAgent(root, ScopeGlobal, "claude"); err != nil {
		t.Fatalf("SetAgent global failed: %v", err)
	}

	// Project scope config lives in the working directory, so we must run from
	// a directory where we can create .rommie/.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	projectDir := t.TempDir()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := SetAgent(root, ScopeProject, "kimi"); err != nil {
		t.Fatalf("SetAgent project failed: %v", err)
	}

	agentName, _, _, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if agentName != "kimi" {
		t.Errorf("project override agent = %q, want kimi", agentName)
	}

	configPath := filepath.Join(projectDir, ".rommie", "switcher.yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("project switcher config not created at %s: %v", configPath, err)
	}
}

func TestSetUnknownAgentFails(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	if err := SetAgent(root, ScopeGlobal, "hermes"); err == nil {
		t.Error("expected SetAgent with unknown agent to fail")
	}
}

func TestSetUnknownProviderFails(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	if err := SetProvider(root, ScopeGlobal, "gpt-5"); err == nil {
		t.Error("expected SetProvider with unknown provider to fail")
	}
}

func TestParseScopeDefaultsToProject(t *testing.T) {
	scope, err := ParseScope(false, false, false)
	if err != nil {
		t.Fatalf("ParseScope failed: %v", err)
	}
	if scope != ScopeProject {
		t.Errorf("default scope = %v, want project", scope)
	}
}

func TestParseScopeExplicitFlags(t *testing.T) {
	cases := []struct {
		name                string
		session, project, global bool
		want                Scope
	}{
		{"session", true, false, false, ScopeSession},
		{"project", false, true, false, ScopeProject},
		{"global", false, false, true, ScopeGlobal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, err := ParseScope(tc.session, tc.project, tc.global)
			if err != nil {
				t.Fatalf("ParseScope failed: %v", err)
			}
			if scope != tc.want {
				t.Errorf("scope = %v, want %v", scope, tc.want)
			}
		})
	}
}

func TestProviderAgentDefaultsToItsProviderWhenAgentScopeWins(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	// Project scope overrides provider to claude.
	if err := SetProvider(root, ScopeProject, "claude"); err != nil {
		t.Fatalf("SetProvider project failed: %v", err)
	}
	// Session scope switches agent to kimi without setting provider.
	if err := SetAgent(root, ScopeSession, "kimi"); err != nil {
		t.Fatalf("SetAgent session failed: %v", err)
	}
	_, provider, _, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if provider != "kimi" {
		t.Errorf("provider = %q, want kimi (provider agent default)", provider)
	}
}

func TestResolveAgentIgnoresInvalidProviderState(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	if err := Save(root, ScopeGlobal, &Config{Agent: "kimi", Provider: "unknown-provider"}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	agentName, err := ResolveAgent(root)
	if err != nil {
		t.Fatalf("ResolveAgent failed: %v", err)
	}
	if agentName != "kimi" {
		t.Errorf("agent = %q, want kimi", agentName)
	}
}

func TestResolveAgentRejectsUnknownAgent(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	if err := Save(root, ScopeGlobal, &Config{Agent: "unknown-agent"}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if _, err := ResolveAgent(root); err == nil {
		t.Fatal("expected ResolveAgent to reject unknown agent")
	}
}

func TestModelCanBeSetAndResolved(t *testing.T) {
	chdirToTemp(t)
	root := t.TempDir()
	if err := SetProvider(root, ScopeGlobal, "claude"); err != nil {
		t.Fatalf("SetProvider failed: %v", err)
	}
	if err := SetModel(root, ScopeGlobal, "claude-3-7-sonnet"); err != nil {
		t.Fatalf("SetModel failed: %v", err)
	}
	_, _, model, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if model != "claude-3-7-sonnet" {
		t.Errorf("model = %q, want claude-3-7-sonnet", model)
	}
}

func TestParseScopeRejectsMultipleFlags(t *testing.T) {
	if _, err := ParseScope(true, true, false); err == nil {
		t.Error("expected multiple scope flags to be rejected")
	}
}
