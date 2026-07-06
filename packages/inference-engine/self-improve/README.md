# Self-Improvement Ratchet

This package is the generic Wave 4 ratchet for skills/plugins/commands.

It is separate from the QFT ratchet:

- QFT ratchet protects science (`.user/projects/qft/bench/ratchet.py`).
- Self-improvement ratchet protects extension quality and the human ADR gate.

## Evaluate

```bash
PYTHONPATH=. python -m packages.self_improve.cli evaluate .user/skills/pass
```

The evaluator reads the frozen held-out bench at
`.data/self_improve/heldout_bench.json` and emits a JSON report.

## Decide

```bash
PYTHONPATH=. python -m packages.self_improve.cli decide candidate.json \
  --baseline-report baseline.json \
  --changed-path .user/skills/pass/SKILL.md
```

Only these paths are eligible for automatic ratchet acceptance:

- `.user/skills/**`
- `.user/plugins/**`
- `.user/commands/**`

These surfaces look editable but are protected infra / vendored content and are
treated like core/infra (require human ADR, never auto-merged):

- `.user/skills/.system/**`
- `.user/plugins/cache/**`
- `.user/plugins/marketplaces/**`

Everything else is treated as core/infra and requires human ADR review
(ADR-031: skills/plugins auto-ratchet; platform-core/infra additionally requires
a human-approved ADR PR).
