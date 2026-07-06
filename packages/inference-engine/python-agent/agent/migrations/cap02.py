"""CAP-02 one-shot migrator: loose ~/.rommie -> final §19 schema.

Dry-run by default; ``--apply`` executes.  stdlib only.
"""
from __future__ import annotations

import hashlib
import json
import os
import shutil
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable, Iterator, TextIO

MARKER_NAME = "runtime/cap02-migrated.json"
MANIFEST_DIR = "manifests"

# Top-level directories that are already part of the final schema and should
# not be archived/moved if encountered on a partially-migrated tree.
FINAL_TOP_DIRS = {
    "shared",
    "clis",
    "domains",
    "runtime",
    "models",
    "secrets",
    "logs",
    "metrics",
    "manifests",
    "quarantine",
    "tmp",
    "archive",
}

# Global payload classes that are already in their final location.
GLOBAL_KEEP = {
    "skills",
    "plugins",
    "hooks",
    "scripts",
    "prompts",
    "memories",
    "commands",
    "rules",
    "roles",
    "workers",
}

# Superseded global classes that go to archive/global/.
GLOBAL_ARCHIVE = {"plans", "tasks", "handoffs"}

GENERATED_DIR_NAMES = {
    ".venv",
    "node_modules",
    "__pycache__",
    ".mypy_cache",
    ".pytest_cache",
    ".ruff_cache",
    "cache",
    "caches",
}
GENERATED_FILE_NAMES = {".DS_Store", "Thumbs.db"}
GENERATED_SUFFIXES = (".pyc", ".pyo", ".sqlite-wal", ".sqlite-shm", ".wal", ".shm")

STANDARD_ROLES: list[tuple[str, str]] = [
    ("conversation", "agent"),
    ("thought", "agent"),
    ("orchestration", "both"),
    ("reasoning", "worker"),
    ("planning", "worker"),
    ("review", "worker"),
    ("execution", "worker"),
    ("reflection", "worker"),
    ("day-dream", "worker"),
    ("deep-sleep", "worker"),
]

AGENTS = ["rommie", "claude", "kimi", "codex", "agy"]
PAYLOAD_DIRS = [
    "roles",
    "workers",
    "prompts",
    "skills",
    "plugins",
    "hooks",
    "scripts",
    "commands",
    "rules",
    "memories",
]
CONTEXT_FILES = [
    "goal.md",
    "task.md",
    "plan.md",
    "short.md",
    "context.md",
    "cache.md",
    "long.md",
    "handoff.md",
]


@dataclass(frozen=True)
class Operation:
    rel_src: str
    rel_dst: str | None
    disposition: str
    src_is_dir: bool
    note: str = ""


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _manifest_ts() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def is_generated(name: str) -> bool:
    if name in GENERATED_DIR_NAMES or name in GENERATED_FILE_NAMES:
        return True
    return any(name.endswith(suffix) for suffix in GENERATED_SUFFIXES)


def _iter_dir(path: Path) -> list[Path]:
    try:
        return sorted(path.iterdir())
    except FileNotFoundError:
        return []


def _walk_files(path: Path) -> Iterator[tuple[Path, str]]:
    """Yield (file_path, relative-slash-path) for every file under *path*.

    Broken symlinks are silently skipped — os.walk() reports them as files but
    they cannot be opened for hashing.
    """
    for root, _dirs, files in os.walk(path):
        root_p = Path(root)
        rel_root = root_p.relative_to(path)
        for name in files:
            fpath = root_p / name
            if not fpath.exists():  # skip broken symlinks
                continue
            yield fpath, _rel_posix(rel_root / name)


def _rel_posix(path: Path) -> str:
    return path.as_posix().lstrip("./")


def classify_hermes(path: Path) -> Iterator[Operation]:
    """Mine memory-like files to global/memories/hermes; archive the rest."""
    for root, _dirs, files in os.walk(path):
        root_p = Path(root)
        rel_root = root_p.relative_to(path)
        for name in files:
            rel_file = _rel_posix(rel_root / name)
            rel_src = f"hermes-agent/{rel_file}".lstrip("./")
            if name.endswith(".md") or "memory" in name.lower():
                dst = f"global/memories/hermes/{rel_file}".lstrip("./")
                yield Operation(rel_src, dst, "active", False, note="mined memory")
            else:
                dst = f"archive/hermes-agent/{rel_file}".lstrip("./")
                yield Operation(rel_src, dst, "archive", False)


