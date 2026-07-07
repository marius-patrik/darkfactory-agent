# AGENTS.md - Agents Harness Operating Pointer

This repo owns the thin Agents Harness package for the Rommie runtime:
the local CLI compatibility surface, runtime bridge points, and
harness-owned adapter contracts.

## Load Current Context

- Read `README.md`, `PRD.md`, and `docs/ownership.md` before non-trivial work.
- Read issue/PR state from GitHub; `dev` is the integration branch.
- Treat sibling packages as the owner for their domains. Do not copy their
  implementation docs back into this repo.

## Ownership Boundary

- `agents-harness`: harness CLI, runtime integration points, plugin policy, and
  adapter-facing orchestration contracts.
- `agents-mono/os/agents-core`: shared contracts, schemas, generated clients.
- `agents-mono/os/agents-manager`: local setup, node identity materialization,
  provider CLI rooting, credential materialization, and audit gates.
- `agents-mono/os/llm-gateway`: model gateway and cloud/provider dispatch docs.
- `agents-mono/os/inference-engine`: agent loop, engine, cluster, and deploy docs.
- `marius-patrik/plugin-rommie`: installable Rommie memory behavior.
- `marius-patrik/dream`: retrospective temporal replay.
- `data/data-agentos`: historical Andromeda/Rommie research, provenance, and
  retired world-model material.

## Hard Rules

- No false-green: claim done only after the real boundary is validated.
- Secrets are never logged, printed, committed, or persisted unredacted.
- One task, one branch, one PR into `dev`; do not commit directly to `dev` or
  `main`.
- Do not touch parked repos or sibling package state unless the issue explicitly
  targets that owner.
- Do not change repo settings.
- Preserve user changes and keep the checkout clean when finished.
