# Commands

- `bun install --frozen-lockfile`
- `bun run check`
- `bun run test`
- `bun run ci`

Root CI needs no submodule initialization: the TypeScript CLI and tests are consolidated under `packages/core`, and the root typecheck and test scripts run against `packages/core/src/manager` and `packages/core/test/manager`.
