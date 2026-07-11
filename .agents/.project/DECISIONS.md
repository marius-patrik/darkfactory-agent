# Decisions

## 2026-07-11 — One Agent OS authority

- Shared identity, memory, roles, skills, sessions, providers, and model choices
  are authoritative only under `$AGENTS_HOME`.
- DarkFactory synchronizes repository-local guidance and policy; it does not
  publish or version a copied global agent floor.
- `agent-os-data` at `$AGENTS_ROOT/data/agent-os` is the only data registration;
  its repository is `marius-patrik/agents-data`.
- Local worker execution uses the canonical `agents` launcher and its configured
  defaults. DarkFactory carries no provider registry, failover list, or model pin.
- The Codex pull-request reviewer remains an isolated CI-only execution path and
  relies on the Codex CLI default model.
- Managed setup is reviewable through pull requests and never bypasses protected
  default branches.
