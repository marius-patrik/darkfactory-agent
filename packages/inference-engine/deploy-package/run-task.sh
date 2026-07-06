#!/usr/bin/env bash
# run-task.sh — dispatched-run execution contract for the agents platform.
# Clones the repo at the manager-created branch, runs the headless agent
# harness on the task (tenant editable-surface enforced), commits, and pushes.
# Invoked as the k8s Job command; all inputs arrive via env from the manager.
set -uo pipefail

EVIDENCE_ROOT="${AGENTS_ROOT:-${HOME:-/tmp}/agents}"
EVIDENCE_RUN_ID="${AGENTS_RUN_ID:-unknown-run}"
EVIDENCE_FORCE_TELEMETRY=1
WORK="${AGENTS_WORKDIR:-/tmp/agent-work}"
rm -rf "$WORK"; mkdir -p "$WORK"; cd "$WORK"
AGENTS_SESSION_ID="${AGENTS_SESSION_ID:-${AGENTS_RUN_ID:-}}"
export AGENTS_SESSION_ID
agent_completed=0
REPO_ROOT=""
tenant_root=""
artifact_kind=""
artifact_paths=""
trusted_qft_guard=""

evidence_dir() {
  if [ "$EVIDENCE_FORCE_TELEMETRY" != "1" ] && [ -n "${AGENTS_TENANT:-}" ] && [ -d "$EVIDENCE_ROOT/projects/$AGENTS_TENANT" ]; then
    printf '%s\n' "$EVIDENCE_ROOT/projects/$AGENTS_TENANT/runs"
  else
    printf '%s\n' "$EVIDENCE_ROOT/telemetry/runs"
  fi
}

