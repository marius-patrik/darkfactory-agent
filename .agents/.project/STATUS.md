# Status

- Andromeda v0.2.2 is released at `d7bafd4f660c275bb327b9dd97b371f26a48adc2` after PRs #169, #170, and #172 passed CI and automated review.
- The complete Windows gate passes: 208 manager tests, generated-code freshness, and 27 review-takeover tests, with zero failures.
- Windows and Mac both install exact `main@d7bafd4f660c` with four checksum-pinned providers. Explicit-root `agents state doctor --json` is green on both, with zero prepared imports.
- The final encrypted exchange is idempotent in both directions: 18 entries, 18 skips on replay, and shared projection hash `15af08e9fe575ff3ebce44f876831dbe24b4ab315f7bef0a580641e706a7b949`.
- Both machines replay 9 memory events to 9 records with memory projection hash `0d6123d9537c2268189f596f4fc36d7053190716cc2ca6c9ef6227803aac58b4`.
- `data/agent-os/context/TASK.md` is the canonical owner-facing task list. Completed work is folded into release history; the board contains one Backlog row followed by one final Parked row.
- Shared runtime identity, memory, sessions, orchestration, and providers live under `.agents`; that shared context references the Git-backed TASK.md board instead of maintaining a competing provider-local task list. Provider-local memories and transcripts are supporting evidence only.
