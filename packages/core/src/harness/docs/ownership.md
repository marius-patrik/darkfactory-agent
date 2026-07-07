# Documentation Ownership

Agents Harness keeps only harness-owned contracts and runtime integration
documentation. Broader Andromeda/Rommie material lives with the package that
owns the behavior.

## Harness-Owned

- CLI compatibility and delegation behavior in `services/cli`.
- Runtime plugin policy in `docs/plugins.md`.
- Adapter-facing orchestration contracts in `docs/adapters/`.
- Package manifest behavior in `agent.package.json`.

## External Owners

| Area | Owner |
| --- | --- |
| Shared schemas, protobuf, generated clients | `agents-mono/os/agents-core` |
| Setup, node identity, CLI homes, credentials, audit gates | `agents-mono/os/agents-manager` |
| Gateway, providers, cloud dispatch, gateway proofs | `agents-mono/os/llm-gateway` |
| Agent loop, inference engine, cluster, deploy docs | `agents-mono/os/inference-engine` |
| Installable Rommie memory plugin and hooks | `marius-patrik/plugin-rommie` |
| Retrospective temporal replay | `marius-patrik/dream` |
| Historical research, retired plans, provenance | `data/data-agentos` |
| One-system root PRD and cross-package binding | `agents-mono` root |

## Removed From This Repo

The old `.plans/` corpus, repo-local `.agents/` Andromeda operating bundle, and
gateway proof files were removed from Agents Harness because they described the
full one-system platform rather than this harness package. Keep future copies in
the owner packages above instead of re-vendoring them here.
