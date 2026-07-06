# services/db — Andromeda Postgres schema + migrations + sqlc

This package holds the application schema for Andromeda, implemented as
[golang-migrate](https://github.com/golang-migrate/migrate) sequential SQL
migrations plus [sqlc](https://sqlc.dev)-generated typed Go queries.

## Layout

```
services/db/
  migrations/   # NNNN_<group>.up.sql / .down.sql
  queries/      # *.sql hot-path query specs
  sqlc.yaml     # sqlc generator config
  go.mod        # module github.com/marius-patrik/agentos/inference-engine/services/db
```

## Migrations

Run against a Postgres with `pgvector`:

```bash
# example disposable local database
docker run -d --name vs0db -e POSTGRES_PASSWORD=vs0 -p 55432:5432 pgvector/pgvector:pg17

# apply all migrations
migrate -path services/db/migrations -database 'postgres://postgres:vs0@localhost:55432/postgres?sslmode=disable' up

# roll back all migrations
migrate -path services/db/migrations -database 'postgres://postgres:vs0@localhost:55432/postgres?sslmode=disable' down -all
```

Migration groups:

| Seq | Group | Tables / artifacts |
|-----|-------|--------------------|
| 0001 | extensions+core | `vector` extension |
| 0002 | registry+adapters | `capabilities`, `adapters`, `model_routes`, `model_health` |
| 0003 | jobs+domains | `jobs`, `job_domains` |
| 0004 | claims+runs | `claims`, `run_status` |
| 0005 | memory | `memory_embeddings` (pgvector, `source_class` enum + `hypothesis` flag) |
| 0006 | config+sync+eval | `config_projection`, `sync_manifests`, `consolidation_runs`, `eval_runs`, `eval_results`, `canary_state` |

## Regenerate sqlc

From `services/db`:

```bash
cd services/db
sqlc generate
```

Generated Go files land in the package root (`package db`) and are committed
alongside the source SQL.

After generating, verify the module builds:

```bash
go build ./...
```

## Schema design notes

- Table names are plural, columns use `snake_case`.
- Common audit columns: `created_at`, `updated_at` (`timestamptz`, default `now()`).
- JSONB columns carry flexible metadata; structured fields use enums where
  query patterns need them.
- Claims implement pause-aware TTL via `suspended_at` / `resumed_at`:
  `SuspendAllClaims` freezes expirations; `ResumeAllClaims` restores the
  remaining TTL from the suspension point.
- Run status uses the §15 OR2 vocabulary (`useful_result`, `no_artifact`,
  `missing_evidence`, `unresolved`, `blocked`, `failed`, `released`, `expired`).
- Memory uses `pgvector` `vector(1024)` with a provisional-dimension comment;
  `source_class` is `{verified, inferred}` and `hypothesis` is a separate
  boolean flag (not a third enum value).

