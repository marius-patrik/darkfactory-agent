# VS2 / S3.3 — single-worker agent loop: acceptance proof

**Date:** 2026-06-13 · **Slice:** VS2 (server stack) · **Task:** S3.3 (#1284) · the **working core**.

The VS2 acceptance bar: *"a single-node session runs a multi-tool task to a VALIDATED artifact
(no-false-green)."* Proven LIVE against the real gateway on s002 (qwen3-8b, tool-enabled via the `hermes`
vLLM parser), tools executing in-process behind the `exec_lane` `daemon-inline` seam, gated by the merged
no-false-green status-machine (S3.5a).

## The no-false-green invariant, proven all three ways (live)

| Scenario | Model behavior | Final status | Correct? |
|---|---|---|---|
| Real artifact + stop | `write_file([0,1,1,2,3,5,8,13,21,34])` → stop | **`useful_result`** | ✅ artifact validated by the gate |
| No artifact + stop | replies "DONE", no tool, stops | **`no_artifact`** | ✅ gate refuses success |
| No artifact + non-convergence | loops `bash` past max_turns | **`failed`** (non_progress) | ✅ never minted success |

`useful_result` is minted **only** by a passed acceptance-check over a real declared artifact — a command
exiting 0 (`bash` success) never mints it. Verified end-to-end on live local inference.

## Adversarial review (opus, 2 lenses) — found + fixed before merge
- **No-false-green: SOUND** — every `RunRecord.status` mutation traced; `useful_result` reachable only via
  `check_pass`; the scripted exit-0-no-artifact session yields `no_artifact`.
- **Blocker (fixed):** a non-materialized secret in a tool_call's JSON-string `arguments` defeated the
  assignment redactor and reached the **gateway** on replay (events boundary was already safe). Fixed by
  parsing+`redact_obj`-ing the tool_call arguments before replay.
- **Major (fixed):** `short.md` (carrying tool output) was injected as a `role=system` message on later
  turns (L6 trust-mixing / prompt-injection surface). Now carried as `role=user`.
- **Major (fixed):** the validator registry shipped empty → `code-change` silently downgraded to
  existence-only. `register_default_validators()` now runs at status-package import, so `code-change`
  requires build/test evidence (absent build_cmd → `missing_evidence`, not `useful_result`).
- Minors fixed: `LoopError`/unexpected → terminal `failed`; budget floored; file-tool unconfinement
  documented (bash already grants host access in VS2).

## Vacuous-test catch (no-false-green applied to the tests themselves)
The redaction regression test asserted inside a fake-gateway callback whose `AssertionError` was swallowed
by the loop's broad `except Exception → failed` handler (and the placeholder assertion was wrong:
`[REDACTED:` vs the real `‹REDACTED:`). The test passed *vacuously*. Fixed: the loop now **re-raises
`AssertionError`** (never swallows invariants/test failures), and the assertion was corrected — so the
redaction test now genuinely proves the secret is absent from the replayed tool_call arguments.

**Deferred (behind contracts):** Go daemon-over-NATS tool lane (VS3) · concurrent brain / streaming / WS TUI
(VS4) · cloud/OAuth gateway (S3.2). Follow-up #1285: strengthen system-prompt stop-guidance (small models
loop tools on open-ended tasks → max_turns).
