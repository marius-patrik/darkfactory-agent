"""Discover Claude-format capabilities from source/runtime roots."""

from __future__ import annotations

import os
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Literal

try:
    import yaml
except ImportError:  # pragma: no cover - defensive fallback
    yaml = None  # type: ignore[assignment]

CapabilityKind = Literal["skill", "plugin", "extension", "hook", "script"]

KIND_DIRS: dict[CapabilityKind, tuple[str, ...]] = {
    "skill": ("skills",),
    "plugin": ("plugins",),
    "extension": ("extensions",),
    "hook": ("hooks",),
    "script": ("scripts",),
}

# Frontmatter delimiter
_FM_RE = re.compile(r"^---\s*\n(.*?)^---\s*\n", re.MULTILINE | re.DOTALL)


@dataclass(frozen=True, slots=True)
class CapabilityRecord:
    """Raw discovered capability before validation."""

    kind: CapabilityKind
    name: str | None
    version: str | None
    path: Path
    origin: Literal["template", "user"]
    frontmatter: dict
    body: str


def _parse_simple_yaml(text: str) -> dict:
    """Minimal YAML-like parser for plain frontmatter (key:value and simple lists)."""
    result: dict = {}
    current_key: str | None = None
    for raw_line in text.splitlines():
        line = raw_line.rstrip()
        stripped = line.lstrip()
        if not stripped:
            continue
        if stripped.startswith("- "):
            if current_key is not None:
                value = stripped[2:].strip()
                result.setdefault(current_key, []).append(value)
            continue
        if ":" in stripped:
            key, _, value = stripped.partition(":")
            key = key.strip()
            value = value.strip()
            current_key = key
            if value == "":
                result[key] = []
            elif value.lower() in ("true", "false"):
                result[key] = value.lower() == "true"
            else:
                # Strip surrounding quotes
                if (value.startswith('"') and value.endswith('"')) or (
                    value.startswith("'") and value.endswith("'")
                ):
                    value = value[1:-1]
                result[key] = value
    return result


def _extract_frontmatter(text: str) -> tuple[dict, str]:
    match = _FM_RE.match(text)
    if not match:
        return {}, text
    fm_text = match.group(1)
    try:
        fm = (yaml.safe_load(fm_text) if yaml is not None else _parse_simple_yaml(fm_text)) or {}
    except Exception:
        fm = _parse_simple_yaml(fm_text) or {}
    body = text[match.end() :]
    return fm if isinstance(fm, dict) else {}, body


def _discover_skill(root: Path, origin: Literal["template", "user"]) -> Iterable[CapabilityRecord]:
    skills_dir = root / "skills"
    if not skills_dir.is_dir():
        return
    for skill_dir in skills_dir.iterdir():
        if not skill_dir.is_dir():
            continue
        md_file = skill_dir / "SKILL.md"
        if not md_file.is_file():
            continue
        text = md_file.read_text(encoding="utf-8")
        fm, body = _extract_frontmatter(text)
        yield CapabilityRecord(
            kind="skill",
            name=fm.get("name"),
            version=fm.get("version"),
            path=skill_dir,
            origin=origin,
            frontmatter=fm,
            body=body.strip(),
        )


def _discover_plugin(root: Path, origin: Literal["template", "user"]) -> Iterable[CapabilityRecord]:
    """Discover Claude plugins.

    Supports both source-template layout (plugins live directly under the root)
    and runtime layout (plugins live under ``root/plugins/``).
    """
    import json

    seen: set[Path] = set()
    candidate_dirs: list[Path] = [root]
    plugins_subdir = root / "plugins"
    if plugins_subdir.is_dir():
        candidate_dirs.append(plugins_subdir)

    for search_dir in candidate_dirs:
        for plugin_dir in search_dir.iterdir():
            if not plugin_dir.is_dir() or plugin_dir in seen:
                continue
            manifest_file = plugin_dir / ".claude-plugin" / "plugin.json"
            if not manifest_file.is_file():
                continue
            seen.add(plugin_dir)
            try:
                fm = json.loads(manifest_file.read_text(encoding="utf-8"))
            except json.JSONDecodeError:
                fm = {}
            yield CapabilityRecord(
                kind="plugin",
                name=fm.get("name"),
                version=fm.get("version"),
                path=plugin_dir,
                origin=origin,
                frontmatter=fm,
                body="",
            )