def classify_top_level(name: str, path: Path) -> Iterator[Operation]:
    if is_generated(name):
        yield Operation(name, None, "drop", path.is_dir())
        return

    if name == "node.yaml":
        yield Operation(name, name, "keep", False)
        return

    if name == "global":
        yield from classify_global(path)
        return

    # LEGACY operational -> archive (must come before FINAL_TOP_DIRS so that a
    # legacy ``agents/`` directory is archived before the fresh ``agents/`` seed
    # is created).
    if name in {
        "projects",
        "conversations",
        "jobs",
        "state",
        "histories",
        "summaries",
        "context",
        "wiki",
        "providers",
        "plannotator",
        "src",
        "session-env",
        "domains",
        "agents",
    }:
        yield Operation(name, f"archive/{name}", "archive", True)
        return
    if "snapshot" in name:
        yield Operation(name, f"archive/{name}", "archive", True)
        return

    # Loose markdown + legacy agent/context/memory/work dirs
    if name.endswith(".md") or name in {"agent", "memory", "work"}:
        yield Operation(name, f"archive/loose/{name}", "archive", path.is_dir())
        return

    # Merge top-level skill-like dirs into global/
    if name in {"skills", "plugins", "hooks"}:
        for child in _iter_dir(path):
            yield Operation(
                f"{name}/{child.name}",
                f"global/{name}/{child.name}",
                "seed-merge",
                child.is_dir(),
            )
        return

    # ACTIVE / preserved raw data
    if name == "sessions":
        yield Operation(name, "shared/sessions", "active", True, note="tag agent=rommie")
        return
    if name == "config":
        yield Operation(name, "shared/config", "active", True)
        return
    if name == "cluster":
        yield Operation(name, "shared/cluster", "active", True)
        return
    if name == "secrets":
        yield Operation(name, "secrets", "keep", True)
        return
    if name == "history-archive":
        yield Operation(name, "shared/history-archive", "active", True, note="keep raw")
        return
    if name == "hermes-agent":
        yield from classify_hermes(path)
        return

    if name in FINAL_TOP_DIRS:
        # Already in final schema; leave alone.
        yield Operation(name, name, "keep", path.is_dir())
        return

    # Anything else is unclassifiable -> quarantine (never dropped).
    yield Operation(name, f"quarantine/{name}", "quarantine", path.is_dir())


def classify_global(global_path: Path) -> Iterator[Operation]:
    for child in _iter_dir(global_path):
        name = child.name
        rel = f"global/{name}"
        if name in GLOBAL_KEEP:
            yield Operation(rel, rel, "seed-keep", child.is_dir())
        elif name in GLOBAL_ARCHIVE:
            yield Operation(rel, f"archive/{rel}", "archive", child.is_dir())
        elif child.is_file() and name.endswith(".md"):
            # [S2.3] table row: loose .md files inside global/ (e.g. agent.md,
            # context.md, memory.md, work.md) are legacy/superseded and go to
            # archive/, not quarantine.  This mirrors classify_top_level()'s
            # treatment of the same filenames at the repo root.
            yield Operation(rel, f"archive/{rel}", "archive", False)
        else:
            # Everything else that is not in the spec table lands in quarantine
            # as the safe conservative holding pen (zero-loss, retrievable).
            # This includes live credential/config files such as credentials/,
            # secrets.manifest.json, kimi.json, config.json, settings.json,
            # keybindings.json, hooks.json, mcp_config.json — none of which have
            # an explicit [S2.3] mapping.  They are NOT dropped; quarantine is the
            # intended catch-all for un-tabled entries until a future operator
            # review decides whether they belong in secrets/ or shared/config.
            yield Operation(rel, f"quarantine/{rel}", "quarantine", child.is_dir())


