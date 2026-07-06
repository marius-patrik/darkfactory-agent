# deploy/postgres — single-node Postgres (VS2 / S3.0b)

The relational store for Rommie (D-023): capability registry/scorecards, jobs, the
claims+TTL ledger, run-status (no-false-green source-separated state), config projection,
sync manifests, eval/canary, and the pgvector memory index. Schema + migrations live in
[`services/db`](../../services/db).

**Scope (VS2):** a single persistent node, **localhost-bound**, no TLS. The clustered
kine→Postgres pair (primary s002 + sync-standby s001, out-of-k3s, mTLS) lands in **VS3**.

## Bring up

```bash
deploy/postgres/bring-up.sh          # create/start rommie-pg + apply migrations + verify
```

Idempotent and NixOS-safe (no `openssl` needed). It:
1. materializes a random password to `~/.rommie/secrets/pg_password` (0600) — reused on re-run;
2. runs `pgvector/pgvector:pg17` as `rommie-pg` on the `rommie` docker network, data on
   `~/.rommie/pg/data`, published to `127.0.0.1:5432` only;
3. applies `services/db/migrations` via the `migrate/migrate` container;
4. verifies table count, `schema_migrations` version, and the `vector` extension.

### Declarative form (compose)

```bash
cp deploy/postgres/.env.example deploy/postgres/.env
#  POSTGRES_PASSWORD=$(cat ~/.rommie/secrets/pg_password) ; ROMMIE_PG_DATA=/home/<user>/.rommie/pg/data
docker compose -f deploy/postgres/docker-compose.yml up -d
docker compose -f deploy/postgres/docker-compose.yml --profile migrate run --rm migrate
```

`.env` is gitignored (carries the password). `bring-up.sh` is the validated path used to stand
up the live VS2 instance on s002; the compose file is the canonical declarative equivalent (VS3
deploy builds on it).

## Connection

```
postgres://postgres:$(cat ~/.rommie/secrets/pg_password)@127.0.0.1:5432/rommie?sslmode=disable
```

Remote access until VS3 mTLS: `ssh -f -N -L 15432:127.0.0.1:5432 s002.ts` then connect to
`127.0.0.1:15432`. The gateway (S3.2) and agent loop (S3.3) read the DSN from the secrets/config
layer — not committed here.

## Notes

- `run_status.status` uses the pg enum `run_status_value` (§15 OR2 vocab). The proto `RunStatus`
  ↔ this enum ↔ D6 §1 reconciliation is tracked in #1276 (S3.5).
- Data persists in the `~/.rommie/pg/data` bind mount across container recreation.
- mTLS, per-service DB roles, and the synchronous standby are VS3 (`08-secrets-mtls.md` SEC2a
  bring-up order: CA → datastore-Postgres → k3s → cert-manager → services).
