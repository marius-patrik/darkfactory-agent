#!/usr/bin/env bash
# Rommie single-node Postgres bring-up (VS2 / S3.0b).
#
# Brings up a persistent pgvector Postgres on the local node and applies the
# services/db migrations. localhost-bound; mTLS + the clustered kine→PG pair are
# deferred to VS3 (reach a remote instance via an ssh tunnel until then).
#
# Idempotent: safe to re-run. Reuses the existing data volume + password if present.
# NixOS-safe: no openssl dependency (uses python3, falls back to /dev/urandom+od).
#
#   ./bring-up.sh            # bring up + migrate + verify
#
# Env overrides:
#   PGDATA_DIR   data dir bind mount   (default: $HOME/.rommie/pg/data)
#   SECRET_FILE  password file (0600)  (default: $HOME/.rommie/secrets/pg_password)
#   PG_DB        database name         (default: rommie)
#   MIGRATIONS   migrations dir        (default: <repo>/services/db/migrations)
set -uo pipefail

PGDATA_DIR="${PGDATA_DIR:-$HOME/.rommie/pg/data}"
SECRET_FILE="${SECRET_FILE:-$HOME/.rommie/secrets/pg_password}"
PG_DB="${PG_DB:-rommie}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MIGRATIONS="${MIGRATIONS:-$REPO_ROOT/services/db/migrations}"

mkdir -p "$PGDATA_DIR" "$(dirname "$SECRET_FILE")"
chmod 700 "$(dirname "$SECRET_FILE")" 2>/dev/null || true

# --- password (generate once, 0600; reuse on re-run) ---
if [ ! -s "$SECRET_FILE" ]; then
  if command -v python3 >/dev/null 2>&1; then
    ( umask 077; python3 -c "import secrets;print(secrets.token_hex(24))" > "$SECRET_FILE" )
  else
    ( umask 077; od -An -tx1 -N24 /dev/urandom | tr -d ' \n' > "$SECRET_FILE" )
  fi
  chmod 600 "$SECRET_FILE"
  echo "[+] generated new Postgres password -> $SECRET_FILE (0600)"
fi
PGPASS="$(cat "$SECRET_FILE")"
[ -n "$PGPASS" ] || { echo "[FATAL] empty password in $SECRET_FILE"; exit 1; }

docker network inspect rommie >/dev/null 2>&1 || docker network create rommie >/dev/null

if docker ps -a --format '{{.Names}}' | grep -qx rommie-pg; then
  echo "[=] rommie-pg exists; starting"
  docker start rommie-pg >/dev/null
else
  echo "[+] creating rommie-pg (pgvector/pgvector:pg17)"
  docker run -d --name rommie-pg --restart unless-stopped --network rommie \
    -e POSTGRES_PASSWORD="$PGPASS" -e POSTGRES_DB="$PG_DB" \
    -v "$PGDATA_DIR":/var/lib/postgresql/data \
    -p 127.0.0.1:5432:5432 pgvector/pgvector:pg17 >/dev/null
fi

echo "[*] waiting for pg ready"
for i in $(seq 1 60); do
  docker exec rommie-pg pg_isready -U postgres -d "$PG_DB" >/dev/null 2>&1 && { echo "[+] ready (${i}s)"; break; }
  sleep 1
  [ "$i" = 60 ] && { echo "[FATAL] not ready; logs:"; docker logs --tail 20 rommie-pg; exit 1; }
done

echo "[*] applying migrations from $MIGRATIONS"
docker run --rm --network rommie -v "$MIGRATIONS":/migrations:ro \
  migrate/migrate -path /migrations \
  -database "postgres://postgres:${PGPASS}@rommie-pg:5432/${PG_DB}?sslmode=disable" up

echo "[*] verify"
docker exec rommie-pg psql -U postgres -d "$PG_DB" -tc \
  "select count(*) || ' tables' from information_schema.tables where table_schema='public';"
docker exec rommie-pg psql -U postgres -d "$PG_DB" -tc \
  "select 'schema_migrations version ' || version || ' dirty=' || dirty from schema_migrations;"
docker exec rommie-pg psql -U postgres -d "$PG_DB" -tc \
  "select 'pgvector ' || extversion from pg_extension where extname='vector';"
echo "[DONE] Postgres up + migrated. DSN: postgres://postgres:<pg_password>@127.0.0.1:5432/${PG_DB}?sslmode=disable"
