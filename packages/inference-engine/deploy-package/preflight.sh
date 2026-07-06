#!/usr/bin/env bash
# Non-invasive release/deploy preflight. Reports state; never restarts services.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEPLOY_DIR/../.." && pwd)"
VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"
TAG="v$VERSION"
OUTPUT=""
CHECK_HEALTH=0
CHECK_GITHUB=1
ALLOW_DIRTY=0
ALLOW_GIT_ROOT=0
REPOSITORY="${GITHUB_REPOSITORY:-}"

usage() {
  cat <<'EOF'
usage: deploy/preflight.sh [--tag vX.Y.Z] [--repo owner/name] [--output path] [--health] [--no-github] [--allow-dirty] [--allow-git-root]

Writes a JSON deploy preflight report. The script is read-only: it renders
compose config, inspects git/release metadata, and optionally probes health
endpoints. It does not pull, install, restart, stop, or deploy anything.
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
    --output)
      OUTPUT="${2:?missing output path}"
      shift 2
      ;;
    --health)
      CHECK_HEALTH=1
      shift
      ;;
    --no-github)
      CHECK_GITHUB=0
      shift
      ;;
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --allow-git-root)
      ALLOW_GIT_ROOT=1
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

if [[ -z "$REPOSITORY" ]]; then
  remote_url="$(git -C "$REPO_ROOT" remote get-url origin 2>/dev/null || true)"
  REPOSITORY="$(python3 - "$remote_url" <<'PY'
import re
import sys

url = sys.argv[1]
match = re.search(r"github.com[:/](?P<repo>[^/]+/[^/.]+)(?:\.git)?$", url)
print(match.group("repo") if match else "")
PY
)"
fi

python3 - "$REPO_ROOT" "$TAG" "$REPOSITORY" "$OUTPUT" "$CHECK_HEALTH" "$CHECK_GITHUB" "$ALLOW_DIRTY" "$ALLOW_GIT_ROOT" <<'PY'
import hashlib
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

repo_root = Path(sys.argv[1])
tag = sys.argv[2]
repository = sys.argv[3]
output = sys.argv[4]
check_health = sys.argv[5] == "1"
check_github = sys.argv[6] == "1"
allow_dirty = sys.argv[7] == "1"
allow_git_root = sys.argv[8] == "1"
version = (repo_root / "VERSION").read_text(encoding="utf-8").strip()
expected_assets = sorted([
    "SHA256SUMS",
    "agents-comms-helper-linux-amd64",
    "agents-daemon-linux-amd64",
    f"agents-gateway-{tag}.tgz",
    f"agents-harness-{tag}.tgz",
    "agents-manager-linux-amd64",
    f"agents-self-improve-{tag}.tgz",
])

def run(args: list[str], cwd: Path = repo_root) -> tuple[int, str, str]:
    proc = subprocess.run(args, cwd=cwd, text=True, capture_output=True, check=False)
    return proc.returncode, proc.stdout.strip(), proc.stderr.strip()

report: dict[str, object] = {
    "version": version,
    "tag": tag,
    "expected_tag": f"v{version}",
    "repository": repository or None,
    "checks": {},
    "warnings": [],
}

checks = report["checks"]
checks["version_tag_match"] = tag == f"v{version}"

def runtime_payload_status(root: Path) -> dict[str, object]:
    required_dirs = [
        ".agents",
        ".agents/context",
        ".data",
        ".user",
        ".user/skills",
        ".user/plugins",
        ".user/commands",
        ".user/projects",
    ]
    required_files = ["env.sh"]
    missing_dirs = [name for name in required_dirs if not (root / name).is_dir()]
    missing_files = [name for name in required_files if not (root / name).is_file()]
    violations: list[str] = []
    if not root.exists():
        violations.append("missing_runtime_root")
    if (root / ".agents" / ".agents").exists():
        violations.append("nested_dot_agents")
    if (root / "data").exists():
        violations.append("stale_data_mirror")
    if (root / ".git").exists() and not allow_git_root:
        violations.append("runtime_root_is_git_checkout")
    if missing_dirs:
        violations.append("missing_payload_dirs")
    if missing_files:
        violations.append("missing_runtime_files")
    return {
        "ok": not violations,
        "path": str(root),
        "exists": root.exists(),
        "missing_dirs": missing_dirs,
        "missing_files": missing_files,
        "violations": violations,
    }

