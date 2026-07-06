# Commands

- `bun install --frozen-lockfile`
- `bun run check`
- `bun run test`
- `bun run ci`

Root CI needs no submodule initialization: `packages/agents-manager` is a normal folder since the packages/ restructure, and the root typecheck and test scripts run against it directly.
