#!/usr/bin/env bash
# Capture read-only rollback evidence for agents services.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEPLOY_DIR/../.." && pwd)"
TAG="${1:-}"
OUTPUT=""

usage() {
  cat <<'EOF'
usage: deploy/rollback-evidence.sh [--tag vX.Y.Z] [--output path]

Writes a read-only JSON snapshot useful before deploy or rollback: git identity,
compose config, agents container/image state, and health bodies. It does not
stop, start, pull, build, or restart anything.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:?missing tag}"
      shift 2
      ;;
    --output)
      OUTPUT="${2:?missing output path}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      if [[ -z "$TAG" && "$1" == v* ]]; then
        TAG="$1"
        shift
      else
        echo "unknown argument: $1" >&2
        usage >&2
        exit 2
      fi
      ;;
  esac
done

if [[ -z "$TAG" ]]; then
  TAG="v$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"
fi
if [[ -z "$OUTPUT" ]]; then
  OUTPUT="$REPO_ROOT/dist/deploy/$TAG/rollback-evidence.json"
fi

python3 - "$REPO_ROOT" "$DEPLOY_DIR" "$TAG" "$OUTPUT" <<'PY'
import json
import os
import shlex
import subprocess
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

repo = Path(sys.argv[1])
deploy = Path(sys.argv[2])
tag = sys.argv[3]
output = Path(sys.argv[4])

def run(args, cwd=repo):
    try:
        proc = subprocess.run(args, cwd=cwd, text=True, capture_output=True, check=False)
        return {"rc": proc.returncode, "stdout": proc.stdout.strip(), "stderr": proc.stderr.strip()}
    except FileNotFoundError as exc:
        return {"rc": 127, "stdout": "", "stderr": str(exc)}

def redact_value(value):
    if not isinstance(value, str):
        return value
    key = value.split("=", 1)[0].upper()
    if any(marker in key for marker in ("TOKEN", "KEY", "SECRET", "PASSWORD", "CREDENTIAL")):
        return f"{value.split('=', 1)[0]}=<redacted>"
    return value

def sanitize(obj):
    if isinstance(obj, dict):
        clean = {}
        for key, value in obj.items():
            upper = key.upper()
            if any(marker in upper for marker in ("TOKEN", "KEY", "SECRET", "PASSWORD", "CREDENTIAL")):
                clean[key] = "<redacted>"
            elif key == "Env" and isinstance(value, list):
                clean[key] = [redact_value(item) for item in value]
            else:
                clean[key] = sanitize(value)
        return clean
    if isinstance(obj, list):
        return [sanitize(item) for item in obj]
    return obj

def docker_inspect(name):
    result = run(["docker", "inspect", name])
    if result["rc"] != 0:
        return result
    try:
        result["json"] = sanitize(json.loads(result["stdout"]))
        result["stdout"] = ""
    except json.JSONDecodeError:
        result["stdout"] = "<unparseable docker inspect output omitted>"
    return result

def configured_compose_cmds():
    configured = os.environ.get("AGENTS_COMPOSE_CMD", "").strip()
    if configured:
        return [shlex.split(configured)]
    return [["docker", "compose"], ["docker-compose"]]

def compose_config():
    attempts = []
    compose_file = str(deploy / "docker-compose.cluster.yml")
    for candidate in configured_compose_cmds():
        result = run([*candidate, "-f", compose_file, "config", "--format", "json"])
        result["command"] = candidate
        attempts.append(dict(result))
        if result["rc"] != 0:
            continue
        try:
            result["json"] = sanitize(json.loads(result["stdout"]))
            result["stdout"] = ""
        except json.JSONDecodeError:
            result["stdout"] = "<unparseable compose config output omitted>"
        result["attempts"] = attempts
        return result
    fallback = dict(attempts[-1]) if attempts else {"rc": 127, "stdout": "", "stderr": "no compose command candidates"}
    fallback["attempts"] = attempts
    return fallback

def health(name, url):
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            body = response.read(4096).decode("utf-8", "replace")
        parsed = None
        try:
            parsed = json.loads(body)
        except json.JSONDecodeError:
            pass
        return {"name": name, "url": url, "ok": True, "body": body, "json": parsed}
    except (OSError, urllib.error.URLError) as exc:
        return {"name": name, "url": url, "ok": False, "error": str(exc)}

def configured_endpoints(name, default):
    plural = os.environ.get(f"AGENTS_ROLLBACK_{name.upper()}_URLS", "")
    if plural:
        return [item.strip() for item in plural.split(",") if item.strip()]
    return [os.environ.get(f"AGENTS_ROLLBACK_{name.upper()}_URL", default)]

health_checks = []
for service, default in {
    "gateway": "http://s001:4000/health",
    "daemon": "http://s001:18080/health",
    "manager": "http://s001:18081/health",
}.items():
    urls = configured_endpoints(service, default)
    for index, url in enumerate(urls):
        key = service if len(urls) == 1 else f"{service}-{index + 1}"
        health_checks.append(health(key, url))

containers = ["agents-gateway", "agents-daemon", "agents-manager", "agents-nats"]
report = {
    "created_at": datetime.now(timezone.utc).isoformat(),
    "tag": tag,
    "version": tag[1:] if tag.startswith("v") else tag,
    "git": {
        "head": run(["git", "rev-parse", "HEAD"]),
        "tag_commit": run(["git", "rev-list", "-n", "1", tag]),
        "status": run(["git", "status", "--porcelain"]),
    },
    "compose": {
        "config": compose_config(),
    },
    "docker": {
        "ps": run(["docker", "ps", "--filter", "name=agents-", "--format", "{{json .}}"]),
        "containers": {name: docker_inspect(name) for name in containers},
    },
    "health": health_checks,
}

output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
print(json.dumps(report, indent=2, sort_keys=True))
PY

