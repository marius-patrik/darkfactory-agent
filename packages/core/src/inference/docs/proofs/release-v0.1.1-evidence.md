# v0.1.1 Release Evidence

Date checked: 2026-07-04

## Published release

- Tag: `v0.1.1`
- Release: https://github.com/marius-patrik/inference-engine/releases/tag/v0.1.1
- Target: `main`
- Assets: none; this repository currently publishes validation-only releases with generated notes.

## Workflow evidence

- Workflow: `DarkFactory Release`
- Run: https://github.com/marius-patrik/inference-engine/actions/runs/28693774123
- Event: tag push for `v0.1.1`
- Conclusion: success
- Relevant completed steps:
  - Check out repository
  - Check out `agents-core` contracts sibling
  - Set up Node.js, Bun, Go, Python, and uv
  - Validate release
  - Create GitHub release

## Prior failed evidence

The earlier `v0.1.0` release exists, but its release workflow run failed during
validation because uv was missing in the runner environment:
https://github.com/marius-patrik/inference-engine/actions/runs/28693356370

`v0.1.1` is the current successful release evidence for the managed release
workflow.