def collect_plan(root: Path) -> list[Operation]:
    """Return the planned operations.  A marker file makes subsequent runs no-ops."""
    marker = root / MARKER_NAME
    if marker.exists():
        return []

    plan: list[Operation] = []
    for entry in _iter_dir(root):
        plan.extend(classify_top_level(entry.name, entry))
    return plan


def _write_manifest(
    mf: TextIO,
    rel_src: str,
    rel_dst: str | None,
    disposition: str,
    sha256: str | None = None,
    size: int | None = None,
    note: str = "",
) -> None:
    record: dict[str, object] = {
        "timestamp": _now(),
        "src": rel_src,
        "dst": rel_dst,
        "disposition": disposition,
    }
    if sha256 is not None:
        record["sha256"] = sha256
    if size is not None:
        record["size"] = size
    if note:
        record["note"] = note
    mf.write(json.dumps(record, ensure_ascii=False) + "\n")


def _move_or_merge(src: Path, dst: Path) -> None:
    """Move *src* to *dst*, merging directories if *dst* already exists."""
    if dst.exists():
        if src.is_dir() and dst.is_dir():
            for child in _iter_dir(src):
                target = dst / child.name
                if target.exists():
                    # Collision: leave source in place under a quarantine suffix.
                    quarantine = dst.parent / "quarantine" / f"collision-{src.name}-{child.name}"
                    quarantine.parent.mkdir(parents=True, exist_ok=True)
                    shutil.move(str(child), str(quarantine))
                else:
                    shutil.move(str(child), str(target))
            shutil.rmtree(src)
        elif src.is_file() and dst.is_dir():
            shutil.move(str(src), str(dst / src.name))
        elif src.is_dir() and dst.is_file():
            # Weird collision: quarantine the source dir.
            quarantine = src.parent / "quarantine" / f"collision-{src.name}"
            quarantine.parent.mkdir(parents=True, exist_ok=True)
            shutil.move(str(src), str(quarantine))
        else:
            # File/file collision: prefer existing destination, quarantine source.
            quarantine = src.parent / "quarantine" / f"collision-{src.name}"
            quarantine.parent.mkdir(parents=True, exist_ok=True)
            shutil.move(str(src), str(quarantine))
    else:
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(src), str(dst))


def _execute(op: Operation, root: Path, mf: TextIO) -> None:
    src = root / op.rel_src
    if not src.exists():
        return

    if op.disposition == "drop":
        if src.is_dir():
            _write_manifest(mf, op.rel_src, None, "drop", size=_count_files(src))
            shutil.rmtree(src)
        else:
            _write_manifest(mf, op.rel_src, None, "drop", size=src.stat().st_size)
            src.unlink()
        return

    if op.rel_dst is None:
        return

    dst = root / op.rel_dst

    if src.is_file():
        sha = sha256_file(src)
        size = src.stat().st_size
        _write_manifest(mf, op.rel_src, op.rel_dst, op.disposition, sha, size)
        _move_or_merge(src, dst)
        return

    if src.is_dir():
        # Audit every file, then move the directory.
        for fpath, rel in _walk_files(src):
            sha = sha256_file(fpath)
            size = fpath.stat().st_size
            _write_manifest(
                mf,
                f"{op.rel_src}/{rel}",
                f"{op.rel_dst}/{rel}",
                op.disposition,
                sha,
                size,
            )
        _write_manifest(mf, op.rel_src, op.rel_dst, op.disposition, size=_count_files(src))
        _move_or_merge(src, dst)
        return

    # Symlinks or other oddities -> quarantine.
    quarantine = root / "quarantine" / f"oddity-{src.name}"
    quarantine.parent.mkdir(parents=True, exist_ok=True)
    shutil.move(str(src), str(quarantine))
    _write_manifest(mf, op.rel_src, _rel_posix(quarantine.relative_to(root)), "quarantine")


def _count_files(path: Path) -> int:
    if path.is_file():
        return 1
    try:
        return sum(1 for _ in path.rglob("*") if _.is_file())
    except FileNotFoundError:
        return 0


def _ensure_final_dirs(root: Path) -> None:
    for name in {
        "shared",
        "global",
        "clis",
        "domains",
        "runtime",
        "models",
        "secrets",
        "logs",
        "metrics",
        "manifests",
        "quarantine",
        "tmp",
    }:
        (root / name).mkdir(parents=True, exist_ok=True)


