# Agents OS Architecture

`agents-os` is the Linux container distribution for the one-system agents
program. Its first job is to make the whole system runnable from one local
Docker container while preserving the existing `agents` package manager,
git-backed package layout, shared state root, data repositories, and workspace
topology.

This document specifies the architecture contract for agents-mono #8. It is the
input for the image and release work in #9, the lifecycle CLI work in #10, the
inference-engine and llm-gateway profiles in #11, the workspace topology in
#13, and the capstone full-system proof in #14.

## Scope

`agents-os` defines:

- A releaseable container image assembled from agents-manager package and
  environment metadata.
- A deterministic host launcher contract for `agents` and `agents os`.
- A shared mount and environment contract for agents system state,
  DarkFactory operational state, global workspaces, and per-agent workspaces.
- Runtime profiles for the harness, inference-engine, llm-gateway, DarkFactory
  adapter, and plugins.
- Local Docker as the MVP runtime on the owner's machine.

`agents-os` does not implement #9, #10, or #11 in this spec. Those issues must
use the contracts below as their acceptance source.

## One-System Layering

The container runs one integrated system, not separate products:

- `os/agents-manager` is the package, state, secrets, environment, and launcher
  substrate. The host `agents` CLI owns image selection, container lifecycle,
  environment materialization, and the TUI.
- `os/agents-harness` is the orchestration engine. It is the long-term home for
  workers, streams, scheduling, memory, and runtime supervision.
- `agents/agent-darkfactory` is the GitHub control-plane adapter. It maps issues,
  PRs, labels, gates, and releases into harness work. It must remain thin and
  must not become a second orchestration brain.
- `os/inference-engine` is the execution substrate for agent loops and runtime
  services.
- `os/llm-gateway` is the OpenAI-format model gateway and routing service.
- `os/agents-core` provides shared contracts, schemas, generated clients, and
  cross-package protocol docs.
- `plugins/*`, `skills/*`, and installed manager packages extend the distro
  through explicit package manifests and mounted shared state.

The north-star proof is a single `agents-os` container booting agents-manager,
agents-harness, inference-engine, llm-gateway, DarkFactory loops, and shared
state, then executing a GitHub issue through worker, PR, gates, merge, and
release.

## Distro Composition

The image is composed in ordered layers. Later layers may depend on earlier
layers but must not rewrite their ownership contracts.

| Layer | Contents | Contract |
| --- | --- | --- |
| OS base | Minimal Debian or Ubuntu LTS userspace, CA certificates, git, OpenSSH client, curl, unzip, bash, tini or equivalent init, and non-root runtime user. | Provides a stable Linux userspace and process supervisor for local Docker. |
| Runtimes | Bun, Node.js, Python 3 with uv, Go, and common build tools needed by managed packages. | Versions are pinned by the image build and surfaced in `agents os doctor`. |
| Agents CLI | Root agents-mono checkout or packaged `os/agents-manager` CLI exposed as `agents` in PATH. | The same CLI contract must work on the host and inside the container. |
| Shared packages | Materialized package manifests for `os/agents-core`, `os/agents-harness`, `os/inference-engine`, `os/llm-gateway`, agents, apps, templates, plugins, data repos, and workspaces. | Packages are discovered through agents-manager package registration, not hard-coded one-off scripts. |
| Harness services | agents-harness runtime and service entrypoints. | Starts and supervises orchestration services with mounted `AGENTS_*` state. |
| Inference services | inference-engine Python and Go components needed for local execution. | Runs through #11 profiles with declared ports, health checks, and data mounts. |
| Gateway services | llm-gateway service, registry routing, quota, and provider configuration. | Routes model requests through mounted secrets and shared credit store. |
| DarkFactory adapter | DarkFactory GitHub loop runtime and enforcement entrypoints. | Reads DarkFactory operational data from `darkfactory-data` and dispatches workers into the container through harness APIs. |
| Plugins and skills | Rommie, Dream, and future installed plugins or skills. | Loaded from shared manager state or package manifests; no secrets are baked into the image. |

### Package And Environment Model

agents-manager already scaffolds package registration through
`agent.package.json`, `agents.package.json`, or `agent.json` manifests and
supports package kinds including `agent`, `app`, `data`, `package`,
`workspace`, `harness`, `cli`, `skill`, `plugin`, `hook`, and `template`.

#9 and agents-manager#10 must extend this model rather than bypass it:

- Distro packages are first-class manager packages that can declare image layer
  inputs, runtime dependencies, provided services, data requirements, and health
  checks.
- Container images are first-class packages with image name, channel, digest,
  build provenance, and compatible manager version.
- Environments are named package sets with scoped config and secrets. MVP
  environments are `host`, `agents-os`, `global-workspace`,
  `workspace-darkfactory`, and future per-agent workspaces.
- Each environment resolves to a deterministic mount set, env set, package set,
  and lifecycle plan.

