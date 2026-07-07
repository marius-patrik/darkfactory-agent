---
name: compact
description: Prepare a compaction-safe Rommie handoff that survives context compaction. Use when the user asks to compact, prepare for compaction, preserve current work state, make compaction actually useful, or before reminding the user to compact after substantial work.
---

# Compact

Use compact to write the active work state into memory files that Rommie startup and wake hooks load after context loss. This does not force the host model to compact on command; it makes compaction effective by placing the right state in `handoff.md`, `SHORT.md`, and `.compact-state.json` before the user or host triggers compaction.

## Workflow

1. Capture the current objective, completed work, repo/path state, validation, blockers, and exact next actions.
2. Prefer facts that a fresh agent needs to resume, not a transcript summary.
3. Run the capsule script with explicit values:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\skills\compact\scripts\write_compaction_capsule.ps1 -Objective "current goal" -State "what is done now" -Next "next command or decision" -Validation "checks run and results" -Blockers "known blockers or None"
```

4. If active work remains, keep a `SHORT.md` entry. If the work is truly complete, say so in the capsule and avoid inventing a follow-up objective.
5. After the capsule is written, tell the user compaction is ready and remind them to compact the thread.

## Capsule Quality Bar

A useful compaction capsule names:

- Current repo paths and important commits.
- Dirty files or clean-state evidence.
- Commands already run and their results.
- Failed validations that still matter.
- The exact next action if work resumes.

