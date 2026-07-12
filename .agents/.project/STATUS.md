# Status

- Andromeda v0.2.0 is released; the post-release convergence fixes landed through
  PR #131 and were synchronized to `main` by PR #132.
- Windows and Mac use the same umbrella layout (`~/marius-patrik` plus
  `~/Projects` symlink), the same Andromeda source commit, and canonical Agent OS
  v2 roots under `~/.agents`.
- Mac doctor is fully green with four pinned providers. Windows doctor is green
  except for the app-owned Claude/Codex split recorded in #129; Codex and Claude
  are checksum-pinned and usable there.
- Canonical session `0mrhj5217-fa0cd9fd9845` records the Fable-to-Codex quota
  takeover. Both machines replay identical memory, session, and orchestrator
  immutable events.
- The owner-facing board is Andromeda-data `context/TASK.md`; provider-local
  memory is cache/evidence only.
- Open product work is tracked by #129 (Windows gaps), #130 (event transport),
  #97 (gateway hardening), and #99 (inferctl discovery). Parked scopes remain
  untouched until explicitly reopened.
