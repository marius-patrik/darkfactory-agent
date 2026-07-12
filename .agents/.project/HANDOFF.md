# Handoff

Resume planning from `data/agent-os/context/TASK.md`. Do not recreate completed rows from stale Fable, Claude, Codex, or Dream task stores.

PR #169 and the PR #170 back-sync are merged. PR #172 is the dedicated `dev`-to-`main` v0.2.2 release. After it merges, tag its merge commit, install that exact `main` commit on Windows and Mac, confirm `agents state doctor --json` on both, and repeat the encrypted two-way exchange idempotently.

After acceptance, update this handoff to the released commit and verified exchange hashes. Backlog items remain deferred; Parked items remain frozen until Patrik explicitly reopens them.
