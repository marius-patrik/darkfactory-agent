# Manager Service — GitHub Control Surface + Global Manager

The manager is the single global service that watches GitHub issues, classifies them by label taxonomy, decomposes PRDs into sub-task runs, and dispatches each run to the daemon's bounded queue. It maintains the run invariant:

> **each run == one branch + one draft PR + one log issue/comment**

## Architecture

```
GitHub Issues (PRD/ADR/log/suggestion)
       |
       v
+-------------+     OpenAI-format      +---------+
| Manager| --> POST /v1/chat/ --> | Gateway |
|  (polling)  |    completions         | (:4000) |
+-------------+                        +---------+
       |
       |  sub-task runs
       v
+-------------+     POST /v1/runs      +--------+
|  Daemon API | ---------------------> | Daemon |
|   (:8080)   |                       |(:8080) |
+-------------+                       +--------+
       |
       |  branch + draft PR + log issue
       v
    GitHub
```

## Design Choices

- **Go service in the engine module** (`engine/go/cmd/manager`) — reuses `pkg/contracts`, `internal/ops`, and `internal/store` patterns.
- **Polls GitHub issues** — long-lived ticker loop, not webhook-driven. GitHub is the control/record surface, never the scheduler (ADR-029/030).
- **Calls daemon HTTP API** — the manager does NOT run containers itself and does NOT fan out unbounded. It respects the daemon's concurrency cap by limiting sub-tasks per PRD (`max_concurrent_runs`, default 4).
- **`gh` CLI for GitHub operations** — simple, auth-aware, mockable in tests.
- **Idempotency via `ops.Broker`** — every side effect (branch create, run submit, PR create, log issue create) uses an `OperationEnvelope` + deterministic idempotency key stored in SQLite.

## Issue Taxonomy

| Label | Action |
|-------|--------|
| `PRD` | Decompose into sub-tasks via gateway LLM → create branch + draft PR + log issue + daemon run per sub-task |
| `ADR` | Create branch + draft PR for human approval (manager never self-merges core/infra) |
| `suggestion` | Post acknowledgment comment; promote to PRD/ADR manually if accepted |
| `log` | Skip (these are created by the manager itself) |

## How to Run

### Prerequisites
- `gh` CLI authenticated to the target repo (`marius-patrik/agents`)
- Daemon running on `:8080`
- Gateway running on `:4000`

### Build
```bash
cd engine/go
go build -o bin/manager ./cmd/manager
```

### Run
```bash
# With defaults (repo marius-patrik/agents, poll 30s)
./bin/manager

# With env overrides
AGENTS_MANAGER_REPO_OWNER=myuser \
AGENTS_MANAGER_DAEMON_URL=http://localhost:8080 \
AGENTS_MANAGER_GATEWAY_URL=http://localhost:4000 \
AGENTS_MANAGER_MAX_RUNS=4 \
  ./bin/manager

# With JSON config
./bin/manager  # reads manager.json if present
```

### Configuration

Environment variables (override defaults):

| Variable | Default | Description |
|----------|---------|-------------|
| `AGENTS_MANAGER_ADDR` | `:8081` | Manager HTTP listen address (health) |
| `AGENTS_MANAGER_REPO_OWNER` | `marius-patrik` | GitHub repo owner |
| `AGENTS_MANAGER_REPO_NAME` | `agents` | GitHub repo name |
| `AGENTS_MANAGER_POLL` | `30s` | Issue polling interval |
| `AGENTS_MANAGER_DAEMON_URL` | `http://localhost:8080` | Daemon HTTP API |
| `AGENTS_MANAGER_GATEWAY_URL` | `http://localhost:4000` | Gateway HTTP API |
| `AGENTS_MANAGER_DB` | `agents_manager.db` | SQLite path for ops + state |
| `AGENTS_HARNESS_IMAGE` | `agents/harness:latest` | Default container image for runs |
| `AGENTS_MANAGER_BASE_BRANCH` | `dev` | Branch to fork from |
| `AGENTS_MANAGER_MAX_RUNS` | `4` | Max sub-task runs per PRD per tick |

