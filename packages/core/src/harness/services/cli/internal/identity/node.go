// Package identity consumes the node identity contract materialized by
// os/agents-manager. Schema ownership lives in os/agents-core.
package identity

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Role is the node role accepted by the manager-owned node contract.
type Role string

const (
	// RoleClient is a user-facing node that connects to the cluster.
	RoleClient Role = "client"
	// RoleGateway is a cluster ingress / egress node.
	RoleGateway Role = "gateway"
	// RoleCPU is a CPU compute node.
	RoleCPU Role = "cpu"
	// RoleGPU is a GPU compute node.
	RoleGPU Role = "gpu"
)

// NodeYAML is the minimal node identity projection the harness consumes after
// agents-manager materializes the shared agents-core schema.
type NodeYAML struct {
	Role     Role   `yaml:"role"`
	Hostname string `yaml:"hostname"`
}

// InitOptions are the options for Init.
type InitOptions struct {
	Root string
	Role Role
}

// Init delegates node identity and runtime-root materialization to
// os/agents-manager. The harness must not write node.yaml, VERSION, or
// runtime/live itself.
func Init(opts InitOptions) error {
	if err := validateRole(opts.Role); err != nil {
		return err
	}
	if opts.Root == "" {
		return fmt.Errorf("identity: root must not be empty")
	}
	agentsBin := os.Getenv("AGENTS_BIN")
	if agentsBin == "" || os.Getenv("AGENTS_HOME") == "" {
		return fmt.Errorf("identity: node initialization is owned by os/agents-manager; set AGENTS_BIN and AGENTS_HOME or run through Agentos")
	}
	out, err := runAgents(agentsBin, "node", "init", "--root", opts.Root, "--role", string(opts.Role))
	if err != nil {
		return fmt.Errorf("identity: agents-manager node init failed: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// ReadNodeYAML reads the manager-materialized <root>/node.yaml projection.
func ReadNodeYAML(root string) (*NodeYAML, error) {
	path := filepath.Join(root, "node.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("identity: read node.yaml: %w", err)
	}
	var n NodeYAML
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("identity: parse node.yaml: %w", err)
	}
	return &n, nil
}

// BuildVersion is injected at build time via -ldflags="-X …/identity.BuildVersion=<ver>".
// The cmd/rommie main package exposes this via the --version flag.
var BuildVersion string

func validateRole(r Role) error {
	switch r {
	case RoleClient, RoleGateway, RoleCPU, RoleGPU:
		return nil
	default:
		return fmt.Errorf("identity: invalid role %q; must be one of client, gateway, cpu, gpu", r)
	}
}

func runAgents(agentsBin string, args ...string) (string, error) {
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
