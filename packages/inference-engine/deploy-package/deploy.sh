#!/usr/bin/env bash
# deploy.sh - Core cluster deployment script.
# Syncs code -> builds images -> compose up -> checks health.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${DEPLOY_DIR}/../.." && pwd)"

echo "=== Starting Cluster Deployment ==="

# 1. Sync repository code
cd "${REPO_ROOT}"
if [[ -d ".git" ]]; then
  echo "Local git repo found. Pulling latest code..."
  # Stash local changes if any, to avoid conflicts
  git stash -q || true
  DEPLOY_BRANCH="${DEPLOY_BRANCH:-dev}"
  git pull origin "${DEPLOY_BRANCH}" --rebase || true
  git stash pop -q >/dev/null 2>&1 || true
else
  echo "Not a git repository. Skipping git pull..."
fi

# 2. Build local Docker images
echo "Building Docker images..."

# Build Go Services (Daemon + Manager)
echo "Building Go services (Daemon and Manager)..."
docker build \
  -f "${DEPLOY_DIR}/Dockerfile.daemon" \
  -t agents-go-services:latest \
  "${REPO_ROOT}/engine/go"

# Build Agent Harness
echo "Building Agent Harness..."
docker build \
  -f "${DEPLOY_DIR}/Dockerfile.harness" \
  -t agents/harness:latest \
  "${REPO_ROOT}"

# Build Gateway Service (built inside compose up automatically, but we pre-build it to ensure safety)
echo "Building Gateway..."
docker build \
  -t agents-gateway:latest \
  "${REPO_ROOT}/gateway"

# 3. Start Docker Compose Cluster Services
echo "Launching Compose services..."
docker network create agents-inference >/dev/null 2>&1 || true
export AGENTS_ROOT="${AGENTS_ROOT:-/home/patrik/agents}"
export AGENTS_REPO="${AGENTS_REPO:-${REPO_ROOT}}"
if [[ -n "${NODE_ID:-}" ]]; then
  export AGENTS_DAEMON_DATA_DIR="${AGENTS_DAEMON_DATA_DIR:-./data/${NODE_ID}-daemon}"
  export AGENTS_MANAGER_DATA_DIR="${AGENTS_MANAGER_DATA_DIR:-./data/${NODE_ID}-manager}"
else
  export AGENTS_DAEMON_DATA_DIR="${AGENTS_DAEMON_DATA_DIR:-./data/daemon}"
  export AGENTS_MANAGER_DATA_DIR="${AGENTS_MANAGER_DATA_DIR:-./data/manager}"
fi
export AGENTS_KUBECONFIG_HOST_PATH="${AGENTS_KUBECONFIG_HOST_PATH:-/home/patrik/k3s-client.yaml}"
export AGENTS_HARNESS_IMAGE="${AGENTS_HARNESS_IMAGE:-agents/harness:latest}"
if [[ -n "${AGENTS_COMPOSE_CMD:-}" ]]; then
  read -r -a compose_cmd <<< "${AGENTS_COMPOSE_CMD}"
elif docker compose -f "${DEPLOY_DIR}/docker-compose.cluster.yml" config >/dev/null 2>&1; then
  compose_cmd=(docker compose)
elif command -v docker-compose >/dev/null 2>&1 && docker-compose -f "${DEPLOY_DIR}/docker-compose.cluster.yml" config >/dev/null 2>&1; then
  compose_cmd=(docker-compose)
else
  echo "Error: no usable Docker Compose command found. Set AGENTS_COMPOSE_CMD or install docker compose/docker-compose." >&2
  exit 125
fi
if ! "${compose_cmd[@]}" -f "${DEPLOY_DIR}/docker-compose.cluster.yml" config >/dev/null 2>&1; then
  echo "Error: configured Docker Compose command failed validation: ${compose_cmd[*]}" >&2
  exit 125
fi
"${compose_cmd[@]}" -f "${DEPLOY_DIR}/docker-compose.cluster.yml" up -d

# 4. Wait for all services to pass their Healthchecks
echo "Waiting for services to become healthy..."
max_attempts=30
attempt=1

# Helper to check service health
check_health() {
  local service=$1
  local status
  status=$(docker inspect --format='{{json .State.Health.Status}}' "$service" 2>/dev/null || echo '"unknown"')
  echo "$status" | tr -d '"'
}

while [ $attempt -le $max_attempts ]; do
  nats_h=$(check_health "agents-nats")
  gateway_h=$(check_health "agents-gateway")
  daemon_h=$(check_health "agents-daemon")

  echo "Attempt $attempt/$max_attempts - Health statuses:"
  echo "  nats:      $nats_h"
  echo "  gateway:   $gateway_h"
  echo "  daemon:    $daemon_h"

  # NATS, Gateway, and Daemon must be healthy. Model backend health is reported
  # by the gateway registry because backends may be local llama.cpp, NVCF, or
  # another OpenAI-compatible service depending on the active release wave.
  if [[ "$nats_h" == "healthy" && "$gateway_h" == "healthy" && "$daemon_h" == "healthy" ]]; then
    echo "=== Core services are HEALTHY! ==="
    break
  fi

  if [ $attempt -eq $max_attempts ]; then
    echo "Error: Services did not become healthy within the timeout period." >&2
    exit 1
  fi

  sleep 10
  attempt=$((attempt + 1))
done

echo "=== Deployment Completed Successfully ==="

