# Agents Manager Commands

Run from the repository root:

```powershell
bun install
bun run check
bun run test
bun run ci
```

`bun run ci` must remain equivalent to `bun run check && bun run test`.

## Agent state consolidation

```powershell
agents state status [--json]
agents state adopt <claude|codex|kimi> [--dry-run]
agents state sync [--dry-run]
```

- `status` reports whether each known tool state dir is `in-place`, `adopted`, `missing`, or `conflict`, plus the state-repo state.
- `adopt` moves the original tool state dir to `~/.agents/state/<tool>` and leaves a junction at the original path. Idempotent; refuses locked or conflicting dirs.
- `sync` clones the private `agents-data` repo into `~/.agents/state-repo`, copies the allowlisted shareable subset into `machines/<hostname>/`, commits, rebases, and pushes.