JSON config file (`manager.json`) uses the same keys in camelCase / snake_case:
```json
{
  "listen_addr": ":8081",
  "repo_owner": "marius-patrik",
  "repo_name": "agents",
  "poll_interval": "30s",
  "daemon_url": "http://localhost:8080",
  "gateway_url": "http://localhost:4000",
  "default_image": "agents/harness:latest",
  "base_branch": "dev",
  "max_concurrent_runs": 4
}
```

## How a PRD Flows to Runs

1. **User files a PRD issue** with label `PRD`.
2. Manager tick picks it up (first time only — idempotent).
3. **Decomposition**: calls gateway (`coding` role) with a structured system prompt; expects JSON array of sub-tasks.
4. **Per sub-task** (up to `max_concurrent_runs`):
   - Generate UUIDv7 run ID.
   - Create branch `run/<short-id>/<slug>` from `dev`.
   - Submit run to daemon (`POST /v1/runs`) with `BranchRef`, `IssueRef`, container image, and command.
   - Create draft PR referencing the original PRD issue.
   - Create log issue with label `log`.
   - Record metadata in manager state DB.
5. Daemon's bounded queue starts containers as slots free up.
6. When a run finishes, the log issue can be updated manually or by a future enhancement.

## How an ADR Flows

1. **User files an ADR issue** with label `ADR`.
2. Manager creates branch `adr/<number>-<slug>` and opens a **draft PR**.
3. The PR description contains the ADR content and a human-approval warning.
4. Manager never self-merges. A human must review and merge.

## Tests

```bash
cd engine/go
go test ./internal/manager/ -v
```

Tests cover:
- Issue classification (`PRD`/`ADR`/`log`/`suggestion`/`unknown`)
- PRD decomposition → branch + run + PR + log issue creation
- ADR branch + draft PR creation
- Suggestion acknowledgment comment
- Idempotency (re-processing the same issue does not duplicate side effects)
- Max-run truncation (large PRDs are capped)
- Sub-task JSON parsing with markdown fences
- Health endpoint

All tests use mock GitHub, mock gateway (httptest), and mock daemon (httptest) — no real network calls.

## Files Added

| File | Purpose |
|------|---------|
| `engine/go/cmd/manager/main.go` | Entry point |
| `engine/go/internal/manager/config.go` | Config loading (env + JSON) |
| `engine/go/internal/manager/github.go` | `gh` CLI wrapper for issues/branches/PRs |
| `engine/go/internal/manager/gateway.go` | OpenAI-format gateway client + decomposition prompt |
| `engine/go/internal/manager/daemon_client.go` | Daemon HTTP API client |
| `engine/go/internal/manager/state.go` | SQLite state store (processed issues + manager runs) |
| `engine/go/internal/manager/manager.go` | Core polling loop + classification + PRD/ADR/suggestion handlers |
| `engine/go/internal/manager/manager_test.go` | Unit tests |
| `engine/go/cmd/manager/README.md` | This document |

## Open Questions / Follow-ups

1. **Run completion callback**: when a daemon run finishes, the manager currently does not automatically update the log issue with the final status. A future enhancement could poll daemon run status or consume NATS `RunEvent`s.
2. **Gateway response robustness**: the decomposition prompt assumes well-formed JSON. In production, a rescue-parse / retry guardrail (like the harness's) could be added.
3. **PR URL capture**: the `ops.Broker` records results but does not return them; the manager stores "created" as the PR URL. Extending the broker to return results would let us store exact URLs.
4. **Webhook mode**: polling is simple but creates load. A future mode could use GitHub webhooks pushed to the manager HTTP server.
5. **Multi-repo**: currently hardcoded to one repo. Extending to watch multiple repos is a config change + loop iteration.

