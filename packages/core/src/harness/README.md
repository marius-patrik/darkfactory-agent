# Agents Harness

Agents Harness is the thin runtime harness package for Rommie under Agentos. It
keeps the `rommie` compatibility CLI stable, exposes harness-owned runtime and
adapter contracts, and delegates non-harness behavior to the Agentos packages
that own it.

For scope and operating rules, read [PRD.md](PRD.md) and [AGENTS.md](AGENTS.md).
The detailed owner map lives in [docs/ownership.md](docs/ownership.md).

## Purpose

This repository owns:

- The `rommie` CLI compatibility surface used by Agentos-managed state.
- Runtime bridge points where Agentos invokes the harness.
- Harness-owned plugin policy and adapter-facing orchestration contracts.
- The `agents-harness` release artifact built from `services/cli`.

This repository does not own setup policy, identity schemas, credential
materialization, provider CLI rooting, audit gates, model gateway dispatch,
inference execution, memory behavior, or historical research archives.

## Package Boundaries

| Path | Purpose |
| --- | --- |
| `services/cli` | Go harness CLI launched by Agentos. |
| `docs/plugins.md` | Policy for runtime-owned plugin content and fixture ownership. |
| `docs/ownership.md` | Documentation and package-owner split. |
| `docs/adapters/` | Adapter-facing contracts for external control planes. |
| `agent.package.json` | Agentos harness manifest. |
| `PRD.md` | Product scope, non-goals, and validation acceptance. |
| `AGENTS.md` | Repository operating pointer for agents and workers. |

External ownership:

| Area | Owner |
| --- | --- |
| Shared contracts, schemas, generated clients | `agents-mono/os/agents-core` |
| Local setup, node identity materialization, provider CLI rooting, credentials, audit gates | `agents-mono/os/agents-manager` |
| Model gateway and cloud/provider dispatch | `agents-mono/os/llm-gateway` |
| Agent loop, engine, cluster, and deploy docs | `agents-mono/os/inference-engine` |
| Installable Rommie memory behavior | `marius-patrik/plugin-rommie` |
| Retrospective temporal replay | `marius-patrik/dream` |
| Historical Rommie research, retired plans, and provenance | `data/data-agentos` |

Do not copy implementation plans or package docs from those owners into this
repository. Keep this README focused on operating the harness package.

## Install And Runtime Path

Agents Harness is installed and invoked through Agentos, not as an independent
application installer. Agentos reads [agent.package.json](agent.package.json):

- `workingDirectory`: `services/cli`
- `entry`: `go run ./cmd/rommie`
- `provides`: `agent-runtime`, `cli-adapter-host`, `capability-executor`
- `requires`: provider CLIs plus Agentos-managed state for skills, plugins,
  hooks, and credits

The managed runtime path is:

1. `agents-manager` materializes `AGENTS_HOME`, node identity, provider CLI
   roots, credentials, and audit policy.
2. Agentos launches this harness using the manifest entrypoint.
3. The harness reads manager-owned state and delegates provider CLI, setup, and
   audit operations back through `AGENTS_BIN` and `AGENTS_HOME`.

For local development, the CLI can be run directly:

```sh
cd services/cli
go run ./cmd/rommie --version
go run ./cmd/rommie setup --rommie-home /tmp/rommie
```

For manager-backed runtime checks, run through Agentos or provide the
manager-owned environment:

```sh
export AGENTS_BIN=/path/to/agents
export AGENTS_HOME=/path/to/agentos-home

cd services/cli
go run ./cmd/rommie setup --materialize-creds
go run ./cmd/rommie audit source
go run ./cmd/rommie audit secrets
go run ./cmd/rommie cli codex -- --help
```

Secrets must stay in manager-owned credential materialization. Do not log,
commit, or persist unredacted secrets in this repository.

## Local Validation

Run the documented validation commands from the repository root:

```sh
bun run check
bun run test
```

Both scripts currently execute:

```sh
go test ./services/cli/...
```

Use direct Go commands only when debugging the CLI package locally:

```sh
go test ./services/cli/...
cd services/cli && go run ./cmd/rommie --version
```

Do not claim a change is complete until the real boundary for the change has
been validated.

## Release And Version Status

The current package version is `4.0.0-alpha.3`, sourced from both
[VERSION](VERSION) and `package.json`.

Release rules:

- The `VERSION` file is the release source of truth.
- Release tags must match `v<VERSION>` exactly.
- The release workflow builds the CLI artifact from `services/cli`.
- The uploaded artifact name is `agents-harness`.
- Alpha, beta, and rc tags are marked as pre-releases.

Current release line:

- Latest shipped pre-release: `v4.0.0-alpha.2` (`9e262d3`).
- Next planned pre-release: `v4.0.0-alpha.3`.
- The `dev` branch is the integration branch for repository work.

## Branch And Enforcement Expectations

- Work one issue per branch and target PRs at `dev`.
- Do not commit directly to `dev` or `main`.
- Do not change repository settings from this package.
- Preserve user changes and keep the checkout clean when finished.
- Keep audit, credential, provider, gateway, inference, plugin, dream, and data
  behavior in their owning packages.

## Migrated Material

Material that used to be colocated with this package now lives with its owner:

- Shared contracts and generated clients moved to `agents-mono/os/agents-core`.
- Manager setup, node identity, CLI rooting, credentials, and audit gates moved
  to `agents-mono/os/agents-manager`.
- Gateway/provider dispatch documentation and proofs moved to
  `agents-mono/os/llm-gateway`.
- Agent loop, engine, cluster, and deploy documentation moved to
  `agents-mono/os/inference-engine`.
- Workspace, wiki, and research material moved out of this package.
- Historical Rommie plans, research, retired world-model material, and
  provenance moved to `data/data-agentos`.

Historical artifact names may appear only when pointing to retired material or
release migration history. New harness documentation should use current
Agentos, Rommie, and Agents Harness names.