def _seed_global_roles(root: Path, mf: TextIO) -> None:
    roles_dir = root / "global" / "roles"
    roles_dir.mkdir(parents=True, exist_ok=True)
    for role, scope in STANDARD_ROLES:
        path = roles_dir / f"{role}.yaml"
        if not path.exists():
            content = (
                f"# Standard role: {role}\n"
                f"name: {role}\n"
                f"scope: {scope}\n"
            )
            path.write_text(content, encoding="utf-8")
            _write_manifest(
                mf,
                f"global/roles/{role}.yaml",
                f"global/roles/{role}.yaml",
                "seed",
                sha256_file(path),
                path.stat().st_size,
            )


def _seed_global_workers(root: Path, mf: TextIO) -> None:
    workers_dir = root / "global" / "workers"
    workers_dir.mkdir(parents=True, exist_ok=True)

    standing = [
        ("conversation", "conversation"),
        ("thought", "thought"),
    ]
    for name, role in standing:
        path = workers_dir / f"{name}.yaml"
        if not path.exists():
            content = (
                f"# Standing worker: {name}\n"
                f"name: {name}\n"
                f"role: {role}\n"
                f"scope: standing\n"
            )
            path.write_text(content, encoding="utf-8")
            _write_manifest(
                mf,
                f"global/workers/{name}.yaml",
                f"global/workers/{name}.yaml",
                "seed",
                sha256_file(path),
                path.stat().st_size,
            )

    on_demand = [
        "reasoning",
        "planning",
        "review",
        "execution",
        "reflection",
        "day-dream",
        "deep-sleep",
    ]
    for role in on_demand:
        path = workers_dir / f"{role}.yaml"
        if not path.exists():
            content = (
                f"# On-demand worker: {role}\n"
                f"name: {role}\n"
                f"role: {role}\n"
                f"scope: on-demand\n"
            )
            path.write_text(content, encoding="utf-8")
            _write_manifest(
                mf,
                f"global/workers/{role}.yaml",
                f"global/workers/{role}.yaml",
                "seed",
                sha256_file(path),
                path.stat().st_size,
            )


def _seed_agents(root: Path, mf: TextIO) -> None:
    for agent in AGENTS:
        agent_dir = root / "agents" / agent
        for d in PAYLOAD_DIRS:
            (agent_dir / d).mkdir(parents=True, exist_ok=True)

        context_dir = agent_dir / "context"
        context_dir.mkdir(parents=True, exist_ok=True)
        (context_dir / "archive").mkdir(parents=True, exist_ok=True)

        # Rommie gets a light real persona seed; the four provider agents stay thin.
        if agent == "rommie":
            goal = context_dir / "goal.md"
            goal.write_text(
                "# Rommie\n\n"
                "You are Rommie, the native Andromeda agent. "
                "You coordinate a concurrent worker-brain over a shared blackboard, "
                "speak with one user-facing conversation voice, and learn from every session.\n",
                encoding="utf-8",
            )
            _write_manifest(
                mf,
                "agents/rommie/context/goal.md",
                "agents/rommie/context/goal.md",
                "seed",
                sha256_file(goal),
                goal.stat().st_size,
            )
            persona = agent_dir / "memories" / "persona.md"
            if not persona.exists():
                persona.write_text(
                    "# Persona seed\n\n"
                    "Traits: curious, careful, concise, honest about uncertainty.\n",
                    encoding="utf-8",
                )
                _write_manifest(
                    mf,
                    "agents/rommie/memories/persona.md",
                    "agents/rommie/memories/persona.md",
                    "seed",
                    sha256_file(persona),
                    persona.stat().st_size,
                )

        for fname in CONTEXT_FILES:
            fpath = context_dir / fname
            if not fpath.exists():
                content = f"# {fname} — context-cascade scaffold for {agent}\n"
                fpath.write_text(content, encoding="utf-8")
                _write_manifest(
                    mf,
                    f"agents/{agent}/context/{fname}",
                    f"agents/{agent}/context/{fname}",
                    "seed",
                    sha256_file(fpath),
                    fpath.stat().st_size,
                )


