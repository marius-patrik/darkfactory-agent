---
name: breathe
description: Refresh Rommie memory and force a response reflection checkpoint before finalizing. Use when the user says breathe, reflect, rethink, keep memory up to date, do not stop while work remains, or when a long-running task needs a mid-turn sanity check before continuing or answering.
---

# Breathe

Use breathe as a deliberate pause before finalizing or when work has run long enough that state may drift. The goal is to keep memory current, re-check the newest user request, and continue instead of stopping while required work remains.

## Workflow

1. Re-read the newest user request and any active handoff/context. Latest user instruction wins over older plan state.
2. Inspect live state before deciding the work is done: relevant git status, running processes, test results, generated files, or external state named by the task.
3. Update memory when there is durable active state. Prefer `cache.md` for volatile current-turn facts, `SHORT.md` for active multi-session work, and `handoff.md` for exact resume state.
4. Run the reflection gate:
   - Are all explicit user requests handled?
   - Are touched repos either clean or intentionally dirty with a clear reason?
   - Did validation run, or is the missing validation clearly explained?
   - Is there unfinished work that can still be advanced without user input?
5. If work remains and can be advanced, continue working instead of sending a final answer.
6. If the work is truly done, run or suggest `$sleep` when memory is stale, then remind the user to compact when the thread has become large or the handoff is important.

## Marker Script

When a concrete memory checkpoint is useful, run:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\skills\breathe\scripts\mark_breathe.ps1 -Summary "what changed" -State "in-progress" -Next "next action"
```

The script updates `.breathe-state.json` and a `rommie:breathe` block in `handoff.md`, which startup memory hooks can surface after context loss.