def _discover_command(root: Path, origin: Literal["template", "user"]) -> Iterable[CapabilityRecord]:
    commands_dir = root / "commands"
    if not commands_dir.is_dir():
        return
    for cmd_file in commands_dir.iterdir():
        if not cmd_file.is_file() or cmd_file.suffix.lower() != ".md":
            continue
        text = cmd_file.read_text(encoding="utf-8")
        fm, body = _extract_frontmatter(text)
        yield CapabilityRecord(
            kind="script",  # commands are script-kind invocables
            name=fm.get("name") or cmd_file.stem,
            version=fm.get("version"),
            path=cmd_file,
            origin=origin,
            frontmatter=fm,
            body=body.strip(),
        )


def _discover_hook(root: Path, origin: Literal["template", "user"]) -> Iterable[CapabilityRecord]:
    hooks_dir = root / "hooks"
    if not hooks_dir.is_dir():
        return
    for hook_file in hooks_dir.iterdir():
        if not hook_file.is_file() or hook_file.suffix.lower() != ".md":
            continue
        text = hook_file.read_text(encoding="utf-8")
        fm, body = _extract_frontmatter(text)
        yield CapabilityRecord(
            kind="hook",
            name=fm.get("name") or hook_file.stem,
            version=fm.get("version"),
            path=hook_file,
            origin=origin,
            frontmatter=fm,
            body=body.strip(),
        )


def _discover_script(root: Path, origin: Literal["template", "user"]) -> Iterable[CapabilityRecord]:
    scripts_dir = root / "scripts"
    if not scripts_dir.is_dir():
        return
    for script_file in scripts_dir.iterdir():
        if not script_file.is_file():
            continue
        # scripts have no frontmatter; name from filename
        yield CapabilityRecord(
            kind="script",
            name=script_file.stem,
            version=None,
            path=script_file,
            origin=origin,
            frontmatter={},
            body="",
        )


def discover(root: Path, origin: Literal["template", "user"]) -> list[CapabilityRecord]:
    """Discover all Claude-format capabilities under a single root."""
    records: list[CapabilityRecord] = []
    records.extend(_discover_skill(root, origin))
    records.extend(_discover_plugin(root, origin))
    records.extend(_discover_command(root, origin))
    records.extend(_discover_hook(root, origin))
    records.extend(_discover_script(root, origin))
    return records


def default_runtime_root() -> Path:
    rommie_home = os.environ.get("ROMMIE_HOME", "")
    if rommie_home:
        return Path(rommie_home)
    return Path.home() / ".rommie"


def default_agents_roots() -> list[Path]:
    agents_home = os.environ.get("AGENTS_HOME", "")
    if not agents_home:
        return []
    return [Path(agents_home)]


def discover_all(
    roots: Iterable[Path] | None = None,
) -> list[CapabilityRecord]:
    """Discover capabilities from listed roots.

    If ``roots`` is omitted, discovers from the built-in source template root
    (``plugins/`` relative to the repo root) plus the runtime root
    ``~/.rommie`` (override via ``ROMMIE_HOME``).
    """
    if roots is None:
        repo_root = Path(__file__).resolve().parents[3]
        roots = [repo_root / "plugins", default_runtime_root(), *default_agents_roots()]
    all_records: list[CapabilityRecord] = []
    for root in roots:
        if not root.exists():
            continue
        origin: Literal["template", "user"] = "template" if root.name == "plugins" else "user"
        all_records.extend(discover(root, origin))
    return all_records