agents_root = Path(os.environ.get("AGENTS_PREFLIGHT_AGENTS_ROOT") or os.environ.get("AGENTS_ROOT") or str(Path.home() / "agents")).expanduser()
checks["runtime_payload"] = runtime_payload_status(agents_root)

rc, branch, _ = run(["git", "branch", "--show-current"])
checks["git_branch"] = branch if rc == 0 else None
rc, head, _ = run(["git", "rev-parse", "HEAD"])
checks["git_head"] = head if rc == 0 else None
rc, status, _ = run(["git", "status", "--porcelain"])
checks["git_clean"] = rc == 0 and status == ""
if status:
    report["warnings"].append("working tree has local changes")

compose = {"available": False, "valid": False}
validator = repo_root / "deploy/validate-compose.sh"
if validator.exists():
    rc, stdout, stderr = run([str(validator)])
    compose.update({"available": True, "valid": rc == 0, "output": stdout, "error": stderr})
checks["compose"] = compose

release: dict[str, object] = {"checked": False}
if check_github and repository:
    rc, stdout, stderr = run([
        "gh",
        "release",
        "view",
        tag,
        "--repo",
        repository,
        "--json",
        "url,assets,isDraft,isPrerelease,tagName",
    ])
    release["checked"] = True
    if rc == 0:
        data = json.loads(stdout)
        assets = sorted(asset["name"] for asset in data.get("assets", []))
        release.update({
            "exists": True,
            "url": data.get("url"),
            "isDraft": data.get("isDraft"),
            "isPrerelease": data.get("isPrerelease"),
            "assets": assets,
            "missing_assets": [name for name in expected_assets if name not in assets],
            "download_counts": {
                asset["name"]: asset.get("downloadCount", 0)
                for asset in data.get("assets", [])
            },
        })
    else:
        release.update({"exists": False, "error": stderr or stdout})
checks["github_release"] = release

artifact_dir = repo_root / "dist/release" / tag
local_artifacts: dict[str, object] = {"path": str(artifact_dir), "exists": artifact_dir.exists()}
if artifact_dir.exists():
    files = sorted(path.name for path in artifact_dir.iterdir() if path.is_file())
    local_artifacts["files"] = files
    sums = artifact_dir / "SHA256SUMS"
    if sums.exists():
        failures: list[str] = []
        for line in sums.read_text(encoding="utf-8").splitlines():
            digest, name = line.split(maxsplit=1)
            name = name.lstrip("*")
            path = artifact_dir / name
            if not path.exists():
                failures.append(f"missing {name}")
                continue
            actual = hashlib.sha256(path.read_bytes()).hexdigest()
            if actual != digest:
                failures.append(f"sha256 mismatch {name}")
        local_artifacts["sha256_ok"] = not failures
        local_artifacts["sha256_failures"] = failures
    else:
        local_artifacts["sha256_ok"] = False
        local_artifacts["sha256_failures"] = ["missing SHA256SUMS"]
checks["local_artifacts"] = local_artifacts

