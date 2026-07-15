Format: JSON

Emit exactly one JSON object and no prose. Required keys:

- `schemaVersion`: integer `1`.
- `status`: `completed`, `reclassify`, or `blocked`.
- `target`: object with `repository`, `workItem`, and `path`.
- `transformation`: object with `expectedBefore`, `observedBefore`, `expectedAfter`, and `observedAfter`.
- `verification`: object with `check`, `result`, and `evidence`.
- `judgmentRequired`: boolean.
- `evidence` and `blockers`: arrays.

Unknown keys are forbidden. Any judgment or unexpected state requires `reclassify` without mutation.
