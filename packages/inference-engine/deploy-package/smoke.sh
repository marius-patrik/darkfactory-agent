#!/usr/bin/env bash
# smoke.sh - Post-deployment smoke test suite.
# Validates NATS, Gateway, configured LLM roles, Daemon, and a test k8s Job run.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${DEPLOY_DIR}/../.." && pwd)"
OUTPUT=""
STATUS="running"
FAILED_STEP=""
GATEWAY_HEALTH=""
DAEMON_HEALTH=""
MANAGER_CONTROL_HEALTH=""
MANAGER_CONTROL_RUNS=""
RUN_RESPONSE=""
RUN_ID=""
JOB_NAME=""
ROLE_RESULTS="[]"
KUBECTL=()
GATEWAY_URL="${AGENTS_SMOKE_GATEWAY_URL:-http://localhost:4000}"
DAEMON_URL="${AGENTS_SMOKE_DAEMON_URL:-http://localhost:18080}"
DAEMON_URLS="${AGENTS_SMOKE_DAEMON_URLS:-${DAEMON_URL} http://s001:18080 http://s002:18080}"
MANAGER_URL="${AGENTS_SMOKE_MANAGER_URL:-http://localhost:18081}"
NATS_HOST="${AGENTS_SMOKE_NATS_HOST:-localhost}"
NATS_PORT="${AGENTS_SMOKE_NATS_PORT:-4222}"

