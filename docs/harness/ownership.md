# Runtime Harness Ownership

| Area | Owner |
| --- | --- |
| Session event schema, locking, replay, and projections | `src/migrate/harness/session.ts` |
| Tool loop and event-backed provider/model changes | `src/migrate/harness/tools.ts` |
| Managed provider invocation and startup-memory injection | `src/migrate/manager/src/session-adapters.ts` |
| State roots, provider registry, memory, CLI, packages, and orchestration policy | `src/migrate/manager/src` |
| Shared wire contracts and generated clients | `src/migrate/core` |
| Model routing and gateway OAuth refresh | `src/migrate/gateway` |
| Agent loop and inference execution | `src/migrate/inference` |

The harness directory has no independent binary, manifest, release version,
state root, credentials, switcher store, or orchestration ledger. External
harness packages remain ordinary Agent OS package registrations.
