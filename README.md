# agents-mono

agents-mono is the root aggregator workspace for the agents-mono topology. It
exposes the `agents` CLI for managing agent packages, apps, templates, private
workspace state, shared skills/plugins, CLI data, and a common credit store.

## Usage

```sh
bun install
bun link
agents list
agents state init
agents state env
agents cli doctor
agents packages register os/agents-harness
agents harness doctor agents-harness
agents data repo list
agents doctor
```

## Layout

- `os/agents-core` is a submodule containing shared proto contracts, generated clients, schemas, and contract docs.
- `os/agents-manager` is a submodule containing the `agents` CLI source and tests.
- `os/agents-harness` is a submodule containing the managed Agents runtime harness.
- `os/llm-gateway` is a submodule containing the OpenAI-format LLM gateway, model registry routing, fallback, switchers, quota, OAuth seams, and tests.
- `os/inference-engine` is a submodule containing the Python agent loop, Go runtime services, engine work, deploy assets, and inference architecture.
- `plugins/plugin-rommie` is the Rommie Codex plugin submodule.
- `plugins/dream` is the Dream plugin submodule.
- `agents/darkfactory-agent`, `agents/life-support`, and `agents/skyblock-agent` are managed agent submodules.
- `apps/singularity` contains the managed Singularity app.
- `apps/fabrica` contains the managed Fabrica app workspace.
- `templates/darkfactory-templates` contains the Bun templates monorepo and nested template submodules.
- `data/data-agentos` contains the private AgentOS managed data repository and migrated non-code material.
- `workspaces/darkfactory-workspace` contains the lightweight DarkFactory workspace package that points at `agentos-data`.

## Naming contract

This repository uses the following names consistently. Legacy names are retained
only where they identify an existing repo, env var, or historical concept.

- `agents-mono` — the root aggregator repository and workspace (this repo).
- `agents` — the unified management CLI implemented in `os/agents-manager`.
- `os/agents-*` — OS/platform packages (`agents-core`, `agents-manager`, `agents-harness`).
- `agentos-data` — retained compatibility name for the default git-backed data repository and its env var (`AGENTOS_DATA_ROOT`).
- `Agentos`, `Andromeda`, `Rommie`, and similar legacy names are intentionally scoped; new docs and metadata use the current names above.

## Commands

- `agents list [--json]` lists registered packages from `.gitmodules`.
- `agents info <name-or-path> [--json]` shows package metadata.
- `agents add <name> <git-url> [--kind agent|app|data|package|template|workspace|harness|cli|plugin] [--branch main]` adds a git-backed package.
- `agents remove <name-or-path>` removes a package submodule.
- `agents sync` syncs and initializes submodules.
- `agents state init` initializes shared runtime state.
- `agents cli list|doctor|env|exec|materialize-creds` manages Codex, Claude, Kimi, and Agy through one adapter layer.
- `agents packages register <path>` registers a local package manifest.
- `agents data repo list|set|path|env` manages git-backed data repositories.
- `agents harness list|doctor|run` manages runtime harnesses such as Agents Harness.
- `agents install <skill|plugin|hook|template|cli|harness> <name> <source-path-or-url>` installs shared capabilities.
- `agents credits` shows the shared credit store.
- `agents doctor` checks package registration and shared state.

## Shared State

All managed CLIs must use the root `.agents` directory as the single source of
runtime state:

- `.agents/clis/` stores CLI-specific data and adapter metadata.
- `.agents/harnesses/` stores harness runtime roots.
- `.agents/skills/` stores user-installed shared skills.
- `.agents/plugins/` stores user-installed shared plugins.
- `.agents/hooks/` stores user-installed shared hooks.
- `.agents/templates/` stores templates.
- `.agents/secrets/` stores local secret values managed by `agents secrets`.
- `.agents/credits.json` stores the shared credit ledger.
- `.agents/data-repos.json` stores managed data repository mappings.
- `.agents/packages.json` stores registered package manifests.
- `.agents/env` exports the paths every CLI should consume.

See [PRD.md](PRD.md).