write_terminal_evidence_to_dir() {
  local dir="$1"
  local status="$2"
  local failure_kind="$3"
  local message="$4"
  if ! mkdir -p "$dir" 2>/dev/null; then
    echo "[run-task] evidence directory is not writable: $dir" >&2
    return 1
  fi
  if [ ! -w "$dir" ]; then
    echo "[run-task] evidence directory is not writable: $dir" >&2
    return 1
  fi

  local evidence_path="$dir/$EVIDENCE_RUN_ID.json"
  local head_sha=""
  if [ -n "$REPO_ROOT" ] && [ -d "$REPO_ROOT/.git" ]; then
    head_sha="$(cd "$REPO_ROOT" && git rev-parse HEAD 2>/dev/null || true)"
  fi
  if [ -n "${AGENTS_HEAD_SHA:-}" ] && [ "$head_sha" != "$AGENTS_HEAD_SHA" ]; then
    status="infra-failed"
    failure_kind="identity"
    message="head_sha ${head_sha:-<empty>} does not match ${AGENTS_HEAD_SHA}"
  fi
  local now
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local paths_json="[]"
  if [ -n "$artifact_paths" ]; then
    paths_json="$(printf '%s\n' "$artifact_paths" | python3 -c 'import json,sys; print(json.dumps([line.strip() for line in sys.stdin if line.strip()]))')"
  fi

  local tmp
  tmp="$(mktemp "$dir/.${EVIDENCE_RUN_ID}.XXXXXX")" || return 1
  RUN_TASK_EVIDENCE_PATH="$tmp" \
  RUN_TASK_RUN_ID="$EVIDENCE_RUN_ID" \
  RUN_TASK_TASK_ID="${AGENTS_TASK_ID:-$EVIDENCE_RUN_ID}" \
  RUN_TASK_TENANT="${AGENTS_TENANT:-}" \
  RUN_TASK_ISSUE_NUMBER="${AGENTS_ISSUE_NUMBER:-0}" \
  RUN_TASK_BRANCH="${AGENTS_BRANCH:-}" \
  RUN_TASK_PR_URL="${AGENTS_PR_URL:-}" \
  RUN_TASK_LOG_ISSUE_NUMBER="${AGENTS_LOG_ISSUE_NUMBER:-0}" \
  RUN_TASK_HEAD_SHA="$head_sha" \
  RUN_TASK_K8S_NAMESPACE="${AGENTS_K8S_NAMESPACE:-}" \
  RUN_TASK_K8S_JOB_NAME="${AGENTS_K8S_JOB_NAME:-}" \
  RUN_TASK_K8S_POD_NAME="${AGENTS_K8S_POD_NAME:-${HOSTNAME:-}}" \
  RUN_TASK_K8S_CONTAINER_NAME="${AGENTS_K8S_CONTAINER_NAME:-}" \
  RUN_TASK_LOG_REF="${AGENTS_LOG_REF:-}" \
  RUN_TASK_RATCHET_VERDICT="${AGENTS_RATCHET_VERDICT:-}" \
  RUN_TASK_RATCHET_HEAD_SHA="${AGENTS_RATCHET_HEAD_SHA:-$head_sha}" \
  RUN_TASK_RATCHET_NORTH_STARS_REGRESSED="${AGENTS_RATCHET_NORTH_STARS_REGRESSED:-false}" \
  RUN_TASK_RATCHET_HELD_OUT_SCORE_DELTA="${AGENTS_RATCHET_HELD_OUT_SCORE_DELTA:-0}" \
  RUN_TASK_STATUS="$status" \
  RUN_TASK_ARTIFACT_KIND="$artifact_kind" \
  RUN_TASK_PATHS_JSON="$paths_json" \
  RUN_TASK_FAILURE_KIND="$failure_kind" \
  RUN_TASK_FAILURE_MESSAGE="$message" \
  RUN_TASK_NOW="$now" \
  python3 <<'PY'
import json
import os
from pathlib import Path

payload = {
    "run_id": os.environ["RUN_TASK_RUN_ID"],
    "task_id": os.environ["RUN_TASK_TASK_ID"],
    "tenant": os.environ["RUN_TASK_TENANT"],
    "issue_number": int(os.environ["RUN_TASK_ISSUE_NUMBER"]),
    "branch": os.environ["RUN_TASK_BRANCH"],
    "pr_url": os.environ["RUN_TASK_PR_URL"],
    "log_issue_number": int(os.environ["RUN_TASK_LOG_ISSUE_NUMBER"]),
    "head_sha": os.environ["RUN_TASK_HEAD_SHA"],
    "status": os.environ["RUN_TASK_STATUS"],
    "artifact": {
        "kind": os.environ["RUN_TASK_ARTIFACT_KIND"],
        "paths": json.loads(os.environ["RUN_TASK_PATHS_JSON"]),
    },
    "ratchet": {
        "verdict": os.environ["RUN_TASK_RATCHET_VERDICT"],
        "head_sha": os.environ["RUN_TASK_RATCHET_HEAD_SHA"],
        "north_stars_regressed": os.environ["RUN_TASK_RATCHET_NORTH_STARS_REGRESSED"].lower() == "true",
        "held_out_score_delta": float(os.environ["RUN_TASK_RATCHET_HELD_OUT_SCORE_DELTA"] or 0),
    },
    "kubernetes": {
        "namespace": os.environ["RUN_TASK_K8S_NAMESPACE"],
        "job_name": os.environ["RUN_TASK_K8S_JOB_NAME"],
        "pod_name": os.environ["RUN_TASK_K8S_POD_NAME"],
        "container_name": os.environ["RUN_TASK_K8S_CONTAINER_NAME"],
        "log_ref": os.environ["RUN_TASK_LOG_REF"],
    },
    "failure": {
        "kind": os.environ["RUN_TASK_FAILURE_KIND"],
        "message": os.environ["RUN_TASK_FAILURE_MESSAGE"],
    },
    "created_at": os.environ["RUN_TASK_NOW"],
    "updated_at": os.environ["RUN_TASK_NOW"],
}
Path(os.environ["RUN_TASK_EVIDENCE_PATH"]).write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
  python3 - "$tmp" <<'PY' || { rm -f "$tmp"; return 1; }
import os
import sys
with open(sys.argv[1], "rb") as handle:
    os.fsync(handle.fileno())
PY
  mv "$tmp" "$evidence_path"
  python3 - "$dir" <<'PY' || true
import os
import sys
fd = os.open(sys.argv[1], os.O_RDONLY)
try:
    os.fsync(fd)
finally:
    os.close(fd)
PY
  echo "[run-task] wrote terminal evidence: $evidence_path"
}

write_terminal_evidence() {
  local status="$1"
  local failure_kind="$2"
  local message="$3"
  local dir fallback_dir
  dir="$(evidence_dir)"
  fallback_dir="$EVIDENCE_ROOT/telemetry/runs"
  if write_terminal_evidence_to_dir "$dir" "$status" "$failure_kind" "$message"; then
    return 0
  fi
  if [ "$dir" != "$fallback_dir" ]; then
    echo "[run-task] falling back to telemetry evidence: $fallback_dir" >&2
    write_terminal_evidence_to_dir "$fallback_dir" "$status" "$failure_kind" "$message"
    return $?
  fi
  return 1
}

finish_run() {
  local status="$1"
  local failure_kind="$2"
  local message="$3"
  local exit_code="$4"
  if ! write_terminal_evidence "$status" "$failure_kind" "$message"; then
    [ "$status" = "succeeded" ] && exit_code=1
    echo "[run-task] terminal evidence write failed" >&2
  fi
  exit "$exit_code"
}

require_env() {
  local name="$1"
  if [ -z "${!name:-}" ]; then
    finish_run "infra-failed" "infra" "missing required env: $name" 1
  fi
}

