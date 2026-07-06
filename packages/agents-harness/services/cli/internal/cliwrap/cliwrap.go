// Package cliwrap delegates vendor CLI operations to os/agents-manager.
package cliwrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CLISpec identifies a provider CLI name accepted by the harness compatibility
// surface. Adapter rooting and credentials are owned by os/agents-manager.
type CLISpec struct {
	Name string
}

// Specs is the compatibility list of provider CLIs the harness can delegate.
var Specs = map[string]CLISpec{
	"claude": {Name: "claude"},
	"codex":  {Name: "codex"},
	"kimi":   {Name: "kimi"},
	"agy":    {Name: "agy"},
	"gemini": {Name: "agy"},
}

// Error carries the intended rommie exit code for wrapper failures.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string {
	return e.Msg
}

// RootDir returns the agents-manager-owned CLI home for cli, if AGENTS_HOME is
// present. The harness no longer creates or owns provider homes.
func RootDir(home, cli string) string {
	if agentsHome := os.Getenv("AGENTS_HOME"); agentsHome != "" {
		spec, ok := Specs[cli]
		if !ok {
			return filepath.Join(agentsHome, "clis", cli)
		}
		return filepath.Join(agentsHome, "clis", spec.Name)
	}
	return ""
}

// Exec delegates vendor CLI execution to os/agents-manager.
func Exec(cli string, args []string) error {
	return ExecWithEnv(cli, args, nil)
}

// ExecWithEnv delegates vendor CLI execution to os/agents-manager, injecting
// additional environment variables into the child process.
func ExecWithEnv(cli string, args []string, env []string) error {
	spec, err := specFor(cli)
	if err != nil {
		return err
	}
	agentsBin, err := requireAgentsManager()
	if err != nil {
		return err
	}
	return execViaAgents(agentsBin, spec.Name, args, env)
}

func execViaAgents(agentsBin, cli string, args []string, extraEnv []string) error {
	argv := []string{}
	if script := os.Getenv("AGENTS_BIN_SCRIPT"); script != "" {
		argv = append(argv, script)
	}
	argv = append(argv, "cli", "exec", cli, "--")
	argv = append(argv, args...)
	cmd := exec.Command(agentsBin, argv...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// VersionCheck asks os/agents-manager to verify the provider CLI.
func VersionCheck(cli string) CheckResult {
	return VersionCheckRoot("", cli)
}

// VersionCheckRoot is VersionCheck using a compatibility signature retained for
// callers; the root is ignored because CLI homes are manager-owned.
func VersionCheckRoot(home, cli string) CheckResult {
	result := CheckResult{
		CLI: cli,
	}
	spec, err := specFor(cli)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.CLI = spec.Name
	result.RootedHome = RootDir("", spec.Name)

	agentsBin, err := requireAgentsManager()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	out, err := RunAgents(agentsBin, "cli", "doctor", spec.Name)
	result.BinFound = !strings.Contains(out, "binary=(missing)") && !strings.Contains(out, "missing binary")
	if err != nil {
		result.Error = strings.TrimSpace(out)
		return result
	}
	result.Version = strings.TrimSpace(out)
	result.OK = true
	return result
}

// CheckResult is the setup-time rooting verification result for one CLI.
type CheckResult struct {
	CLI        string `json:"cli"`
	RootedHome string `json:"rooted_home"`
	BinFound   bool   `json:"bin_found"`
	Version    string `json:"version,omitempty"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// CredResult is retained for setup's JSON shape when it asks agents-manager to
// materialize credentials. It intentionally carries only metadata.
type CredResult struct {
	CLI          string `json:"cli"`
	SrcFound     bool   `json:"src_found"`
	Materialized bool   `json:"materialized"`
	Dst          string `json:"dst,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Error        string `json:"error,omitempty"`
}

func specFor(cli string) (CLISpec, error) {
	spec, ok := Specs[cli]
	if !ok {
		return CLISpec{}, &Error{Code: 2, Msg: fmt.Sprintf("unknown cli: %s (claude|codex|kimi|agy|gemini)", cli)}
	}
	return spec, nil
}

func requireAgentsManager() (string, error) {
	agentsBin := os.Getenv("AGENTS_BIN")
	agentsHome := os.Getenv("AGENTS_HOME")
	if agentsBin == "" || agentsHome == "" {
		return "", &Error{Code: 2, Msg: "provider CLI adapters are owned by os/agents-manager; set AGENTS_BIN and AGENTS_HOME or run this through Agentos"}
	}
	return agentsBin, nil
}

// RunAgents invokes the manager CLI and returns combined stdout/stderr.
func RunAgents(agentsBin string, args ...string) (string, error) {
	argv := []string{}
	if script := os.Getenv("AGENTS_BIN_SCRIPT"); script != "" {
		argv = append(argv, script)
	}
	argv = append(argv, args...)
	cmd := exec.Command(agentsBin, argv...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}
