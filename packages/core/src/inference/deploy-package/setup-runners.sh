#!/usr/bin/env bash
# setup-runners.sh - Bounded pool registration for self-hosted GitHub Actions runners.
# Idempotent: Can be run multiple times; replaces old runners cleanly.

set -euo pipefail

# Configuration
REPO_OWNER="marius-patrik"
REPO_NAME="agents"
HOSTNAME=$(hostname)
case "${HOSTNAME}" in
  s001|s002) ;;
  *)
    echo "Error: refusing to register '${HOSTNAME}' as a cluster runner; expected s001 or s002." >&2
    exit 1
    ;;
esac

LABELS="self-hosted,agents,cluster,cluster-node"
DEFAULT_NUM_RUNNERS=2
GH_CLI_VERSION="${GH_CLI_VERSION:-2.74.0}"

# Number of runners to deploy
NUM_RUNNERS=${1:-$DEFAULT_NUM_RUNNERS}

echo "=== Setting up ${NUM_RUNNERS} GitHub Actions runners ==="

# 1. Validation
if [[ -z "${GH_TOKEN:-}" ]]; then
  echo "Error: GH_TOKEN environment variable is not set." >&2
  echo "Please set it with admin access to the repository '${REPO_OWNER}/${REPO_NAME}'." >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "Error: docker command not found. Ensure Docker is installed." >&2
  exit 1
fi

# 2. Get registration token from GitHub API
echo "Fetching runner registration token..."
API_URL="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/actions/runners/registration-token"

RESPONSE=$(curl -s -X POST \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer ${GH_TOKEN}" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "${API_URL}")

# Parse token cleanly using python or jq if available
if echo "$RESPONSE" | grep -q "message.*Bad credentials"; then
  echo "Error: GitHub API returned Bad Credentials. Is GH_TOKEN correct and active?" >&2
  exit 1
fi

if command -v jq >/dev/null 2>&1; then
  RUNNER_TOKEN=$(echo "$RESPONSE" | jq -r '.token')
elif command -v python3 >/dev/null 2>&1; then
  RUNNER_TOKEN=$(echo "$RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('token', ''))")
else
  RUNNER_TOKEN=$(echo "$RESPONSE" | grep -o '"token": "[^"]*' | grep -o '[^"]*$' || true)
fi

if [[ -z "${RUNNER_TOKEN:-}" || "${RUNNER_TOKEN}" == "null" ]]; then
  echo "Error: Failed to retrieve runner registration token. Response:" >&2
  echo "${RESPONSE}" >&2
  exit 1
fi

# 3. Clean up existing runners on this host (Idempotency)
echo "Ensuring clean slate on node '${HOSTNAME}'..."

for i in $(seq 1 "${NUM_RUNNERS}"); do
  RUNNER_NAME="runner-${HOSTNAME}-${i}"
  if docker ps -a --format '{{.Names}}' | grep -q "^${RUNNER_NAME}$"; then
    echo "Stopping and removing existing runner container: ${RUNNER_NAME}"
    docker stop "${RUNNER_NAME}" >/dev/null 2>&1 || true
    docker rm "${RUNNER_NAME}" >/dev/null 2>&1 || true
  fi
done

# 4. Spin up the new bounded pool of runners
echo "Launching ${NUM_RUNNERS} runner container(s)..."
for i in $(seq 1 "${NUM_RUNNERS}"); do
  RUNNER_NAME="runner-${HOSTNAME}-${i}"
  
  echo "Starting ${RUNNER_NAME}..."
  docker run -d \
    --name "${RUNNER_NAME}" \
    --restart on-failure:5 \
    --network host \
    -e URL="https://github.com/${REPO_OWNER}/${REPO_NAME}" \
    -e TOKEN="${RUNNER_TOKEN}" \
    -e NAME="${RUNNER_NAME}" \
    -e LABELS="${LABELS}" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v /usr/libexec/docker/cli-plugins:/usr/libexec/docker/cli-plugins:ro \
    --group-add $(getent group docker | cut -d: -f3) \
    ghcr.io/actions/actions-runner:latest \
    /bin/bash -c "set -euo pipefail
      if ! command -v gh >/dev/null 2>&1; then
        tmp=\$(mktemp -d)
        curl -fsSL \"https://github.com/cli/cli/releases/download/v${GH_CLI_VERSION}/gh_${GH_CLI_VERSION}_linux_amd64.tar.gz\" | tar -xz -C \"\$tmp\"
        sudo install -m 0755 \"\$tmp/gh_${GH_CLI_VERSION}_linux_amd64/bin/gh\" /usr/local/bin/gh
        rm -rf \"\$tmp\"
      fi
      ./config.sh --url \$URL --token \$TOKEN --name \$NAME --labels \$LABELS --unattended --replace
      ./run.sh" >/dev/null

  echo "Runner ${RUNNER_NAME} container started successfully."
done

echo "=== Setup Completed: All ${NUM_RUNNERS} runners are active ==="
