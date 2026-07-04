# Commands

- `bun install --frozen-lockfile`
- `bun run check`
- `bun run test`
- `bun run ci`

Root CI initializes only `os/agents-manager` because the root typecheck and test scripts depend on that submodule.