if check_health:
    def configured_endpoints(name: str, default: str) -> list[str]:
        plural = os.environ.get(f"AGENTS_PREFLIGHT_{name.upper()}_URLS", "")
        if plural:
            return [item.strip() for item in plural.split(",") if item.strip()]
        return [os.environ.get(f"AGENTS_PREFLIGHT_{name.upper()}_URL", default)]

    endpoints = {
        "gateway": configured_endpoints("gateway", "http://s001:4000/health"),
        "daemon": configured_endpoints("daemon", "http://s001:18080/health"),
        "manager": configured_endpoints("manager", "http://s001:18081/health"),
    }
    expected_version = tag[1:] if tag.startswith("v") else tag
    health: dict[str, object] = {}
    for name, urls in endpoints.items():
      for index, url in enumerate(urls):
        key = name if len(urls) == 1 else f"{name}-{index + 1}"
        try:
            with urllib.request.urlopen(url, timeout=5) as response:
                body = response.read().decode("utf-8", "replace")
            parsed = None
            status = None
            version = None
            git_sha = None
            image_tag = None
            build_time = None
            node_id = None
            leader = None
            try:
                parsed = json.loads(body)
                if isinstance(parsed, dict):
                    status = parsed.get("status")
                    version = parsed.get("version")
                    git_sha = parsed.get("git_sha")
                    image_tag = parsed.get("image_tag")
                    build_time = parsed.get("build_time")
                    node_id = parsed.get("node_id")
                    leader = parsed.get("leader")
            except json.JSONDecodeError:
                parsed = None
            health[key] = {
                "ok": status == "healthy",
                "service": name,
                "url": url,
                "body": body,
                "status": status,
                "version": version,
                "version_matches_tag": version == expected_version,
                "git_sha": git_sha,
                "image_tag": image_tag,
                "build_time": build_time,
                "node_id": node_id,
                "leader": leader,
            }
        except (OSError, urllib.error.URLError) as exc:
            health[key] = {"ok": False, "service": name, "url": url, "error": str(exc), "version_matches_tag": False}
    checks["health"] = health
    control: dict[str, object] = {}
    orch_urls = endpoints["manager"]
    for index, url in enumerate(orch_urls):
        base = url[:-len("/health")] if url.endswith("/health") else url.rstrip("/")
        key = "manager-control" if len(orch_urls) == 1 else f"manager-control-{index + 1}"
        endpoint_results: dict[str, object] = {}
        failures: list[str] = []
        for route_name, route in {
            "health": "/v1/control/health",
            "runs": "/v1/control/runs",
        }.items():
            route_url = f"{base}{route}"
            try:
                with urllib.request.urlopen(route_url, timeout=5) as response:
                    body = response.read().decode("utf-8", "replace")
                    status_code = getattr(response, "status", response.getcode())
                try:
                    parsed = json.loads(body)
                except json.JSONDecodeError as exc:
                    parsed = body
                    failures.append(f"{route_name}.json")
                endpoint_results[route_name] = {
                    "ok": 200 <= int(status_code) < 300 and not isinstance(parsed, str),
                    "url": route_url,
                    "status_code": status_code,
                    "body": parsed,
                }
                if route_name == "health" and isinstance(parsed, dict) and parsed.get("status") != "healthy":
                    failures.append("health.status")
                if route_name == "runs" and isinstance(parsed, dict) and not isinstance(parsed.get("runs"), list):
                    failures.append("runs.shape")
            except urllib.error.HTTPError as exc:
                endpoint_results[route_name] = {"ok": False, "url": route_url, "status_code": exc.code, "error": str(exc)}
                failures.append(f"{route_name}.http")
            except (OSError, urllib.error.URLError) as exc:
                endpoint_results[route_name] = {"ok": False, "url": route_url, "error": str(exc)}
                failures.append(f"{route_name}.http")
        control[key] = {
            "ok": not failures,
            "base_url": base,
            "failures": failures,
            "endpoints": endpoint_results,
        }
    checks["control_plane"] = control

git_state_ok = bool(checks["git_clean"]) or allow_dirty
ok = (
    bool(checks["version_tag_match"])
    and git_state_ok
    and bool(compose.get("valid", False))
    and bool(checks["runtime_payload"].get("ok"))
)
if local_artifacts.get("exists"):
    ok = ok and bool(local_artifacts.get("sha256_ok"))
if release.get("checked"):
    ok = ok and bool(release.get("exists")) and not release.get("missing_assets")
if check_health:
    ok = ok and all(item.get("ok") and item.get("version_matches_tag") for item in checks.get("health", {}).values())
    ok = ok and all(item.get("ok") for item in checks.get("control_plane", {}).values())
report["ok"] = ok

encoded = json.dumps(report, indent=2, sort_keys=True)
if output:
    Path(output).parent.mkdir(parents=True, exist_ok=True)
    Path(output).write_text(encoded + "\n", encoding="utf-8")
print(encoded)

raise SystemExit(0 if ok else 1)
PY