The distro image must be reproducible from the root package graph plus explicit
build metadata. Any package that needs data or workspace access declares it in
its manifest and receives it through the Data Contracts document.

## Image Naming And Channels

The image name contract for #9 is:

- Local image: `agents-os:<version>` and `agents-os:dev`.
- GitHub Container Registry image:
  `ghcr.io/marius-patrik/agents-os:<version>`.
- Moving release channel tags: `dev`, `latest`, and optional prerelease tags
  such as `edge`.
- Immutable builds must also publish or record an image digest.

The image version should follow the agents-mono release that produced it.
Manager metadata must record:

- image reference
- digest when available
- channel
- build commit
- built-at timestamp
- supported manager version range

## Build Pipeline Shape For #9

#9 must add a release pipeline with this shape:

1. Build the image from agents-mono root on release or explicit workflow
   dispatch.
2. Initialize only the submodules required to assemble the distro.
3. Install pinned runtime dependencies.
4. Register package manifests for the OS package set.
5. Run `agents doctor` inside the image against mounted or test state.
6. Run profile smoke checks for services that do not require live secrets.
7. Publish versioned and channel tags to the selected registry.
8. Attach build metadata and validation output to the release.

Local owner-machine builds must also work without publishing:

```sh
agents os image build --channel dev
agents os create --name agents-os-dev --image agents-os:dev
agents os start agents-os-dev
agents os exec agents-os-dev -- agents doctor
```

Secrets must never be committed or baked into the image. Local builds may mount
host state and materialize credentials only through explicit agents-manager
commands.

## Runtime Topology

The container starts a small supervisor that can run these services:

| Service | Owner package | Required by | Health contract |
| --- | --- | --- | --- |
| manager-api | `os/agents-manager` | TUI, lifecycle commands, package/environment state | `agents doctor` plus local API or socket readiness |
| harness | `os/agents-harness` | orchestration, workers, scheduling, streams | harness health endpoint or CLI doctor |
| inference-engine | `os/inference-engine` | execution substrate | profile health command and declared port readiness |
| llm-gateway | `os/llm-gateway` | model routing, provider fallback, quota | HTTP health endpoint plus shared credit-store visibility |
| darkfactory-adapter | `agents/agent-darkfactory` | GitHub issue/PR control plane | loop status and data repo access |
| plugin services | `plugins/*` | optional capabilities | package-declared health check |

The MVP may run these under one container supervisor. The contract must not
prevent later splitting into multiple containers, but the #14 capstone requires
one container to run the full system.

## Launcher Contract

`agents` with no arguments and `agents os` are the interactive launcher entry
points for agents-manager#8. The CLI must:

1. Resolve or create the selected environment, defaulting to `agents-os`.
2. Check Docker availability and report actionable errors if it is missing or
   stopped.
3. Resolve the image from manager image metadata, local Docker cache, or the
   configured registry.
4. Create the container if missing, using the mount and env contracts in
   `DATA-CONTRACTS.md`.
5. Start or attach to the existing container.
6. Wait for a health check before opening the TUI.
7. Open a full TUI that includes system state, lifecycle actions, logs, and an
   embedded terminal attached to the running OS container.

### Docker Interface

The MVP Docker invocation must be reproducible as a dry-run plan. It must
include:

- Container name: `agents-os-<environment>` unless explicitly overridden.
- Labels:
  - `io.agents.os.managed=true`
  - `io.agents.os.environment=<environment>`
  - `io.agents.os.image-channel=<channel>`
  - `io.agents.os.root=<host-root>`
- Volumes from the Data Contracts document.
- Environment variables from the Data Contracts document.
- Published ports required by enabled profiles.
- Optional Docker network `agents-os` for future multi-container expansion.
- Restart policy defaulting to no automatic restart for MVP local development.

Lifecycle commands in #10 must map to Docker and manager state:

| Command | Contract |
| --- | --- |
| `agents os doctor` | Checks Docker, image metadata, host paths, shared state, env export, and profile prerequisites. |
| `agents os image list` | Lists configured local and registry images with channels and digests. |
| `agents os image build` | Builds the image locally according to #9 metadata. |
| `agents os image pull` | Pulls a configured image and records digest. |
| `agents os create` | Creates a named container from an environment plan without starting it unless requested. |
| `agents os start` | Starts the container and waits for health. |
| `agents os stop` | Stops the container without deleting mounted data. |
| `agents os status` | Prints container, service, health, image, and mount status. |
| `agents os logs` | Streams supervisor or selected service logs. |
| `agents os exec` | Runs a command inside the container. |
| `agents os terminal` | Opens an interactive shell inside the container. |
| `agents os remove` | Removes the managed container, never mounted data, unless an explicit prune flag is supplied. |
| `agents os deploy <profile>` | Resolves, records, and starts a package or service profile such as `inference-engine` or `llm-gateway`. |

