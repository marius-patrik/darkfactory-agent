# Contract surface

Shared Andromeda wire contracts and the generated client packages built from
them. This used to be a `core` component of its own; the contracts have since
moved to their owners and this document is the map.

## Where things live

- `packages/mcp/proto/` holds the canonical Andromeda protobuf wire contract.
- `packages/mcp/buf.gen*.yaml` are the generation templates. Codegen runs with
  `packages/mcp` as its working directory.
- `packages/sdk/contracts-go/` holds the generated Go protobuf and Connect stubs.
- `packages/sdk/shared-ts/` holds the generated TypeScript descriptors and the
  shared client exports.
- `packages/sdk/tests/` verifies the surface: TypeScript and Python import
  smokes, and the codegen retry behaviour.
- `packages/mcp/contracts/` holds the protocol, engine, execution-lane, and
  worker lifecycle contracts.

## Package surface

The contract surface is a library, not a CLI or end-user application. It ships
generated wire-contract stubs for downstream components:

- **Go:** module `github.com/marius-patrik/andromeda/packages/sdk/contracts-go`
  - Messages: `andromedav1 "github.com/marius-patrik/andromeda/packages/sdk/contracts-go/gen/andromeda/v1"`
  - Connect services: `"github.com/marius-patrik/andromeda/packages/sdk/contracts-go/gen/andromeda/v1/andromedav1connect"`
  - Consumers: the Go services under `packages/server/inference/`.
  - The module path still says `packages/core` while the directory is
    `packages/sdk/contracts-go`. That identity is embedded in the generated
    descriptors, so changing it is a codegen change rather than a rename.
- **TypeScript:** private workspace `@marius-patrik/andromeda-sdk`
  - Shared descriptors and types: `@marius-patrik/andromeda-sdk/gen`
  - Consumers: `packages/web` and `packages/app`, both still placeholders.
- **Python:** plain protobuf stubs generated to
  `packages/server/inference/python-agent/agent/gen` and
  `packages/server/gateway/andromeda`
  - Bootstrap: `import agent.gen`
  - Messages: `from andromeda.v1 import session_frames_pb2, registry_pb2`
  - Consumers: the inference Python agent and the gateway.

`contracts-go/cmd/contracts-go` is a development placeholder; downstream
services consume the generated module directly.

## Regenerating

Verify freshness from the repository root:

```sh
bun scripts/verify-codegen.ts
```

That regenerates into a scratch tree and fails if the committed output differs,
which is how CI enforces freshness. To regenerate in place:

```sh
cd packages/mcp
bunx --bun @bufbuild/buf generate proto
bunx --bun @bufbuild/buf generate proto --template buf.gen.python.yaml
```

The first updates the committed Go and TypeScript outputs; the second refreshes
the Python stubs for the inference and gateway consumers.

## Validation

Run the full package validation from the repository root:

```sh
bun install
bun run check
```

The committed `bun.lock` is the lockfile for the TypeScript workspaces. On
Windows, prefix `PATH` with `C:/Program Files/Go/bin` before `bun run check` if
`go` is not already available.
