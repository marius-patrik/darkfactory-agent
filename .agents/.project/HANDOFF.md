# Handoff

Andromeda v0.2.2 acceptance is complete. Resume planning from `data/agent-os/context/TASK.md`; do not recreate completed rows from stale Fable, Claude, Codex, or Dream task stores.

Windows and Mac both install exact `main@d7bafd4f660c`. Explicit-root state doctors are green, encrypted exchange replays idempotently in both directions, and no prepared imports remain. Use the commands below to re-verify live state rather than inferring it from this file.

```powershell
$env:AGENTS_HOME = "$HOME\.agents"
$env:AGENTS_USER_HOME = "$HOME"
$env:AGENTS_ROOT = "$HOME\marius-patrik\Andromeda"
& "$env:AGENTS_HOME\bin\agents.ps1" state doctor --json
```

```sh
AGENTS_HOME="$HOME/.agents" \
AGENTS_USER_HOME="$HOME" \
AGENTS_ROOT="$HOME/marius-patrik/Andromeda" \
  "$HOME/.agents/bin/agents" state doctor --json
```

The Backlog row remains deferred. The final Parked row remains frozen until Patrik explicitly reopens an item.
