# Agents OS Data Contracts

This document defines where state, data, workspaces, secrets, and package
metadata live for `agents-os`. Host paths are owned by agents-manager. Container
paths are stable POSIX paths used by every package inside the distro.

## Principles

- The image is replaceable; mounted data is durable.
- agents-manager owns state discovery and environment export.
- System data, DarkFactory operational data, global work, and per-agent work are
  separate contracts.
- Workspaces are writable working sets. Data repos are durable operational data.
- Secrets are explicit environment-scoped mounts or materialized files, never
  image content.
- Every mount must appear in `agents os doctor` and lifecycle dry-run output.

## Container Path Contract

The MVP container uses these canonical paths:

| Env var | Container path | Purpose |
| --- | --- | --- |
| `AGENTS_ROOT` | `/opt/agents-os` | Read-only distro/package root inside the image. |
| `AGENTS_HOME` | `/agents/state` | Shared manager state root, equivalent to host `.agents`. |
| `AGENTS_DATA` | `/agents/data` | Parent for managed data repos. |
| `AGENTS_WORKSPACE` | `/workspace/agents` | Global system workspace from `os/agents-workspace`. |
| `AGENTOS_DATA_ROOT` | `/agents/data/agentos-data` | System data repo. |
| `DARKFACTORY_DATA_ROOT` | `/agents/data/darkfactory-data` | DarkFactory operational data repo. |
| `DARK_FACTORY_WORKSPACE_ROOT` | `/workspace/darkfactory` | DarkFactory bot working set. |
| `AGENTS_DATA_REPOS` | `/agents/state/data-repos.json` | Data repo registry. |
| `AGENTS_PACKAGES` | `/agents/state/packages.json` | Package registry. |
| `AGENTS_CREDITS` | `/agents/state/credits.json` | Shared credit ledger. |
| `AGENTS_SECRETS` | `/agents/state/secrets` | Explicitly materialized local secret files. |
| `AGENTS_PLUGINS` | `/agents/state/plugins` | Shared installed plugins. |
| `AGENTS_SKILLS` | `/agents/state/skills` | Shared installed skills. |
| `AGENTS_HOOKS` | `/agents/state/hooks` | Shared installed hooks. |
| `AGENTS_TEMPLATES` | `/agents/state/templates` | Shared installed templates. |
| `AGENTS_CLIS` | `/agents/state/clis` | CLI adapter metadata and rooted homes. |
| `AGENTS_HARNESSES` | `/agents/state/harnesses` | Harness runtime roots. |

The current host manager already exports most `AGENTS_*` paths through
`.agents/env`. #10 must extend this export with `AGENTS_DATA`,
`AGENTS_WORKSPACE`, `AGENTS_PACKAGES`, `DARKFACTORY_DATA_ROOT`, and any
profile-specific paths.

## Host Mount Contract

The owner-machine MVP maps host paths into the container as follows:

| Host path | Container path | Mode | Owner |
| --- | --- | --- | --- |
| `<root>/.agents` | `/agents/state` | read-write | agents-manager |
| `<root>/data/data-agentos` | `/agents/data/agentos-data` | read-write | agentos-data |
| `<root>/data/darkfactory-data` or registered equivalent | `/agents/data/darkfactory-data` | read-write | darkfactory-data |
| `<root>/os/agents-workspace` | `/workspace/agents` | read-write | global workspace |
| `<root>/workspaces/workspace-darkfactory` | `/workspace/darkfactory` | read-write | DarkFactory workspace |
| `<root>/agents/<agent>` | `/workspace/agents/<agent>/repo` | read-write when enabled | per-agent code checkout |
| `<root>/workspaces/<agent>-workspace` | `/workspace/agents/<agent>/workspace` | read-write when enabled | per-agent workspace |
| `<root>/data/<agent>-data` | `/agents/data/<agent>-data` | read-write when enabled | per-agent data repo |

The host root is the agents-mono checkout or a manager-selected environment
root. Docker must receive absolute host paths. Container processes must consume
only the container paths from environment variables.

## System Data: agentos-data

`agentos-data` is the system-level data repository. It is registered in
`.agents/data-repos.json` with:

```json
{
  "id": "agentos-data",
  "repo": "marius-patrik/data-agentos",
  "path": "data/data-agentos",
  "branch": "main",
  "env": "AGENTOS_DATA_ROOT"
}
```

Inside the container:

- `AGENTOS_DATA_ROOT=/agents/data/agentos-data`
- Read/write access is allowed for manager state sync, system metadata, release
  records, package indexes, and global operational facts.
- Secrets must not be stored in this repo unless they are encrypted and the
  encryption contract is documented separately.

## DarkFactory Data: darkfactory-data

`darkfactory-data` is the DarkFactory operational data repository. It stores
GitHub control-plane state, queues, run metadata, dashboards, dispatch state,
review gates, and release bookkeeping.

Inside the container:

- `DARKFACTORY_DATA_ROOT=/agents/data/darkfactory-data`
- DarkFactory reads and writes this path.
- Harness services may read it when dispatching or reporting work.
- Other packages may depend on it only through documented APIs or registered
  package contracts.

If the physical host path is still being migrated, agents-manager must register
the selected repo path in `.agents/data-repos.json` and expose the container
path through `DARKFACTORY_DATA_ROOT`. Consumers must not assume a host path.

## Workspace Topology

Workspaces are writable checkouts or working sets, distinct from durable data.

### Global Workspace

#13 defines a global system workspace:

- Repository: `workspace-agents`
- Mount point in agents-mono: `os/agents-workspace`
- Container path: `/workspace/agents`
- Env: `AGENTS_WORKSPACE=/workspace/agents`

This workspace is for global system-level work coordinated by the mono
orchestrator. It pairs with `agentos-data` as its durable data companion.

### DarkFactory Workspace

`workspace-darkfactory` is the bot working set:

- Host path: `workspaces/workspace-darkfactory`
- Container path: `/workspace/darkfactory`
- Env: `DARK_FACTORY_WORKSPACE_ROOT=/workspace/darkfactory`
- Data companion: `darkfactory-data`

DarkFactory workers operate here when the task is DarkFactory-owned. The
workspace may contain managed repo checkouts, branch state, generated reports,
and local working files. Operational truth belongs in `darkfactory-data`.

### Per-Agent Pattern

Every future agent follows the same pair pattern:

- Agent code repo: `agents/<agent-name>`
- Agent data repo: `data/<agent-name>-data`
- Agent workspace repo: `workspaces/<agent-name>-workspace`
- Container data path: `/agents/data/<agent-name>-data`
- Container workspace path: `/workspace/agents/<agent-name>/workspace`
- Optional code path: `/workspace/agents/<agent-name>/repo`

Recommended env names:

- `<AGENT_ID>_DATA_ROOT`
- `<AGENT_ID>_WORKSPACE_ROOT`
- `<AGENT_ID>_REPO_ROOT`

For example, a future `rommie` agent would expose
`ROMMIE_DATA_ROOT`, `ROMMIE_WORKSPACE_ROOT`, and `ROMMIE_REPO_ROOT`.

## Package Manifest Data Contract

Package manifests may declare data requirements with the existing `dataRepo`
field:

```json
{
  "schemaVersion": 1,
  "id": "workspace-darkfactory",
  "kind": "workspace",
  "dataRepo": {
    "id": "darkfactory-data",
    "repo": "marius-patrik/darkfactory-data",
    "path": "data/darkfactory-data",
    "branch": "main",
    "managedPath": "managed-repository",
    "env": "DARKFACTORY_DATA_ROOT"
  }
}
```

#10 and agents-manager#10 must preserve backward compatibility with the current
field while adding environment/package metadata for:

- required data repos
- required workspaces
- required secrets
- profile ports
- health checks
- image layer inputs
- container package dependencies

Package registration writes `.agents/packages.json`. Data repo registration
writes `.agents/data-repos.json`. Container plans must derive mounts and env from
those registries rather than duplicating hard-coded paths.

## Environment Contract

An agents-manager environment resolves these objects:

- package set
- data repo set
- workspace set
- secret scope
- image reference and channel
- Docker resource plan
- service profile set

