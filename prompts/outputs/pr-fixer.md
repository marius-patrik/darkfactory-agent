Format: JSON

Emit exactly one JSON object and no prose. Required keys:

- `schemaVersion`: integer `1`.
- `status`: `fixed`, `stale`, or `blocked`.
- `target`: object with `repository`, `pullRequest`, `base`, `expectedHead`, and `observedHead`.
- `findingsAddressed`: sorted array of stable finding identifiers.
- `filesChanged`: sorted array of repository-relative paths.
- `resultHead`: string or null.
- `validation`: array of objects with `command`, `result`, `exitCode`, and `evidence`.
- `policyPreserved`: boolean.
- `evidence`: array of objects with `kind`, `ref`, and `summary`.
- `blockers`: array of concrete blockers.

Unknown keys are forbidden. `fixed` requires a normal follow-up revision and all validation passing.