usage() {
  cat <<'EOF'
usage: deploy/smoke.sh [--output path]

Runs post-deploy smoke checks. With --output, writes a JSON evidence report.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      OUTPUT="${2:?missing output path}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

write_report() {
  [ -n "$OUTPUT" ] || return 0
  python3 - "$OUTPUT" "$STATUS" "$FAILED_STEP" "$GATEWAY_HEALTH" "$DAEMON_HEALTH" "$MANAGER_CONTROL_HEALTH" "$MANAGER_CONTROL_RUNS" "$RUN_RESPONSE" "$RUN_ID" "$JOB_NAME" "$ROLE_RESULTS" "$GATEWAY_URL" "$DAEMON_URL" "$MANAGER_URL" "$NATS_HOST" "$NATS_PORT" <<'PY'
import json
import sys
from pathlib import Path

output, status, failed_step, gateway, daemon, manager_control_health, manager_control_runs, run_response, run_id, job_name, role_results, gateway_url, daemon_url, manager_url, nats_host, nats_port = sys.argv[1:17]

def maybe_json(raw: str):
    if not raw or raw == "FAILED":
        return None
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw

report = {
    "status": status,
    "failed_step": failed_step or None,
    "gateway_health": maybe_json(gateway),
    "daemon_health": maybe_json(daemon),
    "manager_control_health": maybe_json(manager_control_health),
    "manager_control_runs": maybe_json(manager_control_runs),
    "role_results": json.loads(role_results),
    "run_response": maybe_json(run_response),
    "run_id": run_id or None,
    "job_name": job_name or None,
    "endpoints": {
        "gateway": gateway_url,
        "daemon": daemon_url,
        "manager": manager_url,
        "nats": {"host": nats_host, "port": int(nats_port)},
    },
}
path = Path(output)
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

fail() {
  FAILED_STEP="$1"
  shift
  STATUS="failed"
  echo "  [FAIL] $*" >&2
  write_report
  exit 1
}

finish_ok() {
  STATUS="ok"
  write_report
}

json_field_true() {
  python3 - "$1" "$2" <<'PY'
import json
import sys
try:
    data = json.loads(sys.argv[1])
except json.JSONDecodeError:
    raise SystemExit(1)
raise SystemExit(0 if data.get(sys.argv[2]) is True else 1)
PY
}

echo "=== Running Post-Deployment Smoke Tests ==="

# 1. Check NATS Reachability
echo "Checking NATS JetStream connectivity..."
if command -v nc >/dev/null 2>&1; then
  if nc -z "$NATS_HOST" "$NATS_PORT"; then
    echo "  [OK] NATS is reachable on ${NATS_HOST}:${NATS_PORT}."
  else
    fail "nats" "NATS is NOT reachable on ${NATS_HOST}:${NATS_PORT}."
  fi
else
  echo "  [WARN] nc command not available. Skipping raw TCP check."
fi

# 2. Check Gateway Health
echo "Checking LLM Gateway health..."
GATEWAY_HEALTH=$(curl -s -f --max-time 5 "${GATEWAY_URL}/health" || echo "FAILED")
if [[ "${GATEWAY_HEALTH}" != "FAILED" ]]; then
  echo "  [OK] Gateway healthcheck passed."
  echo "  Gateway health info: ${GATEWAY_HEALTH}"
else
  fail "gateway_health" "Gateway /health endpoint is not responding."
fi

# 3. Test Gateway LLM role routing and completion. The registry decides which
# concrete local backend serves each role; this smoke must not hardcode model IDs.
for role in ${AGENTS_SMOKE_MODEL_ROLES:-general coding}; do
  echo "Testing LLM Gateway completion routing for role '${role}'..."
  completion_response=$(curl -s -f -X POST "${GATEWAY_URL}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{
      \"model\": \"${role}\",
      \"messages\": [{\"role\": \"user\", \"content\": \"Respond with the single word PONG.\"}]
    }" || echo "FAILED")

  if [[ "${completion_response}" == "FAILED" ]]; then
    fail "gateway_completion_${role}" "Gateway LLM chat completion routing failed for role '${role}'."
  fi
  ROLE_RESULTS=$(python3 - "$ROLE_RESULTS" "$role" "$completion_response" <<'PY'
import json
import sys
items = json.loads(sys.argv[1])
role = sys.argv[2]
raw = sys.argv[3]
try:
    parsed = json.loads(raw)
except json.JSONDecodeError:
    parsed = raw
items.append({"role": role, "ok": True, "response": parsed})
print(json.dumps(items))
PY
)

  echo "  [OK] Gateway LLM completion succeeded for role '${role}'."
  echo "  Response preview: $(echo "${completion_response}" | grep -o '"content":"[^"]*' | head -n 1 | cut -d'"' -f4 || echo "No content preview")"
done

# 4. Check Daemon Health
echo "Checking Go Daemon scheduler health..."
DAEMON_HEALTH=$(curl -s -f --max-time 5 "${DAEMON_URL}/health" || echo "FAILED")
if [[ "${DAEMON_HEALTH}" == "FAILED" ]]; then
  fail "daemon_health" "Daemon /health endpoint failed."
else
  echo "  [OK] Daemon health check passed."
  echo "  Daemon health info: ${DAEMON_HEALTH}"
fi

# 5. Check manager production control API used by the TUI.
echo "Checking Manager production control API..."
MANAGER_CONTROL_HEALTH=$(curl -s -f --max-time 5 "${MANAGER_URL}/v1/control/health" || echo "FAILED")
if [[ "${MANAGER_CONTROL_HEALTH}" == "FAILED" ]]; then
  fail "manager_control_health" "Manager /v1/control/health endpoint failed."
fi
MANAGER_CONTROL_RUNS=$(curl -s -f --max-time 5 "${MANAGER_URL}/v1/control/runs" || echo "FAILED")
if [[ "${MANAGER_CONTROL_RUNS}" == "FAILED" ]]; then
  fail "manager_control_runs" "Manager /v1/control/runs endpoint failed."
fi
if ! python3 - "$MANAGER_CONTROL_HEALTH" "$MANAGER_CONTROL_RUNS" <<'PY'
import json
import sys

health = json.loads(sys.argv[1])
runs = json.loads(sys.argv[2])
raise SystemExit(0 if health.get("status") == "healthy" and isinstance(runs.get("runs"), list) else 1)
PY
then
  fail "manager_control_shape" "Manager control API returned an unexpected JSON shape."
fi
echo "  [OK] Manager production control API is reachable."

# 6. Dispatch a daemon run and confirm it is scheduled as a k8s Job.
echo "Testing daemon k8s Job dispatch..."
if command -v kubectl >/dev/null 2>&1 && kubectl get namespace "${AGENTS_K8S_NAMESPACE:-agents}" >/dev/null 2>&1; then
  KUBECTL=(kubectl)
elif command -v docker >/dev/null 2>&1 && docker inspect agents-daemon >/dev/null 2>&1 && docker exec agents-daemon kubectl get namespace "${AGENTS_K8S_NAMESPACE:-agents}" >/dev/null 2>&1; then
  KUBECTL=(docker exec agents-daemon kubectl)
else
  fail "kubernetes_namespace" "Kubernetes namespace '${AGENTS_K8S_NAMESPACE:-agents}' is not reachable through runner kubectl or agents-daemon kubectl."
fi

DISPATCH_DAEMON_URL=""
for _ in $(seq 1 30); do
  for candidate in ${DAEMON_URLS}; do
    candidate_health=$(curl -s -f --max-time 5 "${candidate}/health" || echo "FAILED")
    if [[ "${candidate_health}" != "FAILED" ]] && json_field_true "${candidate_health}" "leader"; then
      DISPATCH_DAEMON_URL="${candidate}"
      DAEMON_HEALTH="${candidate_health}"
      break 2
    fi
  done
  sleep 2
done
if [[ -z "${DISPATCH_DAEMON_URL}" ]]; then
  fail "daemon_leader" "No daemon leader became available for run submission."
fi
echo "  [OK] Daemon leader selected for run submission: ${DISPATCH_DAEMON_URL}"

RUN_RESPONSE=$(curl -s -f -X POST "${DISPATCH_DAEMON_URL}/v1/runs" \
  -H "Content-Type: application/json" \
  -d '{
    "image": "busybox:1.36",
    "command": ["sh", "-c", "echo agents-k8s-smoke"],
    "labels": {"tenant": "smoke", "source": "deploy-smoke"}
  }' || echo "FAILED")

if [[ "${RUN_RESPONSE}" == "FAILED" ]]; then
  fail "daemon_submit" "Daemon run submission failed."
fi
RUN_ID=$(echo "${RUN_RESPONSE}" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
if [[ -z "${RUN_ID}" ]]; then
  fail "parse_run_id" "Could not parse daemon run id from: ${RUN_RESPONSE}"
fi
JOB_NAME="agent-$(echo "${RUN_ID}" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9-]/-/g' | cut -c1-52 | sed 's/-*$//')"
if "${KUBECTL[@]}" -n "${AGENTS_K8S_NAMESPACE:-agents}" get job "${JOB_NAME}" >/dev/null 2>&1; then
  echo "  [OK] Confirmed k8s Job created: ${JOB_NAME}"
else
  fail "kubernetes_job" "Expected k8s Job '${JOB_NAME}' was not found."
fi

echo "=== All Smoke Tests Passed Successfully! ==="
finish_ok

