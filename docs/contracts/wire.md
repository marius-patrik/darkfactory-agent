# Agent OS wire contract reference

**Status:** active supporting reference. The root [PRD](../../PRD.md) owns
product specification, and the protobuf module under
[`src/migrate/core/proto/agent_os/v1/`](../../src/migrate/core/proto/agent_os/v1) is
the executable wire authority. This document maps that checked-in schema; its
legacy section and decision labels are provenance, not a competing plan or
authorization surface.

> **The single most load-bearing artifact in the system.** Everything — gateway,
> agent loop, TUI, web — builds against this. Source of truth = the protobuf
> module under [`src/migrate/core/proto/agent_os/v1/`](../../src/migrate/core/proto/agent_os/v1); this doc is its map.
>
> **Transport (design §00 S3, LOCKED):** two layers, **one** protobuf schema:
> 1. **Control plane = Connect (protobuf)** — unary RPCs, generated typed
>    clients/servers in Go + TS + Python.
> 2. **Live session = WebSocket** carrying the **same** protobuf-typed frames —
>    true duplex, browser + terminal native, multi-client attach.
>
> **No tRPC, no Prisma, no gRPC-web/Envoy** (§00 S3, D-023). The TUI and web app
> share the same generated Connect client + WS frame stream.

---

## 1. Module layout

| File | Holds |
|------|-------|
| `common.proto` | Shared enums + types: `SwitcherAxis/Scope`, `Fabric`, `PermissionMode`, `RunStatus` (the §15 OR2 no-false-green vocab), `Host/Provider/Model/SwitcherState/Node/Usage/Error/Task`. |
| `registry.proto` | `RegistryService` — `ListModels/ListProviders/ListHosts/ListNodes`. |
| `switchers.proto` | `SwitcherService` — `GetSwitcherState/SetSwitcher/ListSwitcherOptions` across the four/five axes (§06). |
| `sessions.proto` | `SessionService` — `CreateSession/ListSessions/AttachSession/ForkSession` (§01 G2, §05 L1, §04). |
| `jobs.proto` | `JobService` + `DomainService` — jobs/domains `List`/`Status` (§07). |
| `health.proto` | `HealthService` — `GetHealth` (cluster/fabric/PG snapshot + `paused`). |
| `session_frames.proto` | The **WebSocket** session frames: `ServerFrame` + `ClientFrame` envelopes (§01 G2). |

All in package `agent_os.v1`.

---

## 2. Control-plane RPC inventory (Connect)

| Service | RPC | Purpose | Design ref |
|---------|-----|---------|-----------|
| **RegistryService** | `ListModels` | Registry models (filter by fabric/provider/role) | §01 G2/G4 |
| | `ListProviders` | Providers under a fabric | §06 SW1 |
| | `ListHosts` | Tool-exec hosts + liveness | §06 SW1 |
| | `ListNodes` | Cluster nodes | §00 S4 |
| **SwitcherService** | `GetSwitcherState` | Resolved `{host,fabric,provider,model,agent,scope_source}` | §06 SW3 |
| | `SetSwitcher` | Set one axis at session/project/global scope | §06 SW2/SW3 |
| | `ListSwitcherOptions` | Options per axis, with liveness | §06 SW3 |
| **SessionService** | `CreateSession` | New server-side session (+ optional seed Task/switcher) | §05 L1 |
| | `ListSessions` | Resume surface (live + recent) | D4 §8 |
| | `AttachSession` | Resolve the owning node's WS endpoint (affinity) | §01 G5 |
| | `ForkSession` | Branch the CRDT log (rewind = fork-only, D-029) | §04 |
| **JobService** | `ListJobs` / `GetJobStatus` | Jobs/runs list + status | §07 D5b |
| **DomainService** | `ListDomains` / `GetDomainStatus` | Domains list + status (+ runs) | §07 D1/D6 |
| **HealthService** | `GetHealth` | Cluster/fabric/PG health + `paused` + usage | §01 G2, §11 |

These are the RPCs named in §01 G2 / §06 SW3 / §07. **Nothing beyond the docs was
invented.** `SetSwitcher` rejects invalid combos with the typed `Error` (e.g.
`cloud_disabled`, `host_offline`) per §06 SW3.

---

## 3. Session frame inventory (WebSocket)

The WS channel carries **two envelope messages**, one per direction; each is a
`oneof` so a single wire type carries any frame. Vocabulary is **normalized /
provider-agnostic** (§01 G2) — clients render one way regardless of provider.

### server → client (`ServerFrame`, with a monotonic `seq` for CRDT-resume ordering)
| Frame | Meaning | Design ref |
|-------|---------|-----------|
| `text_delta` | streamed assistant text (attributed by `worker_id`) | §01 G2 |
| `thinking` | streamed reasoning | §01 G2 |
| `tool_call` | loop dispatched a tool to a host | §05 L2 |
| `tool_result` | tool observation (+ OR2 `status`, artifact ref) | §05 L2 |
| `approval_request` | gated tool awaiting the user (mode + worker/role) | §05 L3 |
| `status` | turn/loop status (status line) | §01 G2 |
| `usage` | token/budget rollup + degrade-to-local flag | §01 G6 |
| `error` | typed error (same `Error` shape as Connect) | §06 SW3 |
| `session_event` | **the extensible state frame — see §4** | §01 G2 + D-029 |

