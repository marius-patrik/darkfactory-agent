---
name: sleep
description: Run a long-term memory hygiene pass for the Rommie memory system. Use when the user explicitly asks Codex to sleep, run sleep mode, clean stale memory, prevent context rot, consolidate memory, manage long-term memory, refresh memory_summary.md, triage SHORT.md/cache.md/handoff.md, park stale context, archive durable lessons, or make the agent stay sane across sessions.
---

# Sleep

Run sleep as an explicit maintenance pass over the Rommie memory store. The goal is to reduce context rot while preserving useful long-term signal.

## Workflow

1. Inspect the memory root at `C:\Users\patrik\.codex\memories` unless the user names another root.
2. Read these files in order: `cache.md`, `SHORT.md`, `handoff.md`, `MEMORY.md`, `PARK.md`, `ARCHIVE.md`, `LONG.md`, `memory_summary.md`, and ad-hoc notes under `extensions/ad_hoc/notes`.
3. Classify content:
   - Keep active work in `SHORT.md` only when it still has a clear current objective or blocker.
   - Keep volatile current-turn facts in `cache.md` only while the task is actively in progress.
   - Move stale but possibly useful project context to `PARK.md`.
   - Distill repeated, stable lessons into `ARCHIVE.md` or `LONG.md` when they are broadly applicable.
   - Keep searchable task facts and preferences in `MEMORY.md`.
   - Refresh `memory_summary.md` so startup context stays compact and points to the right detailed entries.
4. Preserve evidence pointers such as rollout summary paths, thread ids, issue ids, repo paths, and exact commands when they are useful for future recovery.
5. Remove or park duplication instead of letting the same lesson live in many layers.
6. Do not store secrets, tokens, private keys, credentials, or copied private message bodies.
7. Write a timestamp marker to `C:\Users\patrik\.codex\memories\.sleep-state.json` after a successful pass:

```json
{
  "last_sleep": "YYYY-MM-DDTHH:mm:ssK",
  "summary": "Short note about what was compacted"
}
```

## Staleness Rules

Treat memory as stale enough to sleep when any of these are true:

- `.sleep-state.json` is missing.
- Last sleep was more than 14 days ago.
- `cache.md` or `SHORT.md` has active content older than 48 hours.
- New ad-hoc notes appeared after the last sleep marker.
- `memory_summary.md` no longer reflects the main active registry topics.

## Editing Rules

Use deterministic edits and keep each layer compact. Prefer preserving unclear but possibly useful material in `PARK.md` over deleting it. Ask before deleting large amounts of user-authored memory.

