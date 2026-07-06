package setup_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marius-patrik/agents-harness/services/cli/internal/setup"
)

func TestRunReportsManagerMissingForCLIAdapters(t *testing.T) {
	root := t.TempDir()
	writeNodeYAML(t, root, "client")
	t.Setenv("AGENTS_HOME", "")
	t.Setenv("AGENTS_BIN", "")
	t.Setenv("ROMMIE_HOME", root)

	report, err := setup.Run(setup.Options{Root: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Root != root {
		t.Fatalf("report.Root = %q, want %q", report.Root, root)
	}
	if report.Role == "" {
		t.Fatal("report.Role is empty")
	}
	if report.Hostname == "" {
		t.Fatal("report.Hostname is empty")
	}
	if len(report.Checks) != 4 {
		t.Fatalf("len(report.Checks) = %d, want 4", len(report.Checks))
	}
	for _, check := range report.Checks {
		if check.OK {
			t.Fatalf("%s OK = true, want false without agents-manager", check.CLI)
		}
		if check.BinFound {
			t.Fatalf("%s BinFound = true, want false without agents-manager", check.CLI)
		}
		if !strings.Contains(check.Error, "os/agents-manager") {
			t.Fatalf("%s Error = %q, want manager guidance", check.CLI, check.Error)
		}
	}
	if setup.HasPresentBinFailure(report) {
		t.Fatal("HasPresentBinFailure = true, want false")
	}
}

func TestRunMaterializeCredsRequiresManager(t *testing.T) {
	root := t.TempDir()
	writeNodeYAML(t, root, "client")
	t.Setenv("AGENTS_HOME", "")
	t.Setenv("AGENTS_BIN", "")
	t.Setenv("ROMMIE_HOME", root)

	report, err := setup.Run(setup.Options{Root: root, MaterializeCreds: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Creds) != 4 {
		t.Fatalf("len(report.Creds) = %d, want 4", len(report.Creds))
	}
	for _, cred := range report.Creds {
		if cred.SrcFound || cred.Materialized || cred.Dst != "" || cred.Bytes != 0 {
			t.Fatalf("%s cred result claims local materialization: %+v", cred.CLI, cred)
		}
		if !strings.Contains(cred.Error, "os/agents-manager") {
			t.Fatalf("%s Error = %q, want manager guidance", cred.CLI, cred.Error)
		}
	}
}

func TestRunDelegatesCLISetupToAgentsWhenManaged(t *testing.T) {
	root := t.TempDir()
	agentsHome := filepath.Join(root, ".agents")
	logPath := filepath.Join(root, "agents-args.txt")
	agentsBin := filepath.Join(root, "agents")
	if runtime.GOOS == "windows" {
		agentsBin += ".cmd"
		body := "@echo off\r\n" +
			"echo %* >> \"%AGENTS_STUB_LOG%\"\r\n" +
			"echo ok %*\r\n"
		if err := os.WriteFile(agentsBin, []byte(body), 0o755); err != nil {
			t.Fatalf("write agents stub: %v", err)
		}
	} else {
		body := "#!/bin/sh\n" +
			"printf '%s\\n' \"$*\" >> \"$AGENTS_STUB_LOG\"\n" +
			"printf 'ok %s\\n' \"$*\"\n"
		if err := os.WriteFile(agentsBin, []byte(body), 0o755); err != nil {
			t.Fatalf("write agents stub: %v", err)
		}
	}

	t.Setenv("AGENTS_HOME", agentsHome)
	t.Setenv("AGENTS_BIN", agentsBin)
	t.Setenv("AGENTS_BIN_SCRIPT", "")
	t.Setenv("AGENTS_STUB_LOG", logPath)

	stateRoot := filepath.Join(agentsHome, "harnesses", "agents-harness", "runtime")
	writeNodeYAML(t, stateRoot, "client")
	report, err := setup.Run(setup.Options{Root: stateRoot, MaterializeCreds: true})
	if err != nil {
		t.Fatalf("Run managed: %v", err)
	}
	if len(report.Checks) != 4 {
		t.Fatalf("len(report.Checks) = %d, want 4", len(report.Checks))
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read agents log: %v", err)
	}
	log := string(logBytes)
	for _, cli := range []string{"claude", "codex", "kimi", "agy"} {
		if !strings.Contains(log, "cli doctor "+cli) {
			t.Fatalf("agents log missing doctor for %s:\n%s", cli, log)
		}
		if !strings.Contains(log, "cli materialize-creds "+cli) {
			t.Fatalf("agents log missing materialize-creds for %s:\n%s", cli, log)
		}
	}
}

func writeNodeYAML(t *testing.T, root, role string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	body := []byte("role: " + role + "\nhostname: test-host\n")
	if err := os.WriteFile(filepath.Join(root, "node.yaml"), body, 0o600); err != nil {
		t.Fatalf("write node.yaml: %v", err)
	}
}
