// Package agent implements the Agentos agent registry and persona model.
//
// Design reference: §19 RS2 — agents are personas/identities independent of the
// provider/model that serves them. The four provider agents are thin personas
// that simply default to their namesake provider.
package agent

// Provider names supported by the harness compatibility surface.
const (
	ProviderClaude = "claude"
	ProviderKimi   = "kimi"
	ProviderCodex  = "codex"
	ProviderAgy    = "agy"
)

// Providers is the ordered list of supported provider CLIs.
var Providers = []string{ProviderClaude, ProviderKimi, ProviderCodex, ProviderAgy}

// Agent is a persona/identity that can be selected for a session.
type Agent struct {
	Name            string
	Description     string
	DefaultProvider string
	Prompt          string
	Skills          []string
	Memory          []string
	History         []string
}

// Registry is the canonical set of agents seeded into the harness.
var Registry = func() map[string]Agent {
	base := Agent{
		Name:            "rommie",
		Description:     "The native Rommie agent — heavily populated with persona, skills, memory, and history.",
		DefaultProvider: ProviderClaude,
		Prompt:          "You are Rommie, the native Agentos harness agent. Coordinate tools, memory, and workers to complete tasks.",
		Skills:          []string{"memory", "compact", "sleep", "breathe", "reflect"},
		Memory: []string{
			"Prefer skills-first execution over ad-hoc shell one-liners.",
			"When memory is full, run compact before starting a new long-context task.",
			"Always emit structured JSON when --json is passed.",
		},
		History: []string{
			"Seeded from agents-harness H/3: agent personas split from provider routing.",
			"Provider agents claude/kimi/codex/agy default to their namesake provider.",
			"Rommie may be switched to any provider via the switcher.",
		},
	}

	m := map[string]Agent{
		"rommie": base,
		ProviderClaude: inherit(base, Agent{
			Name:            ProviderClaude,
			Description:     "Thin provider-tuned agent that defaults to the Claude provider.",
			DefaultProvider: ProviderClaude,
			Prompt:          "You are the Claude provider agent. Speak and work in the style tuned for Anthropic Claude.",
		}),
		ProviderKimi: inherit(base, Agent{
			Name:            ProviderKimi,
			Description:     "Thin provider-tuned agent that defaults to the Kimi provider.",
			DefaultProvider: ProviderKimi,
			Prompt:          "You are the Kimi provider agent. Speak and work in the style tuned for Moonshot Kimi.",
		}),
		ProviderCodex: inherit(base, Agent{
			Name:            ProviderCodex,
			Description:     "Thin provider-tuned agent that defaults to the Codex provider.",
			DefaultProvider: ProviderCodex,
			Prompt:          "You are the Codex provider agent. Speak and work in the style tuned for OpenAI Codex.",
		}),
		ProviderAgy: inherit(base, Agent{
			Name:            ProviderAgy,
			Description:     "Thin provider-tuned agent that defaults to the Agy / Gemini provider.",
			DefaultProvider: ProviderAgy,
			Prompt:          "You are the Agy provider agent. Speak and work in the style tuned for Google Gemini via Agy.",
		}),
	}
	return m
}()

// inherit returns a copy of child with empty persona fields filled from base.
// This lets provider agents stay thin while still having global skills/memory/history.
func inherit(base, child Agent) Agent {
	if len(child.Skills) == 0 {
		child.Skills = append([]string(nil), base.Skills...)
	}
	if len(child.Memory) == 0 {
		child.Memory = append([]string(nil), base.Memory...)
	}
	if len(child.History) == 0 {
		child.History = append([]string(nil), base.History...)
	}
	return child
}

// Names returns the canonical agent names in a stable order.
func Names() []string {
	return []string{"rommie", ProviderClaude, ProviderKimi, ProviderCodex, ProviderAgy}
}

// Get returns an agent by name, or ok=false if unknown.
func Get(name string) (Agent, bool) {
	a, ok := Registry[name]
	return a, ok
}

// ValidProvider reports whether name is a supported provider.
func ValidProvider(name string) bool {
	for _, p := range Providers {
		if p == name {
			return true
		}
	}
	return false
}
