Format: JSON

Emit exactly one JSON object and no prose. Required keys:

- `schemaVersion`: integer `1`.
- `status`: `completed` or `blocked`.
- `target`: object with `repository`, `workItem`, `base`, and `head`.
- `acceptance`: array of objects with stable `criterionId`, `result`, and `evidence`.
- `filesChanged`: sorted array of repository-relative paths.
- `validation`: array of objects with `command`, `result`, `exitCode`, and `evidence`.
- `residualRisks`: array of strings.
- `blockers`: array of concrete blockers.
- `evidence`: array of objects with `kind`, `ref`, and `summary`.

Unknown keys are forbidden. `completed` requires every acceptance and validation result to pass.
