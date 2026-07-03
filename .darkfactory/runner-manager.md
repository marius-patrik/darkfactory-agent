# Runner Manager Notes

DarkFactory runners are local Windows background processes managed by the CLI.

- Default root: `C:/Users/patrik/.darkfactory/runners`
- State file: `C:/Users/patrik/.darkfactory/runners/state.json`
- Shared download cache: `C:/Users/patrik/.darkfactory/runners/_cache`
- Runner name: `df-<repo>`
- Required label: `df-local`
- Work directory: `_work`

The manager uses `gh api` to mint short-lived registration and removal tokens. Tokens must never be logged or persisted. The state file records runner metadata and PIDs only.

Windows service mode is intentionally disabled for this pilot. `config.cmd --runasservice` requires elevation and fails in headless non-elevated sessions, so the CLI starts `run.cmd` as a detached process and records the PID for `start`, `stop`, and `status`.

Workflow routing is a follow-up rollout. The pilot acceptance criterion is that the per-repository runners appear online through the GitHub Actions runners API.