require_env AGENTS_REPO
require_env AGENTS_BRANCH
require_env AGENTS_TASK
require_env GH_TOKEN
require_env AGENTS_GATEWAY_URL
require_env AGENTS_ROOT
require_env AGENTS_RUN_ID
EVIDENCE_FORCE_TELEMETRY=0

mark_session_error() {
  reason="$1"
  sid="${AGENTS_SESSION_ID:-}"
  [ -n "$sid" ] || return 0
  root="${AGENTS_ROOT:-$HOME/agents}"
  session_path="$root/sessions/$sid.json"
  [ -f "$session_path" ] || return 0
  python3 - "$session_path" "$reason" <<'PY' || true
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

path = Path(sys.argv[1])
reason = sys.argv[2]
payload = json.loads(path.read_text(encoding="utf-8"))
payload["status"] = "error"
payload["error"] = reason
payload["updatedAt"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
PY
}

fail_run() {
  reason="$1"
  status="${2:-failed}"
  failure_kind="${3:-tenant}"
  echo "[run-task] $reason" >&2
  if [ "$agent_completed" -eq 1 ]; then
    mark_session_error "$reason"
  fi
  finish_run "$status" "$failure_kind" "$reason" 1
}

validate_qft_proof_certificate() {
  local proof_path="$1"
  python3 - "$REPO_ROOT" "$proof_path" <<'PY'
import json
import sys
from pathlib import Path

repo_root = Path(sys.argv[1])
proof_path = Path(sys.argv[2])
try:
    payload = json.loads(proof_path.read_text(encoding="utf-8"))
except Exception as exc:
    raise SystemExit(f"invalid JSON: {exc}")
if not isinstance(payload, dict):
    raise SystemExit("certificate payload must be an object")

status = str(payload.get("proof_status") or payload.get("certificate_status") or "")
if status not in {"proved", "proved_conditional", "certified"}:
    raise SystemExit(f"certificate status is not verifier-backed: {status!r}")

report = payload.get("machine_verification_report")
if not isinstance(report, dict):
    raise SystemExit("missing machine_verification_report object")
report_status = str(report.get("status") or "")
report_path_text = str(report.get("path") or "")
if report_status and report_status != "passed":
    raise SystemExit(f"machine_verification_report.status is not passed: {report_status!r}")
if not report_path_text.startswith("research/proof_certificates/"):
    raise SystemExit("machine_verification_report.path must stay under research/proof_certificates/")

tenant_root = repo_root / ".agents" / "projects" / "qft"
if not tenant_root.exists():
    tenant_root = repo_root / "projects" / "qft"
report_path = tenant_root / report_path_text
if not report_path.exists():
    raise SystemExit(f"machine verification report is missing: {report_path_text}")
try:
    report_payload = json.loads(report_path.read_text(encoding="utf-8"))
except Exception as exc:
    raise SystemExit(f"machine verification report is invalid JSON: {exc}")
if not isinstance(report_payload, dict):
    raise SystemExit("machine verification report payload must be an object")
if str(report_payload.get("status") or "") != "passed":
    checks = report_payload.get("checks")
    if not isinstance(checks, dict) or checks.get("status") != "passed":
        raise SystemExit("machine verification report did not pass")
PY
}

echo "[run-task] cloning ${AGENTS_REPO}@${AGENTS_BRANCH} (blobless partial clone)"
# Blobless partial clone: skip file contents (the repo tree is large); blobs
# are fetched lazily only for files the agent actually reads.
if ! git clone --depth 1 --single-branch --filter=blob:none --branch "$AGENTS_BRANCH" \
    "https://x-access-token:${GH_TOKEN}@github.com/${AGENTS_REPO}.git" repo; then
  echo "[run-task] clone failed" >&2
  finish_run "infra-failed" "infra" "clone failed" 1
fi
cd repo
REPO_ROOT="$PWD"
git config user.email "agents-bot@users.noreply.github.com"
git config user.name "agents-bot"

tenant_args=(); [ -n "${AGENTS_TENANT:-}" ] && tenant_args=(--tenant "$AGENTS_TENANT")
cloud_args=();  [ -n "${AGENTS_ALLOW_CLOUD:-}" ] && cloud_args=(--allow-cloud)

gateway_health_url="${AGENTS_GATEWAY_URL%/}/health"
echo "[run-task] checking gateway health at ${gateway_health_url}"
if ! curl -fsS "$gateway_health_url" >/dev/null; then
  echo "[run-task] gateway preflight failed: ${gateway_health_url}" >&2
  finish_run "infra-failed" "infra" "gateway preflight failed: ${gateway_health_url}" 1
fi

# Run the agent from the tenant root so its CWD matches the tenant's editable
# surface (tenant.yaml paths like train.py are tenant-root-relative).
run_dir="$REPO_ROOT"
if [ -n "${AGENTS_TENANT:-}" ]; then
  if [ -d "$REPO_ROOT/.user/projects/$AGENTS_TENANT" ]; then
    tenant_root="$REPO_ROOT/.user/projects/$AGENTS_TENANT"
  fi
  if [ -z "$tenant_root" ]; then
    fail_run "missing tenant config: $AGENTS_TENANT" "failed" "tenant"
  fi
fi
if [ -n "$tenant_root" ]; then
  run_dir="$tenant_root"
fi
if [ "${AGENTS_TENANT:-}" = "qft" ]; then
  if [ -f "$REPO_ROOT/.user/projects/qft/guard_changed_paths.py" ]; then
    trusted_qft_guard="$WORK/qft-guard-changed-paths.py"
    cp "$REPO_ROOT/.user/projects/qft/guard_changed_paths.py" "$trusted_qft_guard"
  fi
fi
cd "$run_dir"
agent_workspace_root="$REPO_ROOT"
if [ -n "$tenant_root" ]; then
  agent_workspace_root="$tenant_root"
fi
echo "[run-task] running agent in $run_dir (model=${AGENTS_MODEL:-coding} tenant=${AGENTS_TENANT:-none})"
set +e
AGENTS_WORKSPACE_ROOT="$agent_workspace_root" bun /app/agent/src/cli/agent.ts run "$AGENTS_TASK" \
  --dangerously-skip-permissions \
  --workspace-root "$agent_workspace_root" \
  --model "${AGENTS_MODEL:-coding}" \
  "${tenant_args[@]}" "${cloud_args[@]}"
agent_status=$?
set -e
echo "[run-task] agent exited ${agent_status}"
if [ "$agent_status" -ne 0 ]; then
  echo "[run-task] agent failed with exit code ${agent_status}" >&2
  finish_run "failed" "tenant" "agent failed with exit code ${agent_status}" "$agent_status"
fi
agent_completed=1
cd "$REPO_ROOT"

# Stage edits, but drop harness-internal session noise the agent writes into CWD.
git add -A
git reset -q -- sessions 2>/dev/null || true
git checkout -q -- sessions 2>/dev/null || true
if [ "${AGENTS_TENANT:-}" = "qft" ]; then
  changed_paths="$(git diff --cached --name-only)"
  if [ -n "$changed_paths" ]; then
    # The harness file tools enforce this surface, but bash can still write files.
    # Keep the execution contract honest by rejecting any staged frozen-surface edit.
    if [ -n "$trusted_qft_guard" ]; then
      python3 "$trusted_qft_guard" $changed_paths || fail_run "QFT staged paths failed editable-surface validation"
    elif [ -f .user/projects/qft/guard_changed_paths.py ]; then
      python3 .user/projects/qft/guard_changed_paths.py $changed_paths || fail_run "QFT staged paths failed editable-surface validation"
    else
      fail_run "missing QFT changed-path guard"
    fi
    proof_paths="$(printf '%s\n' "$changed_paths" | grep -E '^(\.user/)?projects/qft/research/proof_certificates/.*\.json$' || true)"
    if [ -z "$proof_paths" ]; then
      fail_run "QFT runs must stage at least one proof certificate JSON"
    fi
    artifact_kind="proof-certificate"
    artifact_paths="$proof_paths"
	    for proof_path in $proof_paths; do
	      validate_qft_proof_certificate "$proof_path" || {
	        fail_run "invalid QFT proof certificate: $proof_path"
	      }
	    done
  fi
fi
if git diff --cached --quiet; then
  echo "[run-task] NO CHANGES — agent produced no edits"
  if [ "${AGENTS_ALLOW_NOOP:-0}" = "1" ] && [ -z "${AGENTS_TENANT:-}" ]; then
    artifact_kind="none"
    finish_run "no-op" "none" "" 0
  else
    fail_run "NO CHANGES — agent produced no edits"
  fi
fi
if [ "${AGENTS_TENANT:-}" = "qft" ]; then
  artifact_kind="${artifact_kind:-proof-certificate}"
  artifact_paths="${artifact_paths:-$proof_paths}"
else
  artifact_kind="code-edit"
  artifact_paths="$(git diff --cached --name-only)"
fi
git commit -m "agent: ${AGENTS_TASK_TITLE:-automated task} (run ${AGENTS_RUN_ID:-unknown})" || fail_run "commit failed" "infra-failed" "infra"
if git push origin "HEAD:${AGENTS_BRANCH}"; then
  echo "[run-task] PUSHED to ${AGENTS_BRANCH}"
  finish_run "succeeded" "none" "" 0
else
  fail_run "push failed" "infra-failed" "infra"
fi

