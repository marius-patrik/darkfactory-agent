# Status

- Repository: `marius-patrik/agent-darkfactory`
- Branch: `reconcile/single-agent-os`
- Product role: Agent OS GitHub control plane
- Managed policy source: the `managed-repository` child of the sole
  `agent-os-data` checkout at `$AGENTS_ROOT/data/agent-os`
- Managed executable source: this DarkFactory package; duplicate payloads in
  managed data fail closed
- Shared state authority: `$AGENTS_HOME`
- Local worker authority: canonical `agents` launcher
- CI reviewer: isolated Codex-only job with no repository model pin
- Validation gate: `npm run check`
