# Agents Manager Daemon (Go)

The Go manager daemon is the platform's **sole bounded scheduler**. It owns run admission, idempotent operations,
and lifecycle event emission.

Execution truth:
- **v3.0 executor:** execution goes through Kubernetes Jobs scheduled by the daemon.
- **CI role:** GitHub Actions remains orchestration/validation/record, not the executor.
- **Local development:** the Docker and GitHub Actions executors remain legacy/test options, but are not the v3 path.

## Build

```bash
cd engine-go
go build ./cmd/daemon
```

## Run

```bash
./daemon
```

Environment variables:
- `AGENTS_DAEMON_ADDR` — HTTP listen address (default `:8080`)
- `AGENTS_DAEMON_CAP` — max concurrent runs (default `4`)
- `AGENTS_DAEMON_DB` — SQLite path (default `agents_daemon.db`)
- `NATS_URL` — NATS URL; if unset, events fall back to no-op
- `AGENTS_DAEMON_EXECUTOR` — `kubernetes`/`k8s` for v3 execution; `docker` for local-only tests
- `AGENTS_K8S_NAMESPACE` — namespace for agent Jobs (default `agents`)
- `KUBECONFIG` / `AGENTS_KUBECONFIG` — optional kubeconfig path when not running in-cluster

Or provide a JSON config file at `daemon.json`:
```json
{
  "listen_addr": ":8080",
  "concurrency_cap": 4,
  "db_path": "agents_daemon.db",
  "nats_url": "nats://localhost:4222"
}
```

## Submit a local test run

```bash
curl -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{"image":"alpine:latest","command":["echo","hello"],"issue_ref":"#42"}'
```

That example exercises the configured executor. In v3 deployments, the daemon turns it into a Kubernetes Job.

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health + capacity |
| POST | `/v1/runs` | Submit a run |
| GET | `/v1/runs` | List runs |
| GET | `/v1/runs/{id}` | Get run status |
| GET | `/v1/runs/{id}/logs` | Get run logs |
| POST | `/v1/runs/{id}/cancel` | Cancel a run |

## Test

```bash
go test ./...
```

Docker-dependent tests skip automatically if Docker is unavailable. NATS-dependent tests skip if NATS is unavailable.

## Architecture

- `cmd/daemon/` — entrypoint
- `internal/server/` — HTTP API
- `internal/queue/` — bounded-concurrency run scheduler
- `internal/docker/` — Docker SDK wrapper
- `internal/kubernetes/` — kubectl-backed Kubernetes Job runner
- `internal/events/` — NATS JetStream event bus with no-op fallback
- `internal/ops/` — OperationEnvelope + idempotency broker
- `internal/store/` — SQLite persistence
- `pkg/contracts/` — shared types

