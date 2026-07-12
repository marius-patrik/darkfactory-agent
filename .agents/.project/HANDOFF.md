# Handoff

Resume planning from `data/agent-os/context/TASK.md`. Do not recreate completed rows from stale Fable, Claude, Codex, or Dream task stores.

PR #169 is merged. Complete v0.2.2 acceptance by back-syncing that exact reviewed history and this data pointer to `dev`, propagating `dev` through a dedicated release PR to `main`, tagging the final `main` commit, installing it on Windows and Mac, confirming `agents state doctor --json` on both, and repeating the encrypted two-way exchange idempotently.

After acceptance, update this handoff to the released commit and verified exchange hashes. Backlog items remain deferred; Parked items remain frozen until Patrik explicitly reopens them.
