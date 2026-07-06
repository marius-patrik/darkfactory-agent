"""Parse and validate Claude-format capability manifests."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Literal

from agent.capabilities.discovery import CapabilityKind, CapabilityRecord


class ValidationError(Exception):
    """A capability manifest failed validation."""

    def __init__(self, field: str, message: str, *, path: Path | None = None) -> None:
        self.field = field
        self.path = path
        super().__init__(f"{field}: {message}" + (f" ({path})" if path else ""))


@dataclass(frozen=True, slots=True)
class CapabilityManifest:
    """Validated, normalized capability manifest."""

    kind: CapabilityKind
    name: str
    version: str
    description: str
    path: Path
    origin: Literal["template", "user"]
    # Parsed declarations
    permissions: dict[str, Any] = field(default_factory=dict)
    host_reqs: dict[str, Any] = field(default_factory=dict)
    io: dict[str, Any] = field(default_factory=dict)
    metadata: dict[str, Any] = field(default_factory=dict)
    lineage: dict[str, Any] = field(default_factory=dict)
    scorecard: dict[str, Any] = field(default_factory=dict)
    # Execution lane inferred from manifest shape
    exec_lane: Literal["daemon", "knative"] = "daemon"
    # Raw body (instruction, command template, hook prompt)
    body: str = ""
    # For skills: declared sub-capabilities / bundled scripts
    sub_capabilities: list[str] = field(default_factory=list)
    # For script-wrapped skills/hooks: local executable relative to capability dir
    local_script: Path | None = None


_SEMVER_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<prerelease>[a-zA-Z0-9.\-]+))?$")


def _is_semverish(version: str) -> bool:
    return bool(_SEMVER_RE.match(version.strip()))


def _require_field(record: CapabilityRecord, field_name: str) -> Any:
    value = record.frontmatter.get(field_name)
    if value is None or (isinstance(value, str) and not value.strip()):
        raise ValidationError(field_name, f"missing required field '{field_name}'", path=record.path)
    return value


def _normalize_permissions(record: CapabilityRecord) -> dict[str, Any]:
    perms: dict[str, Any] = {}
    raw_allowed = record.frontmatter.get("allowed-tools") or record.frontmatter.get("allowed_tools")
    if raw_allowed is not None:
        if isinstance(raw_allowed, str):
            perms["allowed_tools"] = [raw_allowed.strip()]
        elif isinstance(raw_allowed, list):
            perms["allowed_tools"] = [str(x) for x in raw_allowed]
        else:
            raise ValidationError("allowed-tools", "must be a string or list of strings", path=record.path)
    raw_hosts = record.frontmatter.get("host-reqs") or record.frontmatter.get("host_reqs")
    if raw_hosts is not None:
        if not isinstance(raw_hosts, dict):
            raise ValidationError("host-reqs", "must be an object", path=record.path)
        perms["host_reqs"] = raw_hosts
    return perms


def _infer_exec_lane(record: CapabilityRecord) -> Literal["daemon", "knative"]:
    """Infer execution lane from manifest hints.

    - A skill/hook that bundles a local executable script is host-bound -> daemon.
    - A skill/hook with ``exec_lane: knative`` (or ``runtime: knative``) is detached.
    - Defaults to daemon for safety.
    """
    explicit = (
        record.frontmatter.get("exec_lane")
        or record.frontmatter.get("exec-lane")
        or record.frontmatter.get("runtime")
    )
    if isinstance(explicit, str) and explicit.lower() == "knative":
        return "knative"
    return "daemon"


def _find_local_script(record: CapabilityRecord) -> Path | None:
    """If the capability dir contains an executable file named in the manifest, return it."""
    if not record.path.is_dir():
        return None
    script_name = record.frontmatter.get("script") or record.frontmatter.get("executable")
    if not script_name:
        return None
    candidate = record.path / script_name
    if candidate.exists() and candidate.is_file() and candidate.stat().st_mode & 0o111:
        return candidate
    return None


def _normalize_io(record: CapabilityRecord) -> dict[str, Any]:
    io = record.frontmatter.get("io")
    if isinstance(io, dict):
        return io
    return {}


def parse_manifest(record: CapabilityRecord) -> CapabilityManifest:
    """Validate a discovered capability record and return a normalized manifest."""
    if record.kind == "plugin":
        name = _require_field(record, "name")
        version = record.frontmatter.get("version", "0.0.0")
        if not _is_semverish(version):
            raise ValidationError("version", f"invalid semver-ish version '{version}'", path=record.path)
        return CapabilityManifest(
            kind="plugin",
            name=str(name).strip(),
            version=str(version).strip(),
            description=str(record.frontmatter.get("description", "")).strip(),
            path=record.path,
            origin=record.origin,
            permissions=_normalize_permissions(record),
            host_reqs={},
            io={},
            metadata={"bundled": record.frontmatter.get("bundled", {})},
            lineage={"origin": record.origin},
            body=record.body,
            exec_lane=_infer_exec_lane(record),
        )

    # skill / hook / script / extension / command
    name = _require_field(record, "name")
    version = record.frontmatter.get("version", "0.0.0")
    if not _is_semverish(version):
        raise ValidationError("version", f"invalid semver-ish version '{version}'", path=record.path)

    description = str(record.frontmatter.get("description", "")).strip()
    if not description:
        description = str(record.frontmatter.get("summary", "")).strip()

    local_script = _find_local_script(record)
    exec_lane = _infer_exec_lane(record)

    return CapabilityManifest(
        kind=record.kind,
        name=str(name).strip(),
        version=str(version).strip(),
        description=description,
        path=record.path,
        origin=record.origin,
        permissions=_normalize_permissions(record),
        host_reqs=record.frontmatter.get("host-reqs") or record.frontmatter.get("host_reqs") or {},
        io=_normalize_io(record),
        metadata={
            "path": str(record.path),
            "argument_hint": record.frontmatter.get("argument-hint") or record.frontmatter.get("argument_hint"),
        },
        lineage={"origin": record.origin},
        body=record.body,
        exec_lane=exec_lane,
        local_script=local_script,
        sub_capabilities=record.frontmatter.get("sub-capabilities") or record.frontmatter.get("sub_capabilities") or [],
    )
