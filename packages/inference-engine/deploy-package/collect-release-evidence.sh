#!/usr/bin/env bash
# Assemble release/deploy evidence into a single immutable manifest.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEPLOY_DIR/../.." && pwd)"
TAG=""
EVIDENCE_DIR=""
OUTPUT=""
ALLOW_DIRTY=0

usage() {
  cat <<'EOF'
usage: deploy/collect-release-evidence.sh --tag vX.Y.Z [--evidence-dir path] [--output path] [--allow-dirty]

Reads existing JSON evidence files and writes release-evidence.json. This script
is read-only with respect to services: it does not deploy, restart, or mutate
remote hosts.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:?missing tag}"
      shift 2
      ;;
    --evidence-dir)
      EVIDENCE_DIR="${2:?missing evidence dir}"
      shift 2
      ;;
    --output)
      OUTPUT="${2:?missing output path}"
      shift 2
      ;;
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
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

if [[ -z "$TAG" ]]; then
  echo "--tag is required" >&2
  usage >&2
  exit 2
fi
if [[ -z "$EVIDENCE_DIR" ]]; then
  EVIDENCE_DIR="$REPO_ROOT/dist/deploy/$TAG"
fi
if [[ -z "$OUTPUT" ]]; then
  OUTPUT="$EVIDENCE_DIR/release-evidence.json"
fi

python3 - "$REPO_ROOT" "$TAG" "$EVIDENCE_DIR" "$OUTPUT" "$ALLOW_DIRTY" <<'PY'
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

repo = Path(sys.argv[1])
tag = sys.argv[2]
evidence_dir = Path(sys.argv[3])
output = Path(sys.argv[4])
allow_dirty = sys.argv[5] == "1"


def run(args: list[str]) -> tuple[int, str, str]:
    proc = subprocess.run(args, cwd=repo, text=True, capture_output=True, check=False)
    return proc.returncode, proc.stdout.strip(), proc.stderr.strip()


def load_json(name: str) -> Any:
    path = evidence_dir / name
    if not path.exists():
        return None
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        return {"_invalid": str(exc)}


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def deploy_plan_ok(payload: Any) -> bool:
    if not isinstance(payload, dict):
        return False
    if payload.get("ok") is not True or payload.get("apply") is not True:
        return False
    executed = payload.get("executed")
    if not isinstance(executed, list):
        return False
    required = {"preflight", "build_go", "build_harness", "build_gateway", "compose_up", "post_health", "smoke"}
    successful = {
        str(item.get("name"))
        for item in executed
        if isinstance(item, dict) and item.get("rc") == 0 and item.get("name")
    }
    return required.issubset(successful)


def preflight_ok(payload: Any) -> bool:
    if not isinstance(payload, dict) or payload.get("ok") is not True:
        return False
    version = payload.get("version")
    tag = payload.get("tag")
    expected_tag = payload.get("expected_tag")
    if not all(isinstance(item, str) and item for item in (version, tag, expected_tag)):
        return False
    if tag != expected_tag:
        return False
    checks = payload.get("checks")
    if not isinstance(checks, dict):
        return False
    runtime_payload = checks.get("runtime_payload")
    compose = checks.get("compose")
    return (
        checks.get("version_tag_match") is True
        and isinstance(runtime_payload, dict)
        and runtime_payload.get("ok") is True
        and checks.get("git_clean") is True
        and isinstance(compose, dict)
        and compose.get("valid") is True
    )


def smoke_ok(payload: Any) -> bool:
    if not isinstance(payload, dict) or payload.get("status") != "ok":
        return False
    gateway = payload.get("gateway_health")
    daemon = payload.get("daemon_health")
    control_health = payload.get("manager_control_health")
    control_runs = payload.get("manager_control_runs")
    role_results = payload.get("role_results")
    run_response = payload.get("run_response")
    run_id = payload.get("run_id")
    job_name = payload.get("job_name")
    return (
        isinstance(gateway, dict)
        and gateway.get("status") == "healthy"
        and isinstance(daemon, dict)
        and daemon.get("status") == "healthy"
        and isinstance(control_health, dict)
        and control_health.get("status") == "healthy"
        and isinstance(control_runs, dict)
        and isinstance(control_runs.get("runs"), list)
        and isinstance(role_results, list)
        and bool(role_results)
        and all(isinstance(item, dict) and item.get("ok") is True for item in role_results)
        and isinstance(run_response, dict)
        and isinstance(run_id, str)
        and bool(run_id)
        and run_response.get("id") == run_id
        and isinstance(job_name, str)
        and bool(job_name)
    )


