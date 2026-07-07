"""CAP-02 migrator tests.

Builds a synthetic mini-rommie covering every mapping-table row, verifies the
dry-run classification, applies the migration, asserts the final tree matches
the §19 schema, and checks idempotency.
"""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from agent.migrations import cap02


def _write(root: Path, rel: str, content: str) -> Path:
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(content, encoding="utf-8")
    return p


def build_fixture(root: Path) -> None:
    # ACTIVE state
    _write(root, "sessions/s1/log.json", '{"t":1}')
    _write(root, "sessions/s2/log.json", '{"t":2}')
    _write(root, "config/config.yaml", "x: 1")
    _write(root, "cluster/nodes.yaml", "nodes: []")
    _write(root, "secrets/credentials/claude.json", '{"k":"v"}')

    # SEED-KEEP global payload
    _write(root, "global/skills/greet.yaml", "greet")
    _write(root, "global/plugins/p.yaml", "p")
    _write(root, "global/hooks/h.yaml", "h")
    _write(root, "global/scripts/run.sh", "#!/bin/sh")
    _write(root, "global/prompts/system.md", "system")
    _write(root, "global/memories/lesson.md", "lesson")
    _write(root, "global/commands/cmd.yaml", "cmd")
    _write(root, "global/rules/rule.md", "rule")

    # ARCHIVE global superseded classes
    _write(root, "global/plans/plan.md", "plan")
    _write(root, "global/tasks/task.md", "task")
    _write(root, "global/handoffs/hand.md", "hand")
    _write(root, "global/unknown/x.txt", "x")  # unclassifiable

    # Top-level skill-like dirs merge into global/
    _write(root, "skills/extra.yaml", "extra")
    _write(root, "plugins/bar.yaml", "bar")
    _write(root, "hooks/post.yaml", "post")

    # KEEP RAW history archive
    _write(root, "history-archive/mac/.claude/hist.json", "[]")

    # hermes-agent: mine memories, archive the rest
    _write(root, "hermes-agent/memories/hermes.md", "hermes memory")
    _write(root, "hermes-agent/cache/big.bin", "binary")
    _write(root, "hermes-agent/config.yaml", "cfg")

    # LEGACY operational -> archive
    legacy_dirs = [
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
    ]
    for d in legacy_dirs:
        _write(root, f"{d}/file.txt", d)
    _write(root, "snapshots-2025/snap.txt", "snap")

    # Already-archived -> leave alone
    _write(root, "archive/oldarchive/data.md", "old")

    # GENERATED -> drop
    _write(root, ".venv/bin/python", "fake")
    _write(root, "node_modules/pkg/index.js", "js")
    _write(root, "cache/file.cache", "c")
    _write(root, "__pycache__/mod.pyc", "pyc")
    _write(root, "session.sqlite-wal", "wal")
    _write(root, "session.sqlite-shm", "shm")

    # Loose markdown + legacy dirs
    for name in ("agent.md", "context.md", "memory.md", "work.md"):
        _write(root, name, name)

    # Unclassifiable -> quarantine
    _write(root, "weird/unknown.txt", "?")

    # node.yaml preserved
    _write(root, "node.yaml", "role: desktop\n")


def _plan_map(plan: list[cap02.Operation]) -> dict[str, cap02.Operation]:
    return {op.rel_src: op for op in plan}


