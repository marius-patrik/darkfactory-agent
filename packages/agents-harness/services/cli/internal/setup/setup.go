// Package setup implements the local rommie structure setup checks.
package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/marius-patrik/agents-harness/services/cli/internal/cliwrap"
	"github.com/marius-patrik/agents-harness/services/cli/internal/identity"
)

// ExitCodeNoFalseGreen is the D3 exit code for a present-but-failing check.
const ExitCodeNoFalseGreen = 6

// Options controls setup execution.
type Options struct {
	Root             string
	Role             identity.Role
	MaterializeCreds bool
}

// Report is the setup command payload.
type Report struct {
	Root     string                `json:"root"`
	Role     identity.Role         `json:"role"`
	Hostname string                `json:"hostname"`
	Creds    []cliwrap.CredResult  `json:"creds,omitempty"`
	Checks   []cliwrap.CheckResult `json:"checks"`
}

// Run initializes the harness runtime structure and asks os/agents-manager to
// verify provider CLI adapters when manager state is available.
func Run(opts Options) (Report, error) {
	root := opts.Root
	if root == "" {
		root = defaultRoot()
	}
	role := opts.Role
	if role == "" {
		role = identity.RoleClient
	}

	nodePath := filepath.Join(root, "node.yaml")
	if _, err := os.Stat(nodePath); errors.Is(err, os.ErrNotExist) {
		if err := identity.Init(identity.InitOptions{Root: root, Role: role}); err != nil {
			return Report{}, err
		}
	} else if err != nil {
		return Report{}, err
	}

	node, err := identity.ReadNodeYAML(root)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		Root:     root,
		Role:     node.Role,
		Hostname: node.Hostname,
	}

	if agentsBin := os.Getenv("AGENTS_BIN"); agentsBin != "" && os.Getenv("AGENTS_HOME") != "" {
		return runManagedSetup(report, opts.MaterializeCreds, agentsBin)
	}

	for _, cli := range []string{"claude", "codex", "kimi", "agy"} {
		if opts.MaterializeCreds {
			report.Creds = append(report.Creds, cliwrap.CredResult{
				CLI:   cli,
				Error: "credential materialization is owned by os/agents-manager; set AGENTS_BIN and AGENTS_HOME",
			})
		}
		report.Checks = append(report.Checks, cliwrap.VersionCheckRoot(root, cli))
	}

	return report, nil
}

func runManagedSetup(report Report, materializeCreds bool, agentsBin string) (Report, error) {
	for _, cli := range []string{"claude", "codex", "kimi", "agy"} {
		rootedHome := cliwrap.RootDir("", cli)
		if materializeCreds {
			out, err := cliwrap.RunAgents(agentsBin, "cli", "materialize-creds", cli)
			if err != nil {
				return Report{}, err
			}
			materialized := !strings.Contains(out, " 0 credential")
			report.Creds = append(report.Creds, cliwrap.CredResult{
				CLI:          cli,
				SrcFound:     materialized,
				Materialized: materialized,
				Dst:          strings.TrimSpace(out),
			})
		}
		out, err := cliwrap.RunAgents(agentsBin, "cli", "doctor", cli)
		check := cliwrap.CheckResult{
			CLI:        cli,
			RootedHome: rootedHome,
			BinFound:   !strings.Contains(out, "binary=(missing)") && !strings.Contains(out, "missing binary"),
			OK:         err == nil,
		}
		if err != nil {
			check.Error = strings.TrimSpace(out)
		} else {
			check.Version = strings.TrimSpace(out)
		}
		report.Checks = append(report.Checks, check)
	}
	return report, nil
}

// HasPresentBinFailure returns true when a bin exists but rooted execution
// failed. Missing bins are reportable, not fatal, in S2.6 structure scope.
func HasPresentBinFailure(report Report) bool {
	for _, check := range report.Checks {
		if check.BinFound && !check.OK {
			return true
		}
	}
	return false
}

func defaultRoot() string {
	if v := os.Getenv("ROMMIE_HOME"); v != "" {
		return v
	}
	if v := os.Getenv("AGENTS_HOME"); v != "" {
		return filepath.Join(v, "harnesses", "agents-harness", "runtime")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".rommie"
	}
	return filepath.Join(home, ".rommie")
}