def positive_int(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def valid_qft_run_refs(value: Any) -> bool:
    if not isinstance(value, list) or not value:
        return False
    for item in value:
        if not isinstance(item, dict):
            return False
        if positive_int(item.get("issue_number")) <= 0:
            return False
        if not isinstance(item.get("branch"), str) or not item.get("branch"):
            return False
        if not isinstance(item.get("commit_sha"), str) or not item.get("commit_sha"):
            return False
        if positive_int(item.get("pr_number")) <= 0:
            return False
        if not isinstance(item.get("run_id"), str) or not item.get("run_id"):
            return False
    return True


def qft_run_ref_key(item: dict[str, Any]) -> tuple[int, str, str, int, str]:
    return (
        positive_int(item.get("issue_number")),
        str(item.get("branch") or ""),
        str(item.get("commit_sha") or ""),
        positive_int(item.get("pr_number")),
        str(item.get("run_id") or ""),
    )


def qft_run_refs_cover(required: list[Any], actual: list[Any]) -> bool:
    actual_keys = {qft_run_ref_key(item) for item in actual if isinstance(item, dict)}
    return all(qft_run_ref_key(item) in actual_keys for item in required if isinstance(item, dict))


def qft_live_proof_ok(payload: Any) -> bool:
    if not isinstance(payload, dict) or payload.get("ok") is not True:
        return False
    checks = payload.get("checks")
    if not isinstance(checks, dict):
        return False
    required = [
        "session_aggregate",
        "seeder_state",
        "queue_bridge",
        "prd_processing",
        "run_records",
        "node_health",
        "cluster_utilization",
    ]
    for name in required:
        item = checks.get(name)
        if not isinstance(item, dict) or item.get("ok") is not True:
            return False
    queue_bridge = checks.get("queue_bridge")
    prd_processing = checks.get("prd_processing")
    run_records = checks.get("run_records")
    if not isinstance(queue_bridge, dict) or not isinstance(queue_bridge.get("bridged_issue_numbers"), list):
        return False
    if not queue_bridge.get("bridged_issue_numbers"):
        return False
    matched_run_refs = prd_processing.get("matched_run_refs") if isinstance(prd_processing, dict) else []
    if not valid_qft_run_refs(matched_run_refs):
        return False
    if not isinstance(run_records, dict):
        return False
    run_record_refs = run_records.get("matched_run_refs")
    if not valid_qft_run_refs(run_record_refs):
        return False
    if not qft_run_refs_cover(matched_run_refs, run_record_refs):
        return False
    if positive_int(run_records.get("bound_ref_count")) < len(matched_run_refs):
        return False
    if not valid_qft_run_record_identity(run_records.get("records"), matched_run_refs):
        return False
    cluster_utilization = checks.get("cluster_utilization")
    if cluster_utilization_failures(cluster_utilization):
        return False
    return (
        positive_int(run_records.get("bound_count")) > 0
        and positive_int(run_records.get("bound_ref_count")) > 0
        and positive_int(run_records.get("acceptable_count")) > 0
    )


def valid_qft_run_record_identity(records: Any, refs: Any) -> bool:
    if not isinstance(records, list) or not records or not isinstance(refs, list) or not refs:
        return False
    ref_keys = {
        (str(ref.get("run_id") or ""), str(ref.get("commit_sha") or ""))
        for ref in refs
        if isinstance(ref, dict)
    }
    for record in records:
        if not isinstance(record, dict):
            continue
        key = (str(record.get("run_id") or ""), str(record.get("commit_sha") or ""))
        if key not in ref_keys:
            continue
        artifact = record.get("artifact")
        kubernetes = record.get("kubernetes")
        ratchet = record.get("ratchet")
        if not isinstance(artifact, dict) or artifact.get("kind") != "proof-certificate" or not artifact.get("paths"):
            continue
        if not isinstance(kubernetes, dict):
            continue
        if not kubernetes.get("job_name") or not kubernetes.get("container_name"):
            continue
        if not kubernetes.get("log_ref") and not kubernetes.get("pod_name"):
            continue
        if not isinstance(ratchet, dict):
            continue
        if not ratchet.get("verdict") or str(ratchet.get("head_sha") or "") != key[1]:
            continue
        if ratchet.get("north_stars_regressed") is True:
            continue
        return True
    return False


def inference_fabric_proof_ok(payload: Any) -> bool:
    return not inference_fabric_proof_failures(payload)


def inference_fabric_proof_failures(payload: Any) -> list[str]:
    failures: list[str] = []
    if not isinstance(payload, dict):
        return ["payload.shape"]
    if payload.get("ok") is not True:
        failures.append("ok")
    for field in ("captured_at", "git_sha"):
        if not isinstance(payload.get(field), str) or not payload.get(field):
            failures.append(field)
    if not isinstance(payload.get("gateway_urls"), dict) or not payload.get("gateway_urls"):
        failures.append("gateway_urls")
    completion_proofs = payload.get("completion_proofs")
    if not isinstance(completion_proofs, list) or not completion_proofs:
        failures.append("completion_proofs")
        completion_proofs = []
    roles: dict[str, list[dict[str, Any]]] = {"general": [], "judge": [], "coding": []}
    trace_correlation_ok = True
    no_cloud = True
    for index, proof in enumerate(completion_proofs):
        if not isinstance(proof, dict):
            failures.append(f"completion_proofs.{index}.shape")
            trace_correlation_ok = False
            continue
        role = str(proof.get("role") or proof.get("requested_model") or "")
        if role in roles:
            roles[role].append(proof)
        response = proof.get("response_metadata")
        trace = proof.get("trace_event")
        if not isinstance(response, dict):
            failures.append(f"completion_proofs.{index}.response_metadata")
            trace_correlation_ok = False
            response = {}
        if not isinstance(trace, dict):
            failures.append(f"completion_proofs.{index}.trace_event")
            trace_correlation_ok = False
            trace = {}
        for field in (
            "trace_id",
            "request_id",
            "requested_model",
            "resolved_model_id",
            "backend_api_base",
            "backend_node_id",
            "served_model",
        ):
            if not isinstance(response.get(field), str) or not response.get(field):
                failures.append(f"completion_proofs.{index}.response_metadata.{field}")
                trace_correlation_ok = False
            if not isinstance(trace.get(field), str) or not trace.get(field):
                failures.append(f"completion_proofs.{index}.trace_event.{field}")
                trace_correlation_ok = False
            elif response.get(field) and trace.get(field) != response.get(field):
                failures.append(f"completion_proofs.{index}.trace_event.{field}.mismatch")
                trace_correlation_ok = False
        if response.get("response_status") != "success" or trace.get("response_status") != "success":
            failures.append(f"completion_proofs.{index}.response_status")
            trace_correlation_ok = False
        if response.get("http_status") not in (200, "200") or trace.get("http_status") not in (200, "200"):
            failures.append(f"completion_proofs.{index}.http_status")
            trace_correlation_ok = False
        if response.get("cloud") is not False or trace.get("cloud") is not False:
            no_cloud = False
        if response.get("allow_cloud") is not False or trace.get("allow_cloud") is not False:
            no_cloud = False
    if not no_cloud:
        failures.append("coverage.no_cloud")
    if not role_has_local_llama_cpu_ram(roles["general"]):
        failures.append("coverage.general.local_llama_cpu_ram")
    if not role_has_local_llama_cpu_ram(roles["judge"]):
        failures.append("coverage.judge.local_llama_cpu_ram")
    coding_models = {
        str((proof.get("response_metadata") or {}).get("resolved_model_id") or "")
        for proof in roles["coding"]
        if isinstance(proof.get("response_metadata"), dict)
    }
    if "qwen-coder-s001" not in coding_models:
        failures.append("coverage.coding.qwen_coder_s001")
    if "qwen-coder-s002" not in coding_models:
        failures.append("coverage.coding.qwen_coder_s002")
    if not trace_correlation_ok:
        failures.append("coverage.trace_response_correlation")
    sampler_failures = sampler_correlation_failures(payload.get("sampler_correlation"))
    if sampler_failures:
        failures.append("coverage.sampler_window_correlation")
        failures.extend(f"sampler_correlation.{item}" for item in sampler_failures)
    co_tenant = payload.get("co_tenant_non_disruption")
    if not isinstance(co_tenant, dict):
        failures.append("co_tenant_non_disruption")
    else:
        if co_tenant.get("disruption_observed") is not False:
            failures.append("co_tenant_non_disruption.disruption_observed")
        if co_tenant.get("evidence_collected") is not True:
            failures.append("co_tenant_non_disruption.evidence_collected")
    return sorted(set(failures))


def role_has_local_llama_cpu_ram(proofs: list[dict[str, Any]]) -> bool:
    for proof in proofs:
        response = proof.get("response_metadata")
        trace = proof.get("trace_event")
        if not isinstance(response, dict) or not isinstance(trace, dict):
            continue
        text = " ".join(
            str(value or "").lower()
            for value in (
                response.get("resolved_model_id"),
                response.get("provider"),
                response.get("backend_type"),
                response.get("backend_api_base"),
                response.get("served_model"),
                response.get("resource_class"),
                trace.get("provider"),
                trace.get("backend_type"),
                trace.get("resource_class"),
            )
        )
        if "llama" in text and "cpu" in text and ("ram" in text or "memory" in text):
            if response.get("cloud") is False and trace.get("cloud") is False:
                return True
    return False


def sampler_correlation_failures(value: Any) -> list[str]:
    failures: list[str] = []
    if not isinstance(value, dict):
        return ["shape"]
    if value.get("ok") is not True:
        failures.append("ok")
    if value.get("window_bound") is not True:
        failures.append("window_bound")
    samples = value.get("samples")
    if not isinstance(samples, list) or not samples:
        return [*failures, "samples"]
    for node in ("s001", "s002"):
        node_samples = [sample for sample in samples if isinstance(sample, dict) and sample.get("node_id") == node]
        if not node_samples:
            failures.append(f"{node}.samples")
            continue
        if not any(positive_float(sample.get("cpu_percent")) > 0 for sample in node_samples):
            failures.append(f"{node}.cpu")
        if not any(positive_float(sample.get("rss_bytes") or sample.get("ram_bytes")) > 0 for sample in node_samples):
            failures.append(f"{node}.ram")
        if not any(positive_float(sample.get("gpu_util_percent")) > 0 or positive_float(sample.get("gpu_memory_bytes")) > 0 for sample in node_samples):
            failures.append(f"{node}.gpu")
    request_ids = value.get("request_ids")
    if not isinstance(request_ids, list) or not request_ids:
        failures.append("request_ids")
    return failures


def positive_float(value: Any) -> float:
    try:
        return float(value or 0)
    except (TypeError, ValueError):
        return 0.0


def cluster_utilization_failures(value: Any) -> list[str]:
    if not isinstance(value, dict):
        return ["missing"]
    if value.get("ok") is not True:
        return ["ok"]
    failures = value.get("failures")
    if isinstance(failures, list) and failures:
        return [str(item) for item in failures]
    collected: list[str] = []
    for field in ("node_count", "agent_job_count", "utilization_sample_count"):
        if positive_int(value.get(field)) < 2:
            collected.append(field)
    disk_pressure = value.get("disk_pressure_node_ids")
    if not isinstance(disk_pressure, list) or disk_pressure:
        collected.append("disk_pressure_node_ids")
    if value.get("co_tenant_disruption") is not False:
        collected.append("co_tenant_disruption")
    for field in (
        "node_ids",
        "usable_node_ids",
        "scheduled_node_ids",
        "resourceful_scheduled_node_ids",
        "utilization_sample_node_ids",
        "gpu_resident_node_ids",
    ):
        ids = value.get(field)
        if not isinstance(ids, list) or len({str(item) for item in ids if item}) < 2:
            collected.append(field)
    queue = value.get("queue_backpressure")
    if not isinstance(queue, dict):
        collected.append("queue_backpressure")
        return collected
    if positive_int(queue.get("max_concurrent_runs")) < 2:
        collected.append("queue_backpressure.max_concurrent_runs")
    if queue.get("backpressure_observed") is not True:
        collected.append("queue_backpressure.backpressure_observed")
    if queue.get("false_green_blocked") is not True:
        collected.append("queue_backpressure.false_green_blocked")
    return collected


def post_health_ok(payload: Any) -> bool:
    if not isinstance(payload, dict) or payload.get("ok") is not True:
        return False
    checks = payload.get("checks")
    health = checks.get("health") if isinstance(checks, dict) else None
    if not isinstance(health, dict) or not health:
        return False
    return all(
        isinstance(item, dict)
        and item.get("ok") is True
        and item.get("version_matches_tag") is True
        for item in health.values()
    )


def rollback_ok(payload: Any) -> bool:
    return (
        isinstance(payload, dict)
        and bool(payload.get("tag"))
        and isinstance(payload.get("git"), dict)
        and isinstance(payload.get("health"), (dict, list))
    )


def tui_control_proof_ok(payload: Any) -> bool:
    if not isinstance(payload, dict) or payload.get("ok") is not True or payload.get("schema_version") != 2:
        return False
    roots = payload.get("roots")
    cwd = payload.get("cwd")
    if not isinstance(roots, dict) or not isinstance(cwd, str) or not cwd.startswith("/") or cwd == roots.get("repo_root"):
        return False
    invocation = payload.get("invocation")
    if not isinstance(invocation, dict):
        return False
    command = invocation.get("command")
    if not isinstance(command, str) or "--control" not in command or "--control-proof-json" not in command:
        return False
    argv = invocation.get("argv")
    if not isinstance(argv, list) or "--control-proof-json" not in [str(item) for item in argv]:
        return False
    if not isinstance(invocation.get("exec_path"), str) or not invocation.get("exec_path"):
        return False
    if not isinstance(invocation.get("entrypoint"), str) or not invocation.get("entrypoint"):
        return False
    if invocation.get("mode") != "production-control" or invocation.get("real_tui_entrypoint") is not True or invocation.get("arbitrary_cwd") is not True:
        return False
    for field in ("repo_root", "runtime_root", "payload_root", "workspace_root"):
        if not isinstance(roots.get(field), str) or not roots.get(field):
            return False
    if roots.get("nested_runtime") is not False:
        return False
    runtime_root = roots.get("runtime_root")
    if isinstance(runtime_root, str) and runtime_root.endswith("/.agents/.agents"):
        return False
    loaded = payload.get("loaded")
    if not isinstance(loaded, dict):
        return False
    if positive_int(loaded.get("skills_count")) < 1 or positive_int(loaded.get("commands_count")) < 1:
        return False
    if not valid_named_loader_entries(loaded.get("skills"), "id", require_source_path=True):
        return False
    if not valid_named_loader_entries(loaded.get("commands"), "name", require_source_path=False):
        return False
    if not isinstance(loaded.get("plugins_count"), int) or loaded.get("plugins_count") < 0:
        return False
    if "plugins" not in loaded or not isinstance(loaded.get("plugins"), list):
        return False
    if not isinstance(loaded.get("sessions_count"), int) or loaded.get("sessions_count") < 0:
        return False
    capabilities = payload.get("capabilities")
    if not isinstance(capabilities, dict):
        return False
    for field in ("refresh", "cancel", "touch", "logs", "follow_up"):
        if capabilities.get(field) is not True:
            return False
    if not isinstance(capabilities.get("pause_hold_supported"), bool):
        return False
    watched = payload.get("watched")
    if not isinstance(watched, dict) or watched.get("production_runs_listed") is not True:
        return False
    if not isinstance(watched.get("poll_count"), int) or watched.get("poll_count") < 2:
        return False
    if not isinstance(watched.get("poll_elapsed_ms"), int) or watched.get("poll_elapsed_ms") < 500:
        return False
    observed_run_ids = watched.get("observed_run_ids")
    observed_set = {str(item) for item in observed_run_ids if item} if isinstance(observed_run_ids, list) else set()
    if len(observed_set) < 2:
        return False
    if not isinstance(watched.get("poll_started_at"), str) or not isinstance(watched.get("poll_finished_at"), str):
        return False
    if not valid_tui_run_snapshots(watched.get("run_snapshots")):
        return False
    if not isinstance(watched.get("status_transitions"), list) or not watched.get("status_transitions"):
        return False
    actions = payload.get("actions")
    if not isinstance(actions, dict):
        return False
    for field in ("created_qft_task_id", "follow_up_task_id"):
        value = actions.get(field)
        if not isinstance(value, str) or not value.startswith("qft-task-"):
            return False
    if actions.get("created_qft_task_id") == actions.get("follow_up_task_id"):
        return False
    for field in ("cancelled_run_id", "touched_run_id", "logs_run_id"):
        value = actions.get(field)
        if not isinstance(value, str) or not value or value not in observed_set:
            return False
    if not valid_tui_control_calls(payload.get("control_calls")):
        return False
    evidence_paths = payload.get("evidence_paths")
    if not isinstance(evidence_paths, list) or not evidence_paths or not all(isinstance(item, str) and item for item in evidence_paths):
        return False
    binders = set(observed_set)
    binders.add(str(actions.get("created_qft_task_id")))
    binders.add(str(actions.get("follow_up_task_id")))
    return evidence_paths_bound_to_tokens(evidence_paths, binders)


def valid_tui_control_calls(calls: Any) -> bool:
    if not isinstance(calls, list):
        return False
    required = {
        "health": ("GET", "/v1/control/health"),
        "list-runs-initial": ("GET", "/v1/control/runs"),
        "list-runs-refresh": ("GET", "/v1/control/runs"),
        "list-runs-post-action": ("GET", "/v1/control/runs"),
        "logs": ("GET", "/v1/control/runs/"),
        "touch": ("POST", "/v1/control/runs/"),
        "cancel": ("POST", "/v1/control/runs/"),
        "create-qft-task": ("POST", "/v1/control/qft/tasks"),
        "create-follow-up": ("POST", "/v1/control/qft/tasks"),
    }
    seen = set()
    for call in calls:
        if not isinstance(call, dict) or call.get("ok") is not True:
            return False
        purpose = call.get("purpose")
        if not isinstance(purpose, str) or purpose not in required:
            continue
        method, path = required[purpose]
        actual_path = call.get("path")
        if call.get("method") != method or not isinstance(actual_path, str) or not actual_path.startswith(path):
            return False
        seen.add(purpose)
    return set(required).issubset(seen)


def valid_named_loader_entries(entries: Any, key: str, *, require_source_path: bool) -> bool:
    if not isinstance(entries, list) or not entries:
        return False
    for entry in entries:
        if not isinstance(entry, dict):
            return False
        if not isinstance(entry.get(key), str) or not entry.get(key):
            return False
        if require_source_path and (not isinstance(entry.get("source_path"), str) or not entry.get("source_path")):
            return False
    return True


def valid_tui_run_snapshots(snapshots: Any) -> bool:
    if not isinstance(snapshots, list) or len(snapshots) < 2:
        return False
    first = None
    changed = False
    for snapshot in snapshots:
        if not isinstance(snapshot, list) or not snapshot:
            return False
        normalized = []
        for run in snapshot:
            if not isinstance(run, dict) or not isinstance(run.get("run_id"), str) or not run.get("run_id"):
                return False
            normalized.append((run.get("run_id"), run.get("status"), run.get("updated_at"), run.get("evidence_path")))
        if first is None:
            first = normalized
        elif normalized != first:
            changed = True
    return changed


def evidence_paths_bound_to_tokens(paths: list[str], tokens: set[str]) -> bool:
    return all(any(path_binds_to_token(path, token) for token in tokens) for path in paths)


def path_binds_to_token(path: str, token: str) -> bool:
    if not token:
        return False
    for component in re.split(r"[\\/]+", path):
        if re.match(rf"^{re.escape(token)}($|[._-])", component):
            return True
    return False


rc, head, _ = run(["git", "rev-parse", "HEAD"])
rc_tag, tag_commit, tag_err = run(["git", "rev-list", "-n", "1", tag])
rc_status, status, _ = run(["git", "status", "--porcelain"])

files = sorted(path for path in evidence_dir.glob("*.json") if path.resolve() != output.resolve())
artifacts = [
    {
        "name": path.name,
        "path": str(path),
        "sha256": sha256(path),
        "bytes": path.stat().st_size,
    }
    for path in files
]

deploy = load_json("deploy-release.json")
preflight = load_json("preflight.json")
post_health = load_json("post-health.json")
rollback_before = load_json("rollback-before.json") or load_json("rollback-evidence.json")
smoke = load_json("smoke.json")
qft_live_proof = load_json("qft-live-proof.json")
inference_fabric_proof = load_json("inference-fabric-release-proof.json")
tui_control_proof = load_json("tui-control-proof.json")

health = {}
if isinstance(post_health, dict):
    health = ((post_health.get("checks") or {}).get("health") or {})
ha_nodes = []
if isinstance(health, dict):
    for name, item in sorted(health.items()):
        if isinstance(item, dict):
            ha_nodes.append({
                "name": name,
                "service": item.get("service"),
                "url": item.get("url"),
                "ok": bool(item.get("ok")),
                "status": item.get("status"),
                "version": item.get("version"),
                "git_sha": item.get("git_sha"),
                "image_tag": item.get("image_tag"),
                "node_id": item.get("node_id"),
                "leader": item.get("leader"),
            })
healthy_node_ids = sorted({
    str(node.get("node_id"))
    for node in ha_nodes
    if node.get("ok") and node.get("node_id")
})

checks = {
    "tag_resolves": rc_tag == 0,
    "head_available": rc == 0,
    "git_clean": (rc_status == 0 and status == "") or allow_dirty,
    "deploy_plan_ok": deploy_plan_ok(deploy),
    "preflight_ok": preflight_ok(preflight),
    "post_health_ok": post_health_ok(post_health),
    "smoke_ok": smoke_ok(smoke),
    "inference_fabric_proof_ok": inference_fabric_proof_ok(inference_fabric_proof),
    "qft_live_proof_ok": qft_live_proof_ok(qft_live_proof),
    "tui_control_proof_ok": tui_control_proof_ok(tui_control_proof),
    "rollback_evidence_present": rollback_ok(rollback_before),
    "ha_identity_present": len(healthy_node_ids) >= 2,
}

report = {
    "tag": tag,
    "tag_commit": tag_commit if rc_tag == 0 else None,
    "tag_error": tag_err if rc_tag != 0 else None,
    "git_head": head if rc == 0 else None,
    "dirty": bool(status),
    "evidence_dir": str(evidence_dir),
    "artifacts": artifacts,
    "checks": checks,
    "ha_topology": {
        "nodes": ha_nodes,
        "healthy_node_ids": healthy_node_ids,
        "leader_count": sum(1 for node in ha_nodes if node.get("leader") is True),
    },
    "inputs": {
        "deploy_release": deploy,
        "preflight": preflight,
        "post_health": post_health,
        "rollback_before": rollback_before,
        "smoke": smoke,
        "inference_fabric_proof": inference_fabric_proof,
        "qft_live_proof": qft_live_proof,
        "tui_control_proof": tui_control_proof,
    },
}
report["ok"] = all(checks.values())

output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
print(json.dumps(report, indent=2, sort_keys=True))
raise SystemExit(0 if report["ok"] else 1)
PY

