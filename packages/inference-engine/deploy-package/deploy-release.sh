#!/usr/bin/env bash
# Release-safe deploy entrypoint. Dry-run by default; --apply is the only mutating mode.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEPLOY_DIR/../.." && pwd)"
TAG=""
APPLY=0
ALLOW_DIRTY=0
EVIDENCE=""
REPOSITORY="${GITHUB_REPOSITORY:-marius-patrik/agents}"

usage() {
  cat <<'EOF'
usage: deploy/deploy-release.sh --tag vX.Y.Z [--repo owner/name] [--evidence path] [--allow-dirty] [--apply]

Plans or applies a deterministic agents release deploy. The default is read-only:
it validates the tag, records exact commands, and writes evidence JSON. Mutating
Docker build/compose actions run only with --apply.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:?missing tag}"
      shift 2
      ;;
    --repo)
      REPOSITORY="${2:?missing repo}"
      shift 2
      ;;
    --evidence)
      EVIDENCE="${2:?missing evidence path}"
      shift 2
      ;;
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --apply)
      APPLY=1
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

VERSION="${TAG#v}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if [[ -z "$EVIDENCE" ]]; then
  EVIDENCE="$REPO_ROOT/dist/deploy/$TAG/deploy-release.json"
fi

run_json_plan() {
  python3 - "$REPO_ROOT" "$TAG" "$VERSION" "$REPOSITORY" "$APPLY" "$ALLOW_DIRTY" "$BUILD_TIME" "$EVIDENCE" <<'PY'
import json
import os
import shlex
import socket
import subprocess
import sys
from pathlib import Path

repo = Path(sys.argv[1])
tag = sys.argv[2]
version = sys.argv[3]
repository = sys.argv[4]
apply = sys.argv[5] == "1"
allow_dirty = sys.argv[6] == "1"
build_time = sys.argv[7]
evidence = Path(sys.argv[8])

def run(args):
    try:
        p = subprocess.run(args, cwd=repo, text=True, capture_output=True, check=False)
        return p.returncode, p.stdout.strip(), p.stderr.strip()
    except FileNotFoundError as exc:
        return 127, "", str(exc)


def load_json(path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        return {"ok": False, "error": str(exc), "path": str(path)}


def configured_compose_cmd():
    configured = os.environ.get("AGENTS_COMPOSE_CMD", "").strip()
    if configured:
        return [shlex.split(configured)]
    return [["docker", "compose"], ["docker-compose"]]


def detect_compose_cmd():
    compose_file = str(repo / "packages" / "deploy" / "docker-compose.cluster.yml")
    attempts = []
    candidates = configured_compose_cmd()
    for candidate in candidates:
        rc, stdout, stderr = run([*candidate, "-f", compose_file, "config"])
        attempts.append({
            "command": candidate,
            "rc": rc,
            "stdout": stdout,
            "stderr": stderr,
        })
        if rc == 0:
            return candidate, attempts
    return None, attempts


def docker_network_gateway(name: str) -> str:
    rc, stdout, _ = run([
        "docker",
        "network",
        "inspect",
        name,
        "--format",
        "{{range .IPAM.Config}}{{.Gateway}}{{end}}",
    ])
    if rc == 0:
        gateway = stdout.strip().split()
        if gateway:
            return gateway[0]
    return "host-gateway"


def smoke_ok(payload):
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


rc, head, err = run(["git", "rev-parse", "HEAD"])
if rc != 0:
    raise SystemExit(err or "git rev-parse failed")
rc, tag_sha, err = run(["git", "rev-list", "-n", "1", tag])
if rc != 0:
    rc, tag_sha, err = run(["git", "rev-parse", "--verify", tag + "^{commit}"])
if rc != 0:
    raise SystemExit(err or f"release ref not found: {tag}")
rc, status, _ = run(["git", "status", "--porcelain", "--untracked-files=all"])
status_lines = [line for line in status.splitlines() if line.strip()]
deploy_evidence_prefix = f"dist/deploy/{tag}/"
release_artifact_prefix = f"dist/release/{tag}/"

def is_controlled_deploy_evidence(line: str) -> bool:
    if not line.startswith("?? "):
        return False
    path = line[3:]
    return (
        path.startswith(deploy_evidence_prefix)
        or path.startswith(release_artifact_prefix)
    )

unreviewed_status = [line for line in status_lines if not is_controlled_deploy_evidence(line)]
dirty = bool(unreviewed_status)
if dirty and not allow_dirty:
    details = "\n".join(unreviewed_status)
    raise SystemExit(f"working tree has unreviewed local changes:\n{details}\nuse --allow-dirty only for an explicitly reviewed deploy")

images = {
    "go": f"agents-go-services:{tag}",
    "harness": f"agents/harness:{tag}",
    "gateway": f"agents-gateway:{tag}",
}
compose_profiles = os.environ.get("AGENTS_COMPOSE_PROFILES", "")
def detected_node_id() -> str:
    candidates = [
        os.environ.get("NODE_ID", ""),
        os.environ.get("AGENTS_NODE_ID", ""),
        os.environ.get("RUNNER_NAME", ""),
        os.environ.get("HOSTNAME", ""),
        socket.gethostname(),
    ]
    lowered = " ".join(item.lower() for item in candidates if item)
    if "s001" in lowered:
        return "s001"
    if "s002" in lowered:
        return "s002"
    return ""

local_node_id = detected_node_id()
local_model_host = os.environ.get("AGENTS_LOCAL_MODEL_HOST", "").strip() or docker_network_gateway("deploy_default")
local_model_url_host = "host.docker.internal" if local_model_host == "host-gateway" else local_model_host
local_model_endpoint_env: list[str] = []
if local_node_id == "s001":
    local_model_endpoint_env = [
        f"AGENTS_REASONER_S001_API_BASE=http://{local_model_url_host}:8082/v1",
        f"AGENTS_JUDGE_S001_API_BASE=http://{local_model_url_host}:8082/v1",
        f"AGENTS_CODER_S001_API_BASE=http://{local_model_url_host}:8001/v1",
    ]
elif local_node_id == "s002":
    local_model_endpoint_env = [
        f"AGENTS_REASONER_S002_API_BASE=http://{local_model_url_host}:8082/v1",
        f"AGENTS_JUDGE_S002_API_BASE=http://{local_model_url_host}:8082/v1",
        f"AGENTS_CODER_S002_API_BASE=http://{local_model_url_host}:8001/v1",
    ]
# NATS HA cluster routes/advertise must use LAN IPs. The bare hostnames s001/s002
# resolve to Tailscale addresses (100.x) that are not reachable on the cluster
# port 6222, which prevents JetStream from forming quorum. Map node -> LAN IP
# (overridable via AGENTS_NODE_LAN_IPS) and build routes/advertise from it unless
# the operator already set them explicitly.
node_lan_ips: dict[str, str] = {}
for _pair in os.environ.get("AGENTS_NODE_LAN_IPS", "s001=192.168.0.29,s002=192.168.0.48").split(","):
    if "=" in _pair:
        _k, _, _v = _pair.partition("=")
        if _k.strip() and _v.strip():
            node_lan_ips[_k.strip()] = _v.strip()
nats_cluster_routes = os.environ.get("AGENTS_NATS_CLUSTER_ROUTES", "").strip() or ",".join(
    f"nats://{ip}:6222" for _, ip in sorted(node_lan_ips.items())
)
nats_cluster_advertise = os.environ.get("AGENTS_NATS_CLUSTER_ADVERTISE", "").strip() or (
    f"{node_lan_ips[local_node_id]}:6222" if local_node_id in node_lan_ips else ""
)

# Manager -> daemon endpoints. Manager and daemon leadership are independent NATS
# elections and can land on different nodes. A manager only submits runs to the
# daemon LEADER, so every manager must know ALL daemons' cluster-routable
# endpoints (node LAN IP : host daemon port) — not just its co-located daemon —
# or a leadership split yields "no leader among daemon endpoints". Build the list
# from the node->LAN-IP map unless the operator set it explicitly.
manager_daemon_host_port = os.environ.get("AGENTS_DAEMON_HOST_PORT", "18080").strip() or "18080"
# The local daemon is reached over the compose network (service name "daemon"),
# which is always routable from the co-located manager. Peer daemons are reached
# over the LAN by node IP : host daemon port. Deliberately do NOT use the LOCAL
# node's own LAN IP here: a container reaching its host's external IP is not
# reliably routable (e.g. s001's secondary NIC hijacks the 192.168.0.0/24 route),
# whereas the compose service name always resolves to the local daemon.
peer_daemon_urls = [
    f"http://{ip}:{manager_daemon_host_port}"
    for node, ip in sorted(node_lan_ips.items())
    if node != local_node_id
]
manager_daemon_urls = os.environ.get("AGENTS_MANAGER_DAEMON_URL", "").strip() or ",".join(
    ["http://daemon:8080"] + peer_daemon_urls
)
# Gateway endpoint injected into dispatched RUN pods. Run pods execute as k8s
# Jobs and cannot resolve the docker-compose service name (http://gateway:4000);
# they must reach the gateway via a cluster-routable address. Use the local
# node's LAN IP (all gateways are all-active/stateless over the shared backend
# pool, and the local node's gateway is co-located with this deploy leg).
gateway_host_port = os.environ.get("AGENTS_GATEWAY_HOST_PORT", "4000").strip() or "4000"
manager_run_gateway_url = os.environ.get("AGENTS_MANAGER_RUN_GATEWAY_URL", "").strip() or (
    f"http://{node_lan_ips[local_node_id]}:{gateway_host_port}" if local_node_id in node_lan_ips else ""
)

preflight = [
    "deploy/preflight.sh",
    "--repo", repository,
    "--tag", tag,
    "--no-github",
    # The runtime root is now a git checkout (tracks origin/live), so it
    # legitimately carries a .git. Pass --allow-git-root so the runtime
    # payload check does not trip runtime_root_is_git_checkout.
    "--allow-git-root",
    "--output", str(repo / "dist" / "deploy" / tag / "preflight.json"),
]
build_args = [
    "--build-arg", f"AGENTS_VERSION={version}",
    "--build-arg", f"AGENTS_GIT_SHA={tag_sha}",
    "--build-arg", f"AGENTS_IMAGE_TAG={{image}}",
    "--build-arg", f"AGENTS_BUILD_TIME={build_time}",
]
compose_cmd, compose_detection = detect_compose_cmd()
planned_compose_cmd = compose_cmd or configured_compose_cmd()[0]
commands = {
    "sync_runtime_payload": [
        "env",
        "AGENTS_LIVE_RECONCILE_WRITE=1",
        f"AGENTS_REPO={str(repo)}",
        f"AGENTS_ROOT={os.environ.get('AGENTS_ROOT', '/home/patrik/agents')}",
        ".data/hooks/sync-root.sh",
        "--from-repo",
    ],
    "preflight": preflight,
    "prune_build_cache": [
        "bash",
        "-lc",
        "docker system df && docker builder prune -af && docker system df",
    ],
    "build_go": ["docker", "build", "-f", "deploy/Dockerfile.daemon", "-t", images["go"], *[part.replace("{image}", images["go"]) for part in build_args], "engine/go"],
    "build_harness": ["docker", "build", "-f", "deploy/Dockerfile.harness", "-t", images["harness"], *[part.replace("{image}", images["harness"]) for part in build_args], "."],
    "build_gateway": ["docker", "build", "-t", images["gateway"], *[part.replace("{image}", images["gateway"]) for part in build_args], "gateway"],
    # Make the freshly-built harness image available to k3s. The daemon dispatches
    # agent runs (and the QFT loop) as k8s Jobs that pull from containerd's k8s.io
    # namespace, NOT from docker's image store — so a docker-built image is invisible
    # to kubelet (ImagePullBackOff, since the image is not in any registry). Import it
    # into containerd and PIN it (io.cri-containerd.pinned=pinned) so kubelet image GC
    # under disk pressure does not evict the platform image. Skips cleanly in contexts
    # without k3s (e.g. a containerized CI runner) so compose-only deploys still pass.
    "import_harness_image": [
        "bash",
        "-lc",
        (
            'set -uo pipefail; IMG="' + images["harness"] + '"; '
            'if ! sudo -n k3s --version >/dev/null 2>&1; then '
            'echo "[import_harness_image] k3s unavailable; skipping containerd import"; exit 0; fi; '
            'echo "[import_harness_image] importing $IMG into containerd k8s.io"; '
            'docker save "$IMG" | sudo -n k3s ctr -n k8s.io images import - || exit 1; '
            'sudo -n k3s ctr -n k8s.io images tag "docker.io/$IMG" docker.io/agents/harness:latest 2>/dev/null || true; '
            'sudo -n k3s ctr -n k8s.io images label "docker.io/$IMG" io.cri-containerd.pinned=pinned || exit 1; '
            'sudo -n k3s ctr -n k8s.io images label docker.io/agents/harness:latest io.cri-containerd.pinned=pinned 2>/dev/null || true; '
            'echo "[import_harness_image] imported + pinned $IMG"'
        ),
    ],
    "ensure_inference_network": ["docker", "network", "create", "agents-inference"],
    # Include agents-orchestrator: it is the legacy container name for the manager
    # service. Upgrading from a release that used that name leaves it bound to the
    # manager port (18081); removing it (plus --remove-orphans below) prevents a
    # "port is already allocated" failure when agents-manager starts.
    "remove_replaced_agents": ["docker", "rm", "-f", "agents-gateway", "agents-daemon", "agents-manager", "agents-orchestrator"],
    "compose_up": [
        "env",
        f"COMPOSE_PROFILES={compose_profiles}",
        f"AGENTS_VERSION={version}",
        f"AGENTS_GIT_SHA={tag_sha}",
        f"AGENTS_BUILD_TIME={build_time}",
        f"AGENTS_GO_IMAGE={images['go']}",
        f"AGENTS_GATEWAY_IMAGE={images['gateway']}",
        f"AGENTS_HARNESS_IMAGE={images['harness']}",
        f"AGENTS_ROOT={os.environ.get('AGENTS_ROOT', '/home/patrik/agents')}",
        f"AGENTS_REPO={str(repo)}",
        f"NODE_ID={local_node_id}",
        f"AGENTS_NODE_ID={local_node_id}",
        f"AGENTS_DOCKER_HOST_GATEWAY={local_model_host}",
        f"AGENTS_DAEMON_DATA_DIR={os.environ.get('AGENTS_DAEMON_DATA_DIR', './data/daemon')}",
        f"AGENTS_MANAGER_DATA_DIR={os.environ.get('AGENTS_MANAGER_DATA_DIR', './data/manager')}",
        f"AGENTS_KUBECONFIG_HOST_PATH={os.environ.get('AGENTS_KUBECONFIG_HOST_PATH', '/home/patrik/k3s-client.yaml')}",
        f"AGENTS_NATS_CLUSTER_ROUTES={nats_cluster_routes}",
        *([f"AGENTS_NATS_CLUSTER_ADVERTISE={nats_cluster_advertise}"] if nats_cluster_advertise else []),
        *([f"AGENTS_MANAGER_DAEMON_URL={manager_daemon_urls}"] if manager_daemon_urls else []),
        *([f"AGENTS_MANAGER_RUN_GATEWAY_URL={manager_run_gateway_url}"] if manager_run_gateway_url else []),
        *local_model_endpoint_env,
        *planned_compose_cmd, "-f", "deploy/docker-compose.cluster.yml", "up", "-d", "--remove-orphans", "--wait", "--wait-timeout", "180",
    ],
    "post_health": ["deploy/preflight.sh", "--repo", repository, "--tag", tag, "--health", "--no-github", "--output", str(repo / "dist" / "deploy" / tag / "post-health.json")],
    "smoke": ["deploy/smoke.sh", "--output", str(repo / "dist" / "deploy" / tag / "smoke.json")],
}
report = {
    "tag": tag,
    "version": version,
    "repository": repository,
    "apply": apply,
    "git_head": head,
    "tag_commit": tag_sha,
    "dirty": dirty,
    "controlled_release_output_status": [
        line for line in status_lines if is_controlled_deploy_evidence(line)
    ],
    "unreviewed_status": unreviewed_status,
    "build_time": build_time,
    "images": images,
    "commands": commands,
    "compose_detection": compose_detection,
    "compose_command": planned_compose_cmd,
    "detected_node_id": local_node_id,
    "local_model_host": local_model_host,
    "local_model_url_host": local_model_url_host,
    "local_model_endpoint_env": local_model_endpoint_env,
    "executed": [],
}

if apply:
    if compose_cmd is None:
        report["ok"] = False
        report["error"] = "no usable Docker Compose command found; set AGENTS_COMPOSE_CMD or install docker compose/docker-compose"
        evidence.parent.mkdir(parents=True, exist_ok=True)
        evidence.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(f"deploy-release: {report['error']}", file=sys.stderr, flush=True)
        raise SystemExit(125)
    for name in ["sync_runtime_payload", "preflight", "prune_build_cache", "build_go", "build_harness", "build_gateway", "import_harness_image", "ensure_inference_network", "remove_replaced_agents", "compose_up", "post_health", "smoke"]:
        print(f"deploy-release: running {name}", flush=True)
        rc, stdout, stderr = run(commands[name])
        if name == "ensure_inference_network" and rc != 0 and "already exists" in (stderr + stdout):
            rc = 0
        if name == "remove_replaced_agents" and rc != 0 and "No such container" in (stderr + stdout):
            rc = 0
        report["executed"].append({"name": name, "rc": rc, "stdout": stdout, "stderr": stderr})
        if rc != 0:
            report["ok"] = False
            evidence.parent.mkdir(parents=True, exist_ok=True)
            evidence.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
            print(f"deploy-release: {name} failed with rc={rc}", file=sys.stderr, flush=True)
            if stderr:
                print(stderr, file=sys.stderr, flush=True)
            if stdout:
                print(stdout, flush=True)
            raise SystemExit(rc)
    evidence_dir = repo / "dist" / "deploy" / tag
    post_health = load_json(evidence_dir / "post-health.json")
    smoke = load_json(evidence_dir / "smoke.json")
    report["evidence_checks"] = {
        "post_health_ok": isinstance(post_health, dict) and post_health.get("ok") is True,
        "smoke_ok": smoke_ok(smoke),
    }
    if not all(report["evidence_checks"].values()):
        report["ok"] = False
        evidence.parent.mkdir(parents=True, exist_ok=True)
        evidence.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        raise SystemExit(1)

report["ok"] = True
evidence.parent.mkdir(parents=True, exist_ok=True)
evidence.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
print(json.dumps(report, indent=2, sort_keys=True))
PY
}

run_json_plan