MVP environments:

| Environment | Purpose |
| --- | --- |
| `host` | Native host CLI state and package management. |
| `agents-os` | Default full-system container environment. |
| `global-workspace` | System-level mono orchestrator work. |
| `workspace-darkfactory` | DarkFactory work execution. |
| `<agent-name>` | Future per-agent execution and workspace scope. |

The environment export inside the container must be deterministic. The minimum
required variables are:

```sh
AGENTS_ROOT=/opt/agents-os
AGENTS_HOME=/agents/state
AGENTS_DATA=/agents/data
AGENTS_WORKSPACE=/workspace/agents
AGENTOS_DATA_ROOT=/agents/data/agentos-data
DARKFACTORY_DATA_ROOT=/agents/data/darkfactory-data
DARK_FACTORY_WORKSPACE_ROOT=/workspace/darkfactory
AGENTS_DATA_REPOS=/agents/state/data-repos.json
AGENTS_PACKAGES=/agents/state/packages.json
AGENTS_CREDITS=/agents/state/credits.json
AGENTS_SECRETS=/agents/state/secrets
AGENTS_CLIS=/agents/state/clis
AGENTS_HARNESSES=/agents/state/harnesses
AGENTS_SKILLS=/agents/state/skills
AGENTS_PLUGINS=/agents/state/plugins
AGENTS_HOOKS=/agents/state/hooks
AGENTS_TEMPLATES=/agents/state/templates
```

## Secrets Contract

Secrets are environment-scoped and must be opt-in:

- Host source files remain in their existing CLI homes or manager secret store.
- `agents secrets` may materialize redacted or scoped files under
  `AGENTS_SECRETS`.
- Docker mounts the selected secret directory into `/agents/state/secrets`.
- Profile dry-runs list secret names and scopes, never values.
- Logs and doctor output must not print secret values.

Required MVP secret scopes:

- `github` for DarkFactory GitHub operations.
- `openai` and other model provider scopes for llm-gateway and
  inference-engine.
- CLI adapter scopes for Codex, Claude, Kimi, and Agy when those tools are
  enabled.

## Credits Contract

The shared credit store remains:

- Host: `<root>/.agents/credits.json`
- Container: `/agents/state/credits.json`
- Env: `AGENTS_CREDITS=/agents/state/credits.json`

llm-gateway must be able to read the credit store in #11 smoke validation.
Any service that writes credit usage must use manager-owned schema updates or a
documented API to avoid corrupting shared state.

## Health And Readiness Contract

Container readiness is not the same as service readiness.

Required readiness gates:

1. Docker container is running.
2. `AGENTS_HOME`, `AGENTS_DATA`, and `AGENTS_WORKSPACE` exist in the container.
3. `agents doctor` passes inside the container.
4. Enabled profile health checks pass.
5. The TUI can open an embedded terminal and execute `agents os status` or an
   equivalent status command.

Profile health checks must declare their type:

- `command`
- `http`
- `tcp`
- `file`

Health checks must have timeout and retry metadata.

## Cleanup Contract

Lifecycle cleanup must be conservative:

- `agents os stop` stops processes only.
- `agents os remove` removes the managed container only.
- `agents os image prune` may remove local images after confirmation or explicit
  force flag.
- Mounted data repos, workspaces, `.agents`, and secrets are never deleted by
  default.
- Any destructive cleanup requires a dry-run plan and explicit opt-in flag.

## Follow-On Acceptance

The data contract is implemented when #9, #10, #11, #13, and agents-manager#10
can prove:

- Host Windows paths are normalized into stable container POSIX paths.
- `AGENTS_HOME`, `AGENTS_DATA`, and `AGENTS_WORKSPACE` are present in every
  container profile.
- agentos-data and darkfactory-data mount to their specified paths.
- `workspace-darkfactory`, `os/agents-workspace`, and per-agent workspaces use
  the documented topology.
- `agents doctor` validates mounted shared state from inside the container.
- llm-gateway reads `AGENTS_CREDITS` through the mount.
- inference-engine sees its required workspace and data paths.
- No image layer contains secrets or mutable operational data.