def test_dry_run_classifies_every_mapping_row(tmp_path: Path) -> None:
    build_fixture(tmp_path)
    plan = cap02.collect_plan(tmp_path)
    by_src = _plan_map(plan)

    # ACTIVE
    assert by_src["sessions"].disposition == "active"
    assert by_src["sessions"].rel_dst == "shared/sessions"
    assert by_src["config"].disposition == "active"
    assert by_src["cluster"].disposition == "active"
    assert by_src["secrets"].disposition == "keep"
    assert by_src["history-archive"].disposition == "active"

    # SEED-KEEP global payload
    for name in (
        "skills",
        "plugins",
        "hooks",
        "scripts",
        "prompts",
        "memories",
        "commands",
        "rules",
    ):
        assert by_src[f"global/{name}"].disposition == "seed-keep"

    # ARCHIVE global superseded classes
    for name in ("plans", "tasks", "handoffs"):
        assert by_src[f"global/{name}"].disposition == "archive"

    # Unclassifiable inside global
    assert by_src["global/unknown"].disposition == "quarantine"

    # Top-level merge
    assert by_src["skills/extra.yaml"].disposition == "seed-merge"
    assert by_src["plugins/bar.yaml"].disposition == "seed-merge"
    assert by_src["hooks/post.yaml"].disposition == "seed-merge"

    # hermes-agent
    assert by_src["hermes-agent/memories/hermes.md"].disposition == "active"
    assert (
        by_src["hermes-agent/memories/hermes.md"].rel_dst
        == "global/memories/hermes/memories/hermes.md"
    )
    assert by_src["hermes-agent/cache/big.bin"].disposition == "archive"
    assert by_src["hermes-agent/config.yaml"].disposition == "archive"

    # Legacy operational dirs
    for name in (
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
    ):
        assert by_src[name].disposition == "archive"
        assert by_src[name].rel_dst == f"archive/{name}"
    assert by_src["snapshots-2025"].disposition == "archive"

    # Loose .md / legacy dirs
    for name in ("agent.md", "context.md", "memory.md", "work.md"):
        assert by_src[name].disposition == "archive"

    # Already archived -> keep
    assert by_src["archive"].disposition == "keep"

    # GENERATED -> drop
    for name in (".venv", "node_modules", "cache", "__pycache__"):
        assert by_src[name].disposition == "drop"
        assert by_src[name].rel_dst is None
    for name in ("session.sqlite-wal", "session.sqlite-shm"):
        assert by_src[name].disposition == "drop"

    # Unclassifiable top-level -> quarantine
    assert by_src["weird"].disposition == "quarantine"

    # node.yaml preserved
    assert by_src["node.yaml"].disposition == "keep"


