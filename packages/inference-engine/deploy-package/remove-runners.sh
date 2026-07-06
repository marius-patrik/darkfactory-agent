#!/usr/bin/env bash
# remove-runners.sh - Graceful decommissioning of local self-hosted runners.
# Idempotent: Removes containers and API registrations cleanly.

set -euo pipefail

# Configuration
REPO_OWNER="marius-patrik"
REPO_NAME="agents"

echo "=== Decommissioning GitHub Actions runners ==="

# 1. Validation
if [[ -z "${GH_TOKEN:-}" ]]; then
  echo "Error: GH_TOKEN environment variable is not set." >&2
  echo "Please set it with admin access to the repository '${REPO_OWNER}/${REPO_NAME}' to clean up registrations." >&2
  exit 1
fi

HOSTNAME=$(hostname)

# 2. Find and stop/remove local Docker containers
echo "Searching for local runner containers named 'runner-${HOSTNAME}-*'..."
CONTAINERS=$(docker ps -a --filter "name=runner-${HOSTNAME}-" --format "{{.Names}}")

if [[ -n "${CONTAINERS}" ]]; then
  for container in ${CONTAINERS}; do
    echo "Stopping container: ${container}"
    docker stop "${container}" >/dev/null 2>&1 || true
    echo "Removing container: ${container}"
    docker rm "${container}" >/dev/null 2>&1 || true
  done
else
  echo "No local runner containers found."
fi

# 3. Clean up GitHub API registrations
echo "Listing registered repository runners from GitHub..."
API_URL="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/actions/runners"

RUNNERS_JSON=$(curl -s \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer ${GH_TOKEN}" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "${API_URL}")

if echo "$RUNNERS_JSON" | grep -q "message.*Bad credentials"; then
  echo "Error: GitHub API returned Bad Credentials. Is GH_TOKEN correct?" >&2
  exit 1
fi

# Retrieve runner IDs to remove using Python or fallbacks
if command -v python3 >/dev/null 2>&1; then
  RUNNER_IDS=$(echo "$RUNNERS_JSON" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    runners = data.get('runners', [])
    prefix = 'runner-' + sys.argv[1] + '-'
    matching = [str(r['id']) for r in runners if r['name'].startswith(prefix)]
    print(' '.join(matching))
except Exception as e:
    sys.exit(1)
" "${HOSTNAME}")
else
  RUNNER_IDS=$(echo "$RUNNERS_JSON" | grep -B 1 "\"name\": \"runner-${HOSTNAME}-" | grep '"id":' | grep -o '[0-9]*' | tr '\n' ' ' || true)
fi

if [[ -n "${RUNNER_IDS// /}" ]]; then
  for id in ${RUNNER_IDS}; do
    echo "Deregistering runner ID ${id} from GitHub Actions..."
    DELETE_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
      -H "Accept: application/vnd.github+json" \
      -H "Authorization: Bearer ${GH_TOKEN}" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "${API_URL}/${id}")
    
    if [[ "${DELETE_RESPONSE}" == "204" ]]; then
      echo "Successfully deregistered runner ${id}."
    else
      echo "Failed to deregister runner ${id} (HTTP Status: ${DELETE_RESPONSE})." >&2
    fi
  done
else
  echo "No registered API runners found matching 'runner-${HOSTNAME}-*'."
fi

echo "=== Decommission completed successfully ==="
