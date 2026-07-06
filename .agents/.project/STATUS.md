# Status

- Branch policy: use PRs into `dev`.
- CI: `.github/workflows/ci.yml` runs `bun run ci` directly; `packages/agents-manager` is a normal folder (packages/ restructure, 2026-07-06), so no submodule init step remains.
- Managed enforcement: DarkFactory baseline files are installed at the root.
- Workspace topology (#13): PR #26 adds `os/agents-workspace` as the global workspace and wires `AGENTS_DATA`/`AGENTS_WORKSPACE` into `agents-manager`; depends on agents-manager PR #17.
