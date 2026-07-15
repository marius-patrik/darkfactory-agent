Format: JSON

Emit exactly one JSON object and no prose. Required keys:

- `schemaVersion`: integer `1`.
- `phase`: `iterative` or `final`.
- `verdict`: `clean`, `findings`, or `blocked`.
- `target`: object with `repository`, `issue`, and `observedVersion`.
- `completeFindingSet`: boolean; `clean` requires `true`.
- `findings`: array of objects with stable `id`, `severity`, `category`,
  `location`, `summary`, `evidence`, and `requiredChange`.
- `ownerQuestions`: array of exact unresolved decisions.
- `evidence`: array of objects with `kind`, `ref`, and `summary`.
- `blockers`: array of concrete blockers.

Unknown keys are forbidden. `clean` requires no findings, blockers, or owner questions.