### client → server (`ClientFrame`)
| Frame | Meaning | Design ref |
|-------|---------|-----------|
| `user_input` | a prompt (+ `@`-mentions) | §01 G2 |
| `approval_response` | allow-once / allow-session / allow-always / deny | §05 L3 |
| `interrupt` | interrupt the current turn (`Esc`) | §01 G2 |
| `switch` | mid-turn session-scope switcher change | §06 SW3 |
| `attach` | attach / detach this client | §01 G2 |

---

## 4. The D-029 `session_event` extensibility rule (load-bearing)

> **D-029 (TUI bullet):** worker/brain state on the wire **EXTENDS the
> `session_event` payload** (`worker_id` / `role` / `parent` / `status` /
> `claim` / `model` / `phase`) — **NO new frame types**.

`SessionEvent` is the single carrier for *all* session state changes:
`todo/plan`, `role/worker`, `switcher`, `mode`, `compaction`, `attach`, and the
require-all-nodes `pause`. A `SessionEventKind` discriminator selects which arm
of the payload `oneof` is populated:

```
SessionEvent { kind; oneof payload {
    switch | mode | plan | worker | compaction | attach | pause } }
```

The brain/worker dimension is carried by `WorkerState`
(`worker_id, role, parent, status, model, phase, claim, claim_ttl_seconds,
activity`) — exactly the D-029 field list. **Adding a future state dimension is a
new `oneof` arm on `SessionEvent` (and/or a new `SessionEventKind`), NEVER a new
top-level frame type.** This is what lets the simplest-first build ship the
idle-only feed in S2 yet have the full multi-worker brain view (S4) plug onto an
existing stream with no contract change (D4 §12).

`RunStatus` everywhere uses the §15 OR2 no-false-green vocabulary — a green/
useful state is **only** `useful_result` or `released`; all other states are
explicitly not-green.

---

## 5. Codegen how-to

One proto module -> language stubs via **buf remote plugins** (no local protoc
needed). From the **repo root**:

```sh
(cd src/migrate/core && bunx --bun @bufbuild/buf generate proto)
bunx --bun @bufbuild/buf lint src/migrate/core/proto
```

Default outputs (all committable, all in this repo):

| Lang | Output | Plugins | Runtime dep |
|------|--------|---------|-------------|
| **Go** | `src/migrate/core/contracts-go/gen` | `protocolbuffers/go` + `connectrpc/go` | `connectrpc.com/connect`, `google.golang.org/protobuf` (`go mod tidy`) |
| **TS** | `src/migrate/core/clients/shared-ts/src/gen` | `bufbuild/es` (protobuf-es v2) | `@bufbuild/protobuf`, `@connectrpc/connect` (`bun install`) |

- **Go:** Connect service stubs land in `gen/agent_os/v1/agent_osv1connect`; messages
  in `gen/agent_os/v1`. `go_package` is injected by buf managed mode.
- **TS:** protobuf-es v2 emits the Connect-compatible service descriptors inside
  the `*_pb.ts` files; `@connectrpc/connect` v2 builds typed clients from them
  via `createClient(RegistryService, transport)` — so **no separate connect-es
  codegen** is needed. Re-exported from `@agent-os/shared-ts/gen`.
- **Python:** plain protobuf `*_pb2.py` + `*_pb2.pyi` for the in-repository
  Agent OS inference consumer are generated with the opt-in template:
  ```sh
  (cd src/migrate/core && bunx --bun @bufbuild/buf generate proto --template buf.gen.python.yaml)
  ```
  That writes to `src/migrate/inference/python-agent/agent/gen` and is run whenever the
  in-repository Python consumer changes. Import as:
  ```python
  import agent.gen                      # puts the gen root on sys.path
  from agent_os.v1 import session_frames_pb2, registry_pb2
  ```

**Regenerate after any `.proto` edit** and commit the stubs alongside the proto.

### Package markers and generated outputs

`buf generate proto` / `protoc` emits the generated files listed below. Python
package markers and the TypeScript export barrel are source files and are
validated separately from generated output.

| File | Kind | Purpose |
|------|------|---------|
| `src/migrate/inference/python-agent/agent/gen/__init__.py` | source | `sys.path` bootstrap so `from agent_os.v1 import ...` resolves after `import agent.gen` |
| `src/migrate/inference/python-agent/agent/gen/agent_os/__init__.py` | source | Python package marker for the `agent_os` namespace |
| `src/migrate/inference/python-agent/agent/gen/agent_os/v1/__init__.py` | source | Python package marker for the `agent_os.v1` namespace |
| `src/migrate/core/clients/shared-ts/src/gen/index.ts` | hand-authored | Re-export barrel so consumers import from `@agent-os/shared-ts/gen` |
| `src/migrate/inference/python-agent/agent/gen/agent_os/v1/*_pb2.py` | **buf-generated** | Protobuf message classes - regenerate, never hand-edit |
| `src/migrate/inference/python-agent/agent/gen/agent_os/v1/*_pb2.pyi` | **buf-generated** | Type stubs - regenerate, never hand-edit |
| `src/migrate/core/clients/shared-ts/src/gen/agent_os/v1/*_pb.ts` | **buf-generated** | protobuf-es v2 message + service descriptors — regenerate, never hand-edit |
| `src/migrate/core/contracts-go/gen/...` | **buf-generated** | Go message + Connect stubs - regenerate, never hand-edit |

