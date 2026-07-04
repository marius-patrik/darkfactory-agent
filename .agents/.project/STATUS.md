# Status

## Current State

| Area | State |
| --- | --- |
| Repository | `marius-patrik/template-repo` |
| Branch | `main` |
| Pull request | No active pull request |
| Scaffold | Bun and TypeScript project template |
| Agent files | `.agents/.global/` reusable, `.agents/.project/` project-specific |
| CI | GitHub Actions `validate` job |

## Validation

Latest local validation for the scaffold:

- `bun install`
- `bun run typecheck`
- `bun test`
- `bun run build`
- `bun run ci`

## Notes

- PR #2 (`[codex] Bootstrap Bun project and CI`) has been merged into `main`.
- There is no active handoff. When creating a new repository from this template, replace this file with that repository's current state.