def _seed_clis(root: Path) -> None:
    for cli in ("claude", "codex", "kimi", "agy"):
        (root / "clis" / cli).mkdir(parents=True, exist_ok=True)


def _tag_sessions(root: Path, mf: TextIO) -> None:
    sessions_dir = root / "shared" / "sessions"
    if not sessions_dir.exists():
        return
    meta = sessions_dir / ".rommie-agent-tag"
    meta.write_text("agent: rommie\n", encoding="utf-8")
    _write_manifest(
        mf,
        _rel_posix(meta.relative_to(root)),
        _rel_posix(meta.relative_to(root)),
        "seed",
        sha256_file(meta),
        meta.stat().st_size,
    )
    for session in _iter_dir(sessions_dir):
        if session.is_dir():
            tag = session / ".agent"
            if not tag.exists():
                tag.write_text("rommie\n", encoding="utf-8")


def _ensure_node_yaml(root: Path, mf: TextIO) -> None:
    node = root / "node.yaml"
    if not node.exists():
        node.write_text("role: client\n", encoding="utf-8")
        _write_manifest(
            mf,
            "node.yaml",
            "node.yaml",
            "seed",
            sha256_file(node),
            node.stat().st_size,
        )


def _seed_fresh(root: Path, mf: TextIO) -> None:
    """Create the D-025 fresh seeding (additive only)."""
    _seed_global_roles(root, mf)
    _seed_global_workers(root, mf)
    _seed_agents(root, mf)
    _seed_clis(root)
    _tag_sessions(root, mf)
    _ensure_node_yaml(root, mf)
    (root / "domains").mkdir(parents=True, exist_ok=True)


def migrate(root: Path, apply: bool = False) -> tuple[list[Operation], Path | None]:
    """Classify and optionally execute the CAP-02 migration.

    Returns ``(plan, manifest_path)``.  ``manifest_path`` is ``None`` in dry-run.
    """
    root = root.expanduser().resolve()
    plan = collect_plan(root)

    if not apply:
        return plan, None

    manifest_path = root / MANIFEST_DIR / f"cap02-{_manifest_ts()}.jsonl"
    manifest_path.parent.mkdir(parents=True, exist_ok=True)

    with manifest_path.open("w", encoding="utf-8") as mf:
        if not plan:
            _write_manifest(mf, ".", None, "noop", note="already migrated")
        else:
            # Move/archive/quarantine legacy content BEFORE creating final dirs that
            # share top-level names (e.g. legacy agents/ must become archive/agents/
            # before the fresh agents/ is seeded).
            for op in plan:
                # seed-keep / keep items are already in their final location.
                if op.rel_dst == op.rel_src:
                    continue
                _execute(op, root, mf)

            # Ensure the rest of the final schema exists and seed fresh content.
            _ensure_final_dirs(root)
            _seed_fresh(root, mf)

        # Idempotency marker.
        marker = root / MARKER_NAME
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_text(
            json.dumps({"cap": "02", "migrated_at": _now()}, indent=2),
            encoding="utf-8",
        )

    return plan, manifest_path


def summarize(plan: list[Operation]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for op in plan:
        counts[op.disposition] = counts.get(op.disposition, 0) + 1
    return counts


def format_plan(plan: list[Operation], applied: bool = False) -> str:
    lines = [
        "CAP-02 migration plan"
        + (" (dry-run)" if not applied else " executed"),
    ]
    counts = summarize(plan)
    if not plan:
        lines.append("No operations needed (already migrated or empty).")
        return "\n".join(lines)

    lines.append("Per-disposition counts:")
    for disp in sorted(counts):
        lines.append(f"  {disp}: {counts[disp]}")

    lines.append("")
    lines.append("Operations:")
    for op in plan:
        if op.rel_dst:
            arrow = f"{op.rel_src} -> {op.rel_dst}"
        else:
            arrow = f"{op.rel_src} -> [DROP]"
        note = f"  # {op.note}" if op.note else ""
        lines.append(f"  [{op.disposition}] {arrow}{note}")
    return "\n".join(lines)