### Python bindings

The inference worker consumes the plain protobuf bindings below
`src/migrate/inference/python-agent/agent/gen`. The gateway consumes the generated
protobuf and Connect bindings below `src/migrate/gateway/agent_os/v1` and mounts
the implemented services described in [Gateway architecture](../gateway.md).

---

## 6. Historical schema decisions

Where a doc was ambiguous, the simplest doc-aligned reading was chosen:

1. **Session lifecycle verbs.** §01 G2 names `CreateSession`/`ForkSession`/
   `ListSessions`; D4 §8 adds attach. Resolved to **create/list/attach/fork**.
   `AttachSession` returns an `AttachInfo` (the owning node's WS URL) rather than
   streaming over Connect — the live stream is WebSocket (§01 G5 affinity), so
   Connect only *resolves where to dial*.
2. **`switch` on two surfaces.** §06 SW3 exposes both a Connect `SetSwitcher` and
   a WS `switch` frame. Both are kept: control-plane (any scope) = `SetSwitcher`;
   mid-turn session-scope = the `switch` WS frame. A `switch` is acknowledged by
   a `session_event(kind=SWITCH)` (§06 SW3 "server replies with a
   `session_event`").
3. **Jobs vs domains.** §07 distinguishes a domain (long-lived orchestrator) from
   a job/run (a (sub-)session). They are modeled as two services
   (`DomainService`, `JobService`) with list and status RPCs in the checked-in
   schema.
4. **`agent` axis.** Agent ⊥ provider (D-011/RS2) but is selected via the same
   switcher surface (§06/D4 §4.1), so `SWITCHER_AXIS_AGENT` is included on the
   axis enum and `SwitcherState.agent` carries it.
5. **Tool-call argument shape.** §01 G2 mandates a normalized provider-agnostic
   vocabulary but no fixed per-tool schema. `ToolCall.args` uses
   `google.protobuf.Struct` (open structured args) rather than inventing a tool
   schema the docs don't specify.
6. **Approval answers.** D4 §7.3 lists four answers (allow once / allow session /
   always-allow rule / deny) → `ApprovalResponse.Decision` enum, verbatim.
7. **`paused` field.** D-029 (CLI/PAUSE) + §00 S4 require a pause indicator.
   Surfaced as `GetHealthResponse.paused` and a `SessionEvent(kind=PAUSE)` — no
   new frame type.

---

## 7. Acceptance (run from the worktree root)

```sh
bun install
bun run check

# Refresh and validate the in-repository Python consumer:
(cd src/migrate/core && bunx --bun @bufbuild/buf generate proto --template buf.gen.python.yaml)
(cd src/migrate/inference/python-agent && uv sync && uv run python -c "import agent.gen; from agent_os.v1 import session_frames_pb2")
```

---

## 8. Internal contract surface and in-repository consumers

`src/migrate/core` is an internal implementation subtree, not an independently
published package or CLI. Its generated stubs are checked into this repository
for Andromeda's in-repository consumers. The package names and import specifiers
below are internal module identities; the JavaScript workspaces remain private,
and none of these generated clients is a supported external distribution
surface.

| Language | Internal path / module identity | Import example | In-repository consumer |
|----------|---------------------------------|----------------|------------------------|
| **Go** | `src/migrate/core/contracts-go` | `agent_osv1 "github.com/marius-patrik/agents-manager/src/migrate/core/contracts-go/gen/agent_os/v1"` | Go services under `src/migrate/inference/` |
| **Go Connect** | `src/migrate/core/contracts-go/gen/agent_os/v1/agent_osv1connect` | `import "github.com/marius-patrik/agents-manager/src/migrate/core/contracts-go/gen/agent_os/v1/agent_osv1connect"` | Go services that speak the Connect control plane |
| **TypeScript** | private workspace `@agent-os/shared-ts` | `import { RegistryService } from "@agent-os/shared-ts/gen"` | `src/migrate/core/clients/tui` and `src/migrate/core/clients/web` |
| **Python** | `agent.gen` bootstrap + `agent_os.v1` | `import agent.gen; from agent_os.v1 import session_frames_pb2, registry_pb2` | `src/migrate/inference/python-agent` |

`src/migrate/core/clients/tui` and `src/migrate/core/clients/web` are currently
private placeholder workspaces. They import the private `@agent-os/shared-ts`
workspace and will host Andromeda's future in-repository TUI and web clients.
There is no user-facing CLI or installer in `src/migrate/core`.
