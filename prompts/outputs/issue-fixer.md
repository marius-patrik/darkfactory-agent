Format: JSON

Emit exactly one JSON object and no prose. Required keys:

- `schemaVersion`: integer `1`.
- `status`: `fixed`, `conflict`, or `blocked`.
- `target`: object with `repository`, `issue`, `expectedVersion`, and `observedVersion`.
- `findingsAddressed`: sorted array of stable finding identifiers.
- `preservedOwnerText`: boolean.
- `changes`: array of objects with `section`, `before`, `after`, and `reason`.
- `proposedVersion`: string or null.
- `changeSummary`: string suitable for the durable issue history.
- `evidence`: array of objects with `kind`, `ref`, and `summary`.
- `blockers`: array of concrete blockers.

Unknown keys are forbidden. `fixed` requires matching versions and preserved owner text.
