# Runtime Harness Ownership

| Area | Owner |
| --- | --- |
| Session event schema, locking, replay, and projections | `src/sdk/harness/session.ts` |
| Tool loop and event-backed provider/model changes | `src/sdk/harness/tools.ts` |
| Managed provider invocation and startup-memory injection | `src/cli/session-adapters.ts` |
| State roots, provider registry, memory, CLI, packages, and orchestration policy | `src/cli` |
| Shared wire contracts and generated clients | `src/sdk` |
| Model routing and gateway OAuth refresh | `src/server/gateway` |
| Agent loop and inference execution | `src/server/inference` |

The harness directory has no independent binary, manifest, release version,
state root, credentials, switcher store, or orchestration ledger. External
harness packages remain ordinary Agent OS package registrations.
