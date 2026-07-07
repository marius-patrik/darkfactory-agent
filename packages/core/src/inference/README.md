# Agentos Inferer

Inference and runtime execution package migrated from Andromeda.

## Contents

- `python-agent/` contains the Python agent loop, engine contracts, capability execution, status machine, and tests.
- `engine-go/` contains the Go runtime engine, manager, daemon, queue, dispatch, GitHub, and store work.
- `services/` contains the Go coordination, daemon, db, inferctl, manager, and statesync modules.
- `deploy/` and `deploy-package/` contain deployment and cluster assets.
- `docs/` and `scripts/` carry the migrated inference architecture, acceptance, benchmark, and validation material.
- `legacy/src-root/` preserves the remaining Andromeda `src` root material that was not already promoted into a package.

## Runtime support

Supported execution lanes today:

- `daemon` capability execution for instruction, command, and local executable script capabilities.
- `daemon-inline` tool execution when a concrete lane is registered by the agent loop at runtime.

Reserved execution lanes:

- `knative` is a post-4.0 roadmap item. Capability manifests may route to it as `routing_only`, but the contract-level `KnativeExecLane` raises `NotImplementedError` for all operations until a concrete adapter is implemented and registered.
- `k3s-job` is a detached execution lane contract seam; it must be registered by a concrete runtime before use.

Supported engine kinds today:

- `vllm` for GPU-backed OpenAI-compatible serving.
- `llamacpp` for GGUF CPU/RAM hybrid serving.

Reserved engine kinds:

- `ktransformers` is planned for the 4.0 on-demand heavy reasoning tier and is not wired yet.
- `dynamo` is a post-4.0 roadmap item for NVIDIA Dynamo disaggregated prefill/decode, KV-aware routing, and multi-node tensor parallelism. The engine factory intentionally raises `NotImplementedError` until the adapter lands.

## Validation

Fast local validation is available from the repository root:

```sh
bun run validate
```

The same entrypoint is also exposed from `legacy/src-root` with `bun run validate`.
It runs the Python unit suite through `python -m uv` and the safe Go package tests
for `engine-go`, `services/coordination`, and `services/daemon`. On Windows, the
script uses `C:\Program Files\Go\bin\go.exe` when `go` is not on `PATH`.

Live gateway, Postgres, NATS, Docker, Kubernetes, release, and deployment checks
are intentionally outside the fast default path. Run those only in an environment
that has the required services and credentials.