### TUI Interface

The TUI is the interactive face of the same CLI, not a separate management
system. It must be able to call the same dry-run plan and lifecycle operations.

MVP panes:

- System status: container, image, health, enabled profiles, and active
  environment.
- Package graph: registered packages, data repos, workspaces, plugins, and
  services.
- Runs and streams: harness/DarkFactory queues, worker status, and recent
  actions.
- Logs: supervisor and service logs.
- Embedded OS terminal: an interactive shell or selected command inside the
  container.

Style presets required by agents-manager#8:

- `agents-os` default identity
- `claude-code`
- `codex`
- `kimi`

Style presets affect color, chrome, layout idioms, and prompt feel only. They
must not change command behavior, data locations, or lifecycle semantics.

## Runtime Profiles For #11

#11 must implement profiles using this minimum schema:

```json
{
  "schemaVersion": 1,
  "id": "llm-gateway",
  "package": "os/llm-gateway",
  "environment": "agents-os",
  "command": ["agents", "packages", "run", "llm-gateway", "--", "serve"],
  "ports": [{ "name": "http", "container": 8787, "host": 8787 }],
  "health": { "type": "http", "url": "http://127.0.0.1:8787/health" },
  "requires": {
    "env": ["AGENTS_HOME", "AGENTS_DATA", "AGENTS_WORKSPACE", "AGENTS_CREDITS"],
    "dataRepos": ["agentos-data"],
    "secrets": ["openai", "github"]
  }
}
```

Required MVP profiles:

- `harness`
- `inference-engine`
- `llm-gateway`
- `darkfactory`
- `full-system`

Each profile must support a dry-run plan that prints command, ports, health,
mounts, secrets scope, and data repos without starting services.

## Threat Model And Safety

The local MVP trusts the owner machine and local Docker daemon. It does not
trust container workloads with unrestricted host access.

Required safety boundaries:

- Mount only explicit state, data, and workspace paths.
- Mount secrets as files or environment only when an environment requires them.
- Never bake secrets into images.
- Keep mounted host paths readable in dry-run output.
- Do not remove mounted data in lifecycle cleanup by default.
- Prefer non-root container execution. Use root only for image build steps.
- Treat GitHub tokens, model-provider credentials, and CLI auth files as
  secret material.
- Container logs must not print secret values.

Windows host handling:

- Host paths may be Windows paths, but container paths are always POSIX.
- `agents os doctor` must validate Docker Desktop path sharing before create.
- Dry-run output must show both host and container paths.
- Path normalization must be tested by #10.

## Acceptance Criteria For Follow-On Issues

### #9 Base Image And Release Pipeline

#9 is complete when:

- A minimal image definition installs OS tools, Bun, Node.js, Python/uv, Go, and
  agents CLI runtime dependencies.
- The image runs `agents doctor` with mounted test or local shared state.
- The image declares and records channel, version, digest, build commit, and
  supported manager version range.
- Local build and smoke commands are documented and validated.
- Release automation publishes versioned and channel images or explicitly
  documents the private/local distribution path.
- No secrets are committed, printed, or baked into image layers.

### #10 CLI Lifecycle Commands

#10 is complete when:

- `agents os doctor` checks Docker, path sharing, image metadata, state files,
  data repos, and configured profiles.
- `agents os image list/build/pull` manages local and registry images.
- `agents os create/start/stop/status/logs/exec/terminal/remove` operate on
  named managed containers.
- `agents os deploy <profile>` records enough metadata for repeatability and
  cleanup.
- All lifecycle commands have dry-run output.
- Unit tests cover argument parsing, environment export, Windows path
  normalization, Docker plan generation, and cleanup semantics.

### #11 Runtime Profiles

#11 is complete when:

- `inference-engine`, `llm-gateway`, `harness`, `darkfactory`, and
  `full-system` profiles exist.
- Each profile declares ports, env, health check, data mounts, workspace mounts,
  and secrets scope.
- `agents os deploy inference-engine` and `agents os deploy llm-gateway`
  produce dry-run plans and can start local/container instances in the MVP
  environment.
- Smoke validation proves llm-gateway can read the shared credit store and
  inference-engine can see required `AGENTS_*` paths through mounts.
- Skipped live-service validation is documented with a manual checklist.

## Sequencing

1. #8 specifies architecture and data contracts.
2. #9 builds and publishes the base image from those contracts.
3. #10 adds lifecycle commands and the Docker/TUI launcher interface.
4. #11 wires service profiles and profile smoke checks.
5. #13 materializes the global `os/agents-workspace` and per-agent workspace
   pattern.
6. agents-manager#8 builds the full TUI launcher on top of #9 and #10.
7. agents-manager#10 extends packages and environments so distro packages,
   container images, and workspace environments are first-class manager objects.
8. #14 proves the full system inside one container.
