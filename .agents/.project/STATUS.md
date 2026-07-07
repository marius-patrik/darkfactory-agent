# Status

- Branch policy: use PRs into `dev`.
- CI: `.github/workflows/ci.yml` runs `bun run ci` directly; the TypeScript CLI and tests live in `packages/core/src/manager` and `packages/core/test/manager`, so no submodule init step remains for the root validation path.
- Managed enforcement: DarkFactory baseline files are installed at the root.
- Workspace topology (#13): PR #26 adds `os/agents-workspace` as the global workspace and wires `AGENTS_DATA`/`AGENTS_WORKSPACE` into `agents-manager`; depends on agents-manager PR #17.
