#!/usr/bin/env bash
# Validate deploy compose wiring that docker compose itself does not catch early.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$DEPLOY_DIR/docker-compose.cluster.yml}"
TMP_JSON="$(mktemp)"
trap 'rm -f "$TMP_JSON"' EXIT

if [ "${AGENTS_FORCE_COMPOSE_FALLBACK:-0}" != "1" ] && docker compose version >/dev/null 2>&1; then
  docker compose -f "$COMPOSE_FILE" config --format json > "$TMP_JSON"
else
  python3 - "$COMPOSE_FILE" "$TMP_JSON" <<'PY'
import json
import re
import sys

compose_path, out_path = sys.argv[1:3]


def indent_of(line: str) -> int:
    return len(line) - len(line.lstrip(" "))


def clean_scalar(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        return value[1:-1]
    return value


def env_default(value: str) -> str:
    match = re.fullmatch(r"\$\{[^}:]+:-([^}]+)\}", value)
    return match.group(1) if match else value


def parse_port(value: str) -> dict[str, str] | None:
    value = clean_scalar(value)
    protocol = "tcp"
    if "/" in value.rsplit(":", 1)[-1]:
        value, protocol = value.rsplit("/", 1)

    if value.startswith("${"):
        end = value.find("}")
        if end != -1 and value[end + 1 :].startswith(":"):
            return {
                "host_ip": "0.0.0.0",
                "published": env_default(value[: end + 1]),
                "protocol": protocol,
            }

    parts = value.split(":")
    if len(parts) < 2:
        return None
    host_ip = "0.0.0.0"
    if len(parts) >= 3:
        host_ip = parts[-3]
    return {"host_ip": host_ip, "published": env_default(parts[-2]), "protocol": protocol}


services: dict[str, dict[str, object]] = {}
current_service: str | None = None
services_indent: int | None = None
service_indent: int | None = None
ports_indent: int | None = None
volumes_indent: int | None = None

with open(compose_path, encoding="utf-8") as fh:
    for raw in fh:
        line = raw.rstrip("\n")
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = indent_of(line)
        if stripped == "services:":
            services_indent = indent
            continue
        if services_indent is None:
            continue
        if indent <= services_indent:
            current_service = None
            service_indent = None
            ports_indent = None
            continue
        if stripped.endswith(":") and indent == services_indent + 2 and not stripped.startswith("-"):
            current_service = stripped[:-1]
            services[current_service] = {"ports": [], "volumes": []}
            service_indent = indent
            ports_indent = None
            volumes_indent = None
            continue
        if current_service is None or service_indent is None:
            continue
        if indent <= service_indent:
            ports_indent = None
            volumes_indent = None
        if indent == service_indent + 2 and not stripped.startswith("-"):
            ports_indent = None
            volumes_indent = None
        if stripped.startswith("image:"):
            services[current_service]["image"] = clean_scalar(stripped.split(":", 1)[1])
        if stripped == "ports:":
            ports_indent = indent
            volumes_indent = None
            continue
        if stripped == "volumes:":
            volumes_indent = indent
            ports_indent = None
            continue
        if ports_indent is not None and indent > ports_indent and stripped.startswith("- "):
            port = parse_port(stripped[2:])
            if port is not None:
                services[current_service]["ports"].append(port)
        if volumes_indent is not None and indent > volumes_indent and stripped.startswith("- "):
            services[current_service]["volumes"].append(clean_scalar(stripped[2:]))

with open(out_path, "w", encoding="utf-8") as fh:
    json.dump({"services": services}, fh)
PY
fi

python3 - "$TMP_JSON" <<'PY'
import json
import sys
from collections import defaultdict

config_path = sys.argv[1]
with open(config_path, encoding="utf-8") as fh:
    config = json.load(fh)

published: dict[tuple[str, str], list[str]] = defaultdict(list)
for service_name, service in sorted(config.get("services", {}).items()):
    for port in service.get("ports") or []:
        protocol = str(port.get("protocol", "tcp"))
        host_ip = str(port.get("host_ip", "0.0.0.0"))
        host_port = str(port.get("published", ""))
        if host_port:
            published[(host_ip, f"{host_port}/{protocol}")].append(service_name)

duplicates = {
    f"{host_ip}:{port}": services
    for (host_ip, port), services in published.items()
    if len(services) > 1
}
if duplicates:
    print("duplicate published compose ports:", file=sys.stderr)
    for port, services in sorted(duplicates.items()):
        print(f"  {port}: {', '.join(services)}", file=sys.stderr)
    raise SystemExit(1)

bad_prefixes = ("/nix", "/run/current-system/sw")


def bad_runner_mount(volume: object) -> str | None:
    if isinstance(volume, str):
        parts = volume.split(":")
        paths = parts[:2] if len(parts) >= 2 else parts
    elif isinstance(volume, dict):
        paths = [
            str(volume.get("source", "")),
            str(volume.get("target", "")),
        ]
    else:
        return None
    for path in paths:
        if path.startswith(bad_prefixes):
            return path
    return None


bad_mounts: list[str] = []
for service_name, service in sorted(config.get("services", {}).items()):
    image = str(service.get("image", ""))
    if image != "ghcr.io/actions/actions-runner":
        continue
    for volume in service.get("volumes") or []:
        bad_path = bad_runner_mount(volume)
        if bad_path:
            bad_mounts.append(f"{service_name}: {bad_path}")

if bad_mounts:
    print("actions-runner services must not mount Nix system paths:", file=sys.stderr)
    for mount in bad_mounts:
        print(f"  {mount}", file=sys.stderr)
    raise SystemExit(1)

print("compose validation ok")
PY
