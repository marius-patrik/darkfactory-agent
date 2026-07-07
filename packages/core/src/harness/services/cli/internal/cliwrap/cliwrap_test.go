package cliwrap_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marius-patrik/agents-harness/services/cli/internal/cliwrap"
)

func TestSpecs(t *testing.T) {
	want := map[string]string{
		"claude": "claude",
		"codex":  "codex",
		"kimi":   "kimi",
		"agy":    "agy",
		"gemini": "agy",
	}
	for cli, managerName := range want {
		spec, ok := cliwrap.Specs[cli]
		if !ok {
			t.Fatalf("Specs missing %q", cli)
		}
		if spec.Name != managerName {
			t.Errorf("Specs[%q].Name = %q, want %q", cli, spec.Name, managerName)
		}
	}
	if len(cliwrap.Specs) != len(want) {
		t.Errorf("Specs len = %d, want %d", len(cliwrap.Specs), len(want))
	}
}

func TestRootDirIsEmptyWithoutAgentsHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTS_HOME", "")
	t.Setenv("ROMMIE_HOME", root)

	got := cliwrap.RootDir("", "codex")
	if got != "" {
		t.Fatalf("RootDir = %q, want empty without AGENTS_HOME", got)
	}
}

func TestRootDirHonorsAgentsHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTS_HOME", root)
	t.Setenv("ROMMIE_HOME", t.TempDir())

	got := cliwrap.RootDir("", "codex")
	want := filepath.Join(root, "clis", "codex")
	if got != want {
		t.Fatalf("RootDir = %q, want %q", got, want)
	}
}

func TestExecUnknownCLI(t *testing.T) {
	err := cliwrap.Exec("unknown", nil)
	if err == nil {
		t.Fatal("Exec unknown returned nil")
	}
	wrapErr, ok := err.(*cliwrap.Error)
	if !ok {
		t.Fatalf("Exec unknown error type = %T, want *cliwrap.Error", err)
	}
	if wrapErr.Code != 2 {
		t.Fatalf("Exec unknown code = %d, want 2", wrapErr.Code)
	}
	if !strings.Contains(wrapErr.Error(), "unknown cli: unknown") {
		t.Fatalf("Exec unknown error = %q", wrapErr.Error())
	}
}

func TestExecRequiresAgentsManager(t *testing.T) {
	t.Setenv("AGENTS_HOME", "")
	t.Setenv("AGENTS_BIN", "")

	err := cliwrap.Exec("codex", []string{"--probe"})
	if err == nil {
		t.Fatal("Exec without manager returned nil")
	}
	wrapErr, ok := err.(*cliwrap.Error)
	if !ok {
		t.Fatalf("Exec error type = %T, want *cliwrap.Error", err)
	}
	if wrapErr.Code != 2 {
		t.Fatalf("Exec code = %d, want 2", wrapErr.Code)
	}
	if !strings.Contains(wrapErr.Error(), "os/agents-manager") {
		t.Fatalf("Exec error = %q, want manager guidance", wrapErr.Error())
	}
}

func TestExecDelegatesToAgentsWhenManaged(t *testing.T) {
	root := t.TempDir()
	outFile := filepath.Join(root, "args.txt")
	agentsBin := filepath.Join(root, "agents")
	if runtime.GOOS == "windows" {
		agentsBin += ".cmd"
		body := "@echo off\r\n" +
			"echo %* > \"%AGENTS_DELEGATE_ARGS%\"\r\n"
		if err := os.WriteFile(agentsBin, []byte(body), 0o755); err != nil {
			t.Fatalf("write agents stub: %v", err)
		}
	} else {
		body := "#!/bin/sh\n" +
			"printf '%s\\n' \"$*\" > \"$AGENTS_DELEGATE_ARGS\"\n"
		if err := os.WriteFile(agentsBin, []byte(body), 0o755); err != nil {
			t.Fatalf("write agents stub: %v", err)
		}
	}

	t.Setenv("AGENTS_HOME", filepath.Join(root, ".agents"))
	t.Setenv("AGENTS_BIN", agentsBin)
	t.Setenv("AGENTS_BIN_SCRIPT", "")
	t.Setenv("AGENTS_DELEGATE_ARGS", outFile)

	if err := cliwrap.Exec("codex", []string{"--probe"}); err != nil {
		t.Fatalf("Exec managed: %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if strings.TrimSpace(string(got)) != "cli exec codex -- --probe" {
		t.Fatalf("delegated args = %q", strings.TrimSpace(string(got)))
	}
}

func TestExecHelperProcess(t *testing.T) {
	if os.Getenv("CLIWRAP_EXEC_HELPER") != "1" {
		return
	}
	cli := os.Getenv("CLIWRAP_EXEC_CLI")
	if err := cliwrap.Exec(cli, []string{"--probe"}); err != nil {
		t.Fatalf("Exec helper: %v", err)
	}
}