def test_apply_produces_final_schema_and_manifest(tmp_path: Path) -> None:
    build_fixture(tmp_path)
    plan = cap02.collect_plan(tmp_path)

    # Capture the source paths *before* the migration runs (zero-loss check).
    moved_dispositions = {"active", "seed-merge", "archive", "quarantine"}
    moved_sources: set[str] = set()
    for op in plan:
        if op.disposition not in moved_dispositions:
            continue
        src = tmp_path / op.rel_src
        if src.is_file():
            moved_sources.add(op.rel_src)
        elif src.is_dir():
            moved_sources.update(
                p.relative_to(tmp_path).as_posix()
                for p in src.rglob("*")
                if p.is_file()
            )

    plan, manifest_path = cap02.migrate(tmp_path, apply=True)

    assert manifest_path is not None
    assert manifest_path.exists()

    # ACTIVE moved to shared/
    assert (tmp_path / "shared" / "sessions" / "s1" / "log.json").exists()
    assert (tmp_path / "shared" / "sessions" / "s2" / "log.json").exists()
    assert (tmp_path / "shared" / "config" / "config.yaml").exists()
    assert (tmp_path / "shared" / "cluster" / "nodes.yaml").exists()
    assert (tmp_path / "secrets" / "credentials" / "claude.json").exists()

    # Global payload merged
    assert (tmp_path / "global" / "skills" / "greet.yaml").exists()
    assert (tmp_path / "global" / "skills" / "extra.yaml").exists()
    assert (tmp_path / "global" / "plugins" / "p.yaml").exists()
    assert (tmp_path / "global" / "plugins" / "bar.yaml").exists()
    assert (tmp_path / "global" / "hooks" / "h.yaml").exists()
    assert (tmp_path / "global" / "hooks" / "post.yaml").exists()
    assert (tmp_path / "global" / "scripts" / "run.sh").exists()
    assert (tmp_path / "global" / "prompts" / "system.md").exists()
    assert (tmp_path / "global" / "memories" / "lesson.md").exists()
    assert (
        tmp_path / "global" / "memories" / "hermes" / "memories" / "hermes.md"
    ).exists()
    assert (tmp_path / "global" / "commands" / "cmd.yaml").exists()
    assert (tmp_path / "global" / "rules" / "rule.md").exists()

    # Raw history preserved
    assert (
        tmp_path / "shared" / "history-archive" / "mac" / ".claude" / "hist.json"
    ).exists()

    # Archive
    assert (tmp_path / "archive" / "projects" / "file.txt").exists()
    assert (tmp_path / "archive" / "global" / "plans" / "plan.md").exists()
    assert (tmp_path / "archive" / "loose" / "agent.md").exists()
    assert (tmp_path / "archive" / "hermes-agent" / "cache" / "big.bin").exists()
    assert (tmp_path / "archive" / "oldarchive" / "data.md").exists()

    # Quarantine
    assert (tmp_path / "quarantine" / "weird" / "unknown.txt").exists()
    assert (tmp_path / "quarantine" / "global" / "unknown" / "x.txt").exists()

    # Generated dropped
    assert not (tmp_path / ".venv").exists()
    assert not (tmp_path / "node_modules").exists()
    assert not (tmp_path / "cache").exists()
    assert not (tmp_path / "__pycache__").exists()
    assert not (tmp_path / "session.sqlite-wal").exists()
    assert not (tmp_path / "session.sqlite-shm").exists()

    # Fresh seeding: 10 standard roles
    for role, _scope in cap02.STANDARD_ROLES:
        assert (tmp_path / "global" / "roles" / f"{role}.yaml").exists()

    # Fresh seeding: default workers
    workers = [
        "conversation",
        "thought",
        "reasoning",
        "planning",
        "review",
        "execution",
        "reflection",
        "day-dream",
        "deep-sleep",
    ]
    for w in workers:
        assert (tmp_path / "global" / "workers" / f"{w}.yaml").exists()

    # Fresh seeding: 5 agents with payload shape + context cascade
    for agent in cap02.AGENTS:
        agent_dir = tmp_path / "agents" / agent
        assert agent_dir.is_dir()
        for d in cap02.PAYLOAD_DIRS:
            assert (agent_dir / d).is_dir()
        for fname in cap02.CONTEXT_FILES:
            assert (agent_dir / "context" / fname).exists()
        assert (agent_dir / "context" / "archive").is_dir()

    # Rommie light persona seed
    goal = tmp_path / "agents" / "rommie" / "context" / "goal.md"
    assert goal.exists()
    assert "Rommie" in goal.read_text()
    assert (tmp_path / "agents" / "rommie" / "memories" / "persona.md").exists()

    # CLI homes and post-4.0 domains dir
    for cli in ("claude", "codex", "kimi", "agy"):
        assert (tmp_path / "clis" / cli).is_dir()
    assert (tmp_path / "domains").is_dir()

    # Final runtime dirs
    for d in ("runtime", "models", "logs", "metrics", "manifests", "quarantine", "tmp"):
        assert (tmp_path / d).is_dir()

    # node.yaml preserved
    assert (tmp_path / "node.yaml").read_text() == "role: desktop\n"

    # Sessions tagged as rommie
    assert (
        tmp_path / "shared" / "sessions" / ".rommie-agent-tag"
    ).read_text() == "agent: rommie\n"
    for sid in ("s1", "s2"):
        assert (tmp_path / "shared" / "sessions" / sid / ".agent").read_text() == "rommie\n"

    # Idempotency marker
    assert (tmp_path / "runtime" / "cap02-migrated.json").exists()

    # Manifest: every moved file is logged with a hash (zero loss).
    entries = [json.loads(line) for line in manifest_path.read_text().splitlines()]

    hashed_sources = {
        e["src"]
        for e in entries
        if e["disposition"] in moved_dispositions and "sha256" in e
    }
    assert hashed_sources == moved_sources, "manifest hash count must equal moved files"


def test_re_run_is_noop(tmp_path: Path) -> None:
    build_fixture(tmp_path)
    cap02.migrate(tmp_path, apply=True)

    plan2, manifest2 = cap02.migrate(tmp_path, apply=True)
    assert plan2 == []
    assert manifest2 is not None
    assert manifest2.exists()

    lines = manifest2.read_text().splitlines()
    assert lines
    entry = json.loads(lines[0])
    assert entry["disposition"] == "noop"
