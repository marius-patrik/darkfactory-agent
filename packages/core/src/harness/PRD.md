# Agents Harness PRD

## Product Summary

Agents Harness is the thin runtime harness package for Rommie under Agentos.
It keeps local compatibility entrypoints stable while delegating ownership of
setup, identity, credentials, audit policy, memory behavior, gateway dispatch,
and inference runtime to their owning packages.

## Goals

- Provide the `rommie` CLI compatibility surface used by Agentos-managed state.
- Consume manager-owned node identity and runtime-root materialization.
- Delegate provider CLI execution, credential materialization, and audit gates
  to `agents-mono/os/agents-manager`.
- Document harness-owned plugin policy and adapter-facing orchestration
  contracts.
- Avoid carrying historical one-system design material that belongs to root,
  gateway, inference, plugin, dream, or data packages.

## Non-Goals

- Agents Harness is not the model gateway.
- Agents Harness is not the inference engine or agent loop implementation.
- Agents Harness is not the installable Rommie memory plugin.
- Agents Harness is not the historical Andromeda/Rommie design archive.
- Agents Harness is not the GitHub control-plane adapter for DarkFactory.

## Owned Surface

- `services/cli`: harness CLI entrypoints and compatibility delegation.
- `docs/plugins.md`: plugin-content ownership policy.
- `docs/ownership.md`: package-owner map and documentation split.
- `docs/adapters/`: adapter-facing contracts for external control planes.

## External Owners

- `agents-mono/os/agents-core`: shared contracts and schemas.
- `agents-mono/os/agents-manager`: setup, identity, CLI rooting, credentials,
  audit gates, and install/test fixtures.
- `agents-mono/os/llm-gateway`: gateway/cloud dispatch docs and proofs.
- `agents-mono/os/inference-engine`: agent loop, engine, cluster, and deploy
  docs.
- `marius-patrik/plugin-rommie`: memory skills and hygiene hooks.
- `marius-patrik/dream`: temporal replay and retrospective reflection.
- `data/data-agentos`: historical research, old plans, and provenance.

## Acceptance

- Harness docs describe only harness-owned runtime integration points and
  adapter contracts.
- Non-harness behavior is represented by owner pointers, not copied
  implementation plans.
- `bun run check` and `bun run test` pass for harness changes.
