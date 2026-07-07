package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marius-patrik/agents-harness/services/cli/internal/switcher"
)

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

func installFakeManager(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "manager.log")
	scriptPath := filepath.Join(dir, "agents")
	script := `#!/bin/sh
{
  printf 'ARGS=%s\n' "$*"
  printf 'ROMMIE_AGENT=%s\n' "$ROMMIE_AGENT"
  printf 'ROMMIE_AGENT_DEFAULT_PROVIDER=%s\n' "$ROMMIE_AGENT_DEFAULT_PROVIDER"
  printf 'ROMMIE_AGENT_MODEL=%s\n' "$ROMMIE_AGENT_MODEL"
} > "$FAKE_MANAGER_LOG"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake manager: %v", err)
	}
	t.Setenv("AGENTS_BIN", scriptPath)
	t.Setenv("AGENTS_HOME", dir)
	t.Setenv("FAKE_MANAGER_LOG", logPath)
	t.Setenv("AGENTS_BIN_SCRIPT", "")
	return logPath
}

func prepareRawCLITest(t *testing.T) (root string, logPath string) {
	t.Helper()
	chdirToTemp(t)
	root = t.TempDir()
	t.Setenv("ROMMIE_HOME", root)
	globalJSON = false
	t.Cleanup(func() { globalJSON = false })
	return root, installFakeManager(t)
}

func readManagerLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manager log: %v", err)
	}
	return string(data)
}

func TestRunRawCLIRommieRunsOnSwitchedProvider(t *testing.T) {
	root, logPath := prepareRawCLITest(t)
	if err := switcher.SetProvider(root, switcher.ScopeGlobal, "kimi"); err != nil {
		t.Fatalf("SetProvider failed: %v", err)
	}
	if err := switcher.SetModel(root, switcher.ScopeGlobal, "moonshot-v1"); err != nil {
		t.Fatalf("SetModel failed: %v", err)
	}

	code, handled := runRawCLI([]string{"cli"})
	if !handled || code != 0 {
		t.Fatalf("runRawCLI handled=%t code=%d, want handled true code 0", handled, code)
	}
	log := readManagerLog(t, logPath)
	for _, want := range []string{
		"ARGS=cli exec kimi --",
		"ROMMIE_AGENT=rommie",
		"ROMMIE_AGENT_DEFAULT_PROVIDER=claude",
		"ROMMIE_AGENT_MODEL=moonshot-v1",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("manager log missing %q:\n%s", want, log)
		}
	}
}

func TestRunRawCLIProviderAgentsDefaultAndCanSwitch(t *testing.T) {
	cases := []struct {
		agent            string
		switchedProvider string
	}{
		{"claude", "kimi"},
		{"kimi", "codex"},
		{"codex", "agy"},
		{"agy", "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			root, logPath := prepareRawCLITest(t)
			if err := switcher.SetAgent(root, switcher.ScopeGlobal, tc.agent); err != nil {
				t.Fatalf("SetAgent failed: %v", err)
			}

			code, handled := runRawCLI([]string{"cli"})
			if !handled || code != 0 {
				t.Fatalf("default run handled=%t code=%d, want handled true code 0", handled, code)
			}
			log := readManagerLog(t, logPath)
			if !strings.Contains(log, "ARGS=cli exec "+tc.agent+" --") {
				t.Fatalf("provider agent did not default to namesake provider:\n%s", log)
			}
			if !strings.Contains(log, "ROMMIE_AGENT="+tc.agent) {
				t.Fatalf("provider agent metadata missing:\n%s", log)
			}

			if err := switcher.SetProvider(root, switcher.ScopeGlobal, tc.switchedProvider); err != nil {
				t.Fatalf("SetProvider failed: %v", err)
			}
			code, handled = runRawCLI([]string{"cli"})
			if !handled || code != 0 {
				t.Fatalf("switched run handled=%t code=%d, want handled true code 0", handled, code)
			}
			log = readManagerLog(t, logPath)
			if !strings.Contains(log, "ARGS=cli exec "+tc.switchedProvider+" --") {
				t.Fatalf("provider agent did not run on switched provider:\n%s", log)
			}
			if !strings.Contains(log, "ROMMIE_AGENT="+tc.agent) {
				t.Fatalf("switched provider run lost agent metadata:\n%s", log)
			}
		})
	}
}

func TestRunRawCLIExplicitProviderInjectsActiveAgentOnly(t *testing.T) {
	root, logPath := prepareRawCLITest(t)
	if err := switcher.Save(root, switcher.ScopeGlobal, &switcher.Config{
		Agent:    "kimi",
		Provider: "unknown-provider",
		Model:    "stale-model",
	}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	code, handled := runRawCLI([]string{"cli", "codex", "prompt"})
	if !handled || code != 0 {
		t.Fatalf("runRawCLI handled=%t code=%d, want handled true code 0", handled, code)
	}
	log := readManagerLog(t, logPath)
	for _, want := range []string{
		"ARGS=cli exec codex -- prompt",
		"ROMMIE_AGENT=kimi",
		"ROMMIE_AGENT_DEFAULT_PROVIDER=kimi",
		"ROMMIE_AGENT_MODEL=",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("manager log missing %q:\n%s", want, log)
		}
	}
}

func TestRunRawCLIExplicitProviderRejectsInvalidActiveAgent(t *testing.T) {
	root, logPath := prepareRawCLITest(t)
	if err := switcher.Save(root, switcher.ScopeGlobal, &switcher.Config{Agent: "unknown-agent"}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	code, handled := runRawCLI([]string{"cli", "codex", "prompt"})
	if !handled || code != 2 {
		t.Fatalf("runRawCLI handled=%t code=%d, want handled true code 2", handled, code)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("manager should not be called for invalid agent state; stat err=%v", err)
	}
}
