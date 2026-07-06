// Package switcher implements the agent/provider switcher persistence and
// resolution used by the rommie CLI.
//
// Design reference: §06 switchers + D3 C10. Scope precedence is
// session > project > global > agent default.
package switcher

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/marius-patrik/agents-harness/services/cli/internal/agent"
	"gopkg.in/yaml.v3"
)

// Scope identifies the configuration layer being read or written.
type Scope int

const (
	ScopeSession Scope = iota
	ScopeProject
	ScopeGlobal
)

// String returns the human-readable scope name.
func (s Scope) String() string {
	switch s {
	case ScopeSession:
		return "session"
	case ScopeProject:
		return "project"
	case ScopeGlobal:
		return "global"
	default:
		return "unknown"
	}
}

// Config holds the currently selected agent, provider, and model.
type Config struct {
	Agent    string `yaml:"agent,omitempty"`
	Provider string `yaml:"provider,omitempty"`
	Model    string `yaml:"model,omitempty"`
}

// DefaultRoot returns the default runtime root, honouring ROMMIE_HOME.
func DefaultRoot() string {
	if v := os.Getenv("ROMMIE_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".rommie"
	}
	return filepath.Join(home, ".rommie")
}

// Load reads the switcher config for the requested scope.
func Load(root string, scope Scope) (*Config, error) {
	path, err := configPath(root, scope)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode switcher config %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes the switcher config for the requested scope.
func Save(root string, scope Scope, cfg *Config) error {
	path, err := configPath(root, scope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Resolve returns the effective agent, provider, and model for the current
// session. Precedence: session/project/global overrides, then the agent's
// default provider. Provider/model overrides are scoped to the same layer that
// supplied the agent, so switching to a provider agent in a higher scope does
// not inherit a stale provider override from a lower scope.
func Resolve(root string) (string, string, string, error) {
	session, err := Load(root, ScopeSession)
	if err != nil {
		return "", "", "", err
	}
	project, err := Load(root, ScopeProject)
	if err != nil {
		return "", "", "", err
	}
	global, err := Load(root, ScopeGlobal)
	if err != nil {
		return "", "", "", err
	}

	agentName, agentScope := resolveAgent(session.Agent, project.Agent, global.Agent)
	a, ok := agent.Get(agentName)
	if !ok {
		return "", "", "", fmt.Errorf("unknown agent %q", agentName)
	}

	provider := resolveProvider(agentScope, session, project, global, a.DefaultProvider)
	if !agent.ValidProvider(provider) {
		return "", "", "", fmt.Errorf("unknown provider %q", provider)
	}

	model := resolveModel(agentScope, session, project, global)

	return agentName, provider, model, nil
}

// ResolveAgent returns the effective agent without resolving provider/model
// switcher state. This is used by explicit provider dispatch, where the caller
// supplied the provider but still needs the selected persona.
func ResolveAgent(root string) (string, error) {
	session, err := Load(root, ScopeSession)
	if err != nil {
		return "", err
	}
	project, err := Load(root, ScopeProject)
	if err != nil {
		return "", err
	}
	global, err := Load(root, ScopeGlobal)
	if err != nil {
		return "", err
	}

	agentName, _ := resolveAgent(session.Agent, project.Agent, global.Agent)
	if _, ok := agent.Get(agentName); !ok {
		return "", fmt.Errorf("unknown agent %q", agentName)
	}
	return agentName, nil
}

func resolveAgent(sessionAgent, projectAgent, globalAgent string) (string, Scope) {
	if sessionAgent != "" {
		return sessionAgent, ScopeSession
	}
	if projectAgent != "" {
		return projectAgent, ScopeProject
	}
	if globalAgent != "" {
		return globalAgent, ScopeGlobal
	}
	return "rommie", ScopeGlobal + 1 // default agent; allow full provider fall-through
}

func resolveProvider(agentScope Scope, session, project, global *Config, defaultProvider string) string {
	switch agentScope {
	case ScopeSession:
		return firstNonEmpty(session.Provider, defaultProvider)
	case ScopeProject:
		return firstNonEmpty(project.Provider, defaultProvider)
	case ScopeGlobal:
		return firstNonEmpty(global.Provider, defaultProvider)
	default:
		return firstNonEmpty(session.Provider, project.Provider, global.Provider, defaultProvider)
	}
}

func resolveModel(agentScope Scope, session, project, global *Config) string {
	switch agentScope {
	case ScopeSession:
		return session.Model
	case ScopeProject:
		return project.Model
	case ScopeGlobal:
		return global.Model
	default:
		return firstNonEmpty(session.Model, project.Model, global.Model)
	}
}

// SetAgent persists the selected agent at the given scope.
func SetAgent(root string, scope Scope, name string) error {
	if _, ok := agent.Get(name); !ok {
		return fmt.Errorf("unknown agent %q", name)
	}
	cfg, err := Load(root, scope)
	if err != nil {
		return err
	}
	cfg.Agent = name
	return Save(root, scope, cfg)
}

// SetProvider persists the selected provider at the given scope.
func SetProvider(root string, scope Scope, name string) error {
	if !agent.ValidProvider(name) {
		return fmt.Errorf("unknown provider %q", name)
	}
	cfg, err := Load(root, scope)
	if err != nil {
		return err
	}
	cfg.Provider = name
	return Save(root, scope, cfg)
}

// SetModel persists the selected model at the given scope.
func SetModel(root string, scope Scope, model string) error {
	cfg, err := Load(root, scope)
	if err != nil {
		return err
	}
	cfg.Model = model
	return Save(root, scope, cfg)
}

// ParseScope resolves CLI scope flags into a Scope value.
// If no flag is set it defaults to project scope so switches persist across
// invocations. More than one flag is an error.
func ParseScope(session, project, global bool) (Scope, error) {
	count := 0
	if session {
		count++
	}
	if project {
		count++
	}
	if global {
		count++
	}
	if count > 1 {
		return ScopeSession, fmt.Errorf("only one of --session, --project, or --global may be set")
	}
	if session {
		return ScopeSession, nil
	}
	if project {
		return ScopeProject, nil
	}
	if global {
		return ScopeGlobal, nil
	}
	return ScopeProject, nil
}

func configPath(root string, scope Scope) (string, error) {
	switch scope {
	case ScopeSession:
		return filepath.Join(root, "shared", "session-switcher.yaml"), nil
	case ScopeProject:
		return filepath.Join(".rommie", "switcher.yaml"), nil
	case ScopeGlobal:
		return filepath.Join(root, "shared", "switcher.yaml"), nil
	default:
		return "", fmt.Errorf("unknown scope %d", scope)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
