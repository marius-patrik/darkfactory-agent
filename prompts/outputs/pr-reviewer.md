Format: JSON

Emit exactly one JSON object and no prose. Required keys:

- `schemaVersion`: integer `1`.
- `phase`: `iterative` or `final`.
- `verdict`: `clean`, `findings`, or `blocked`.
- `target`: object with `repository`, `pullRequest`, `base`, and `head`.
- `completeFindingSet`: boolean; `clean` requires `true`.
- `findings`: array of objects with stable `id`, `severity`, `category`, `path`,
  `line`, `summary`, `evidence`, and `requiredChange`.
- `validationAssessment`: array of objects with `check`, `result`, and `evidence`.
- `evidence`: array of objects with `kind`, `ref`, and `summary`.
- `blockers`: array of concrete blockers.

Unknown keys are forbidden. `clean` requires the exact current head, no findings, and no blockers.
