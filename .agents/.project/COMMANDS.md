# Commands

```sh
bun install --frozen-lockfile
bun run check
bun run test
bun run ci
bun run smoke:release
```

Component-focused validation still runs from the repository root unless a
subshell is shown:

```sh
# Core contracts and generated clients
bunx tsc --noEmit -p packages/core/tsconfig.json
bunx buf lint packages/core/proto
bun scripts/verify-codegen.ts
(cd packages/core/contracts-go && go test ./...)

# Harness behavior
bun test packages/manager/test/session.test.ts \
  packages/manager/test/session-adapters.test.ts \
  packages/manager/test/tui-tools.test.ts

# Gateway package
(
  cd packages/gateway
  uv sync --frozen
  uv run ruff check llm_gateway tests scripts
  uv run mypy llm_gateway
  uv run pytest -q -m 'not live'
  AGENTS_HOME=/absolute/disposable/.agents \
  AGENTS_USER_HOME=/absolute/disposable \
  AGENTS_ROOT=/absolute/Andromeda \
    uv run python scripts/packaging_smoke.py
  uv build
)

# Inference package
bun packages/inference/scripts/validate.mjs
```

Explicit temporary or live state commands must always set all three roots:

```sh
AGENTS_HOME=/absolute/.agents \
AGENTS_USER_HOME=/absolute/user-home \
AGENTS_ROOT=/absolute/Andromeda \
  bun run agents -- state doctor
```

Never run tests with the personal state root unless the test is an intentional
read-only installed-boundary proof.
