package identity_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marius-patrik/agents-harness/services/cli/internal/identity"
)

func TestNodeInitRequiresAgentsManager(t *testing.T) {
	t.Setenv("AGENTS_BIN", "")
	t.Setenv("AGENTS_HOME", "")

	err := identity.Init(identity.InitOptions{Root: t.TempDir(), Role: identity.RoleGateway})
	if err == nil {
		t.Fatal("expected error without agents-manager")
	}
	if !strings.Contains(err.Error(), "os/agents-manager") {
		t.Fatalf("error = %q, want manager guidance", err.Error())
	}
}

func TestNodeInitDelegatesToAgentsManager(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "agents-args.txt")
	agentsBin := filepath.Join(root, "agents")
	if runtime.GOOS == "windows" {
		agentsBin += ".cmd"
		body := "@echo off\r\n" +
			"echo %* > \"%AGENTS_STUB_LOG%\"\r\n"
		if err := os.WriteFile(agentsBin, []byte(body), 0o755); err != nil {
			t.Fatalf("write agents stub: %v", err)
		}
	} else {
		body := "#!/bin/sh\n" +
			"printf '%s\\n' \"$*\" > \"$AGENTS_STUB_LOG\"\n"
		if err := os.WriteFile(agentsBin, []byte(body), 0o755); err != nil {
			t.Fatalf("write agents stub: %v", err)
		}
	}

	t.Setenv("AGENTS_BIN", agentsBin)
	t.Setenv("AGENTS_HOME", filepath.Join(root, ".agents"))
	t.Setenv("AGENTS_BIN_SCRIPT", "")
	t.Setenv("AGENTS_STUB_LOG", logPath)

	stateRoot := filepath.Join(root, "runtime")
	if err := identity.Init(identity.InitOptions{Root: stateRoot, Role: identity.RoleCPU}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read agents log: %v", err)
	}
	want := "node init --root " + stateRoot + " --role cpu"
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("agents args = %q, want %q", strings.TrimSpace(string(got)), want)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "runtime", "live")); !os.IsNotExist(err) {
		t.Fatalf("harness created runtime layout; stat err = %v, want not exist", err)
	}
}

func TestReadNodeYAMLConsumesManagerMaterializedProjection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "node.yaml"), []byte("role: gateway\nhostname: s002\n"), 0o600); err != nil {
		t.Fatalf("write node.yaml: %v", err)
	}

	node, err := identity.ReadNodeYAML(root)
	if err != nil {
		t.Fatalf("ReadNodeYAML: %v", err)
	}
	if node.Role != identity.RoleGateway {
		t.Fatalf("node.Role = %q, want %q", node.Role, identity.RoleGateway)
	}
	if node.Hostname != "s002" {
		t.Fatalf("node.Hostname = %q, want s002", node.Hostname)
	}
}

func TestNodeInitInvalidRole(t *testing.T) {
	err := identity.Init(identity.InitOptions{Root: t.TempDir(), Role: "supernode"})
	if err == nil {
		t.Fatal("expected error for invalid role, got nil")
	}
}
