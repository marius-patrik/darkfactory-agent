"""Tests for the capability/skills loader + registry v2 (Claude-format)."""

from __future__ import annotations

import os
import uuid
from pathlib import Path

import pytest

from agent.capabilities import (
    CapabilityKind,
    CapabilityRegistry,
    ValidationError,
    discover_all,
    execute,
    load_all,
    parse_manifest,
)
from agent.capabilities.discovery import CapabilityRecord, discover
from agent.capabilities.execute import ExecutionResult
from agent.capabilities.registry import _jsonb
from agent.exec_lane.contract import LANE_KNATIVE, ExecSpec, get_lane


REPO_ROOT = Path(__file__).resolve().parents[2]
PLUGINS_ROOT = REPO_ROOT / "plugins"


def test_discovery_finds_three_samples():
    registry = load_all([PLUGINS_ROOT])

    names = {(m.name, m.kind) for m in registry.list()}

    assert ("hello-skill", "skill") in names
    assert ("sample-plugin", "plugin") in names
    assert ("echo", "script") in names


def test_default_discovery_includes_agents_home(monkeypatch, tmp_path):
    skill_dir = tmp_path / "skills" / "shared-skill"
    skill_dir.mkdir(parents=True)
    (skill_dir / "SKILL.md").write_text(
        "---\nname: shared-skill\nversion: 0.1.0\ndescription: installed by agents\n---\n\nbody\n",
        encoding="utf-8",
    )
    monkeypatch.setenv("AGENTS_HOME", str(tmp_path))
    monkeypatch.setenv("ROMMIE_HOME", str(tmp_path / "missing-rommie"))

    names = {(record.name, record.kind) for record in discover_all()}

    assert ("shared-skill", "skill") in names


def test_manifest_validation_passes_samples():
    for record in discover_all([PLUGINS_ROOT]):
        manifest = parse_manifest(record)
        assert manifest.name
        assert manifest.version
        assert manifest.exec_lane in ("daemon", "knative")


def test_manifest_rejects_missing_name(tmp_path):
    bad_skill = tmp_path / "skills" / "bad-skill"
    bad_skill.mkdir(parents=True)
    (bad_skill / "SKILL.md").write_text(
        "---\nversion: 0.1.0\ndescription: missing name\n---\n\nbody\n",
        encoding="utf-8",
    )

    records = discover_all([tmp_path])
    assert records
    with pytest.raises(ValidationError) as exc_info:
        parse_manifest(records[0])

    assert exc_info.value.field == "name"


def test_manifest_rejects_bad_version(tmp_path):
    bad_skill = tmp_path / "skills" / "bad-version"
    bad_skill.mkdir(parents=True)
    (bad_skill / "SKILL.md").write_text(
        "---\nname: bad-version\nversion: not-a-version\n---\n\nbody\n",
        encoding="utf-8",
    )

    records = discover_all([tmp_path])
    with pytest.raises(ValidationError) as exc_info:
        parse_manifest(records[0])

    assert exc_info.value.field == "version"


def test_registry_get_and_list():
    registry = load_all([PLUGINS_ROOT])

    hello = registry.get("hello-skill", "skill")
    assert hello is not None
    assert hello.name == "hello-skill"
    assert hello.exec_lane == "daemon"

    plugins = registry.list(kind="plugin")
    assert len(plugins) == 1
    assert plugins[0].name == "sample-plugin"

    scripts = registry.list(kind="script")
    assert len(scripts) == 1
    assert scripts[0].name == "echo"


def test_execute_instruction_skill_returns_body():
    registry = load_all([PLUGINS_ROOT])
    hello = registry.get("hello-skill", "skill")

    result = execute(hello)

    assert isinstance(result, ExecutionResult)
    assert result.exec_lane == "daemon"
    assert "greet the user warmly" in result.output
    assert result.sub_capabilities == ["echo"]


def test_execute_command_substitutes_arguments():
    registry = load_all([PLUGINS_ROOT])
    echo_cmd = registry.get("echo", "script")

    result = execute(echo_cmd, arguments="hello world")

    assert result.exec_lane == "daemon"
    assert "hello world" in result.output
    assert "$ARGUMENTS" not in result.output


def test_execute_script_skill_routes_to_daemon(tmp_path):
    if os.name == "nt":
        pytest.skip("POSIX executable bit behavior is not portable to Windows")

    skill_dir = tmp_path / "skills" / "scripted-skill"
    skill_dir.mkdir(parents=True)
    script = skill_dir / "run.sh"
    script.write_text("#!/bin/bash\necho \"ran: $1\"\n", encoding="utf-8")
    script.chmod(0o755)
    (skill_dir / "SKILL.md").write_text(
        "---\nname: scripted-skill\nversion: 0.1.0\ndescription: wraps a script\nscript: run.sh\n---\n\nbody\n",
        encoding="utf-8",
    )

    records = discover_all([tmp_path])
    assert len(records) == 1
    manifest = parse_manifest(records[0])

    assert manifest.local_script == script
    assert manifest.exec_lane == "daemon"

    result = execute(manifest, arguments="test-arg")
    assert result.exec_lane == "daemon"
    assert "ran: test-arg" in result.output


def test_execute_nonexecutable_script_routes_without_exec(tmp_path):
    skill_dir = tmp_path / "skills" / "scripted-skill"
    skill_dir.mkdir(parents=True)
    script = skill_dir / "run.sh"
    script.write_text("#!/bin/bash\necho nope\n", encoding="utf-8")
    script.chmod(0o644)  # not executable
    (skill_dir / "SKILL.md").write_text(
        "---\nname: scripted-skill\nversion: 0.1.0\nscript: run.sh\n---\n\nbody\n",
        encoding="utf-8",
    )

    records = discover_all([tmp_path])
    manifest = parse_manifest(records[0])
    assert manifest.local_script is None

    result = execute(manifest)
    assert result.exec_lane == "daemon"
    assert result.output == "body"


def test_execute_knative_lane_deferred():
    registry = CapabilityRegistry()
    from agent.capabilities.manifest import CapabilityManifest

    manifest = CapabilityManifest(
        kind="skill",
        name="cloud-skill",
        version="0.1.0",
        description="deferred",
        path=Path("/tmp"),
        origin="template",
        exec_lane="knative",
        body="remote body",
    )
    registry.add(manifest)

    result = execute(registry.get("cloud-skill", "skill"))

    assert result.exec_lane == "knative"
    assert result.routing_only is True


def test_knative_contract_lane_is_reserved_with_clear_message():
    lane = get_lane(LANE_KNATIVE)

    with pytest.raises(
        NotImplementedError,
        match="Knative execution lane is reserved for post-4.0",
    ):
        lane.submit(ExecSpec(command=["echo", "deferred"]))


@pytest.mark.asyncio
async def test_sync_to_pg_skips_without_dsn(monkeypatch):
    monkeypatch.delenv("ROMMIE_PG_DSN", raising=False)
    registry = load_all([PLUGINS_ROOT])

    result = await registry.sync_to_pg(dsn="")

    assert result["skipped"] is True
    assert result["upserted"] == 0


@pytest.mark.asyncio
async def test_sync_to_pg_idempotent_when_live(monkeypatch):
    dsn = os.environ.get("ROMMIE_PG_DSN")
    if not dsn:
        pytest.skip("ROMMIE_PG_DSN not set")

    registry = load_all([PLUGINS_ROOT])
    # Use a unique session-scoped namespace to avoid collisions across runs
    namespace = os.environ.get("PYTEST_CURRENT_TEST", "test") + str(uuid.uuid4())[:8]

    # Prefix names so we can clean up and verify idempotency in isolation
    manifests = list(registry._by_key.values())
    assert manifests

    result1 = await registry.sync_to_pg(dsn=dsn)
    result2 = await registry.sync_to_pg(dsn=dsn)

    assert result1["skipped"] is False
    assert result2["skipped"] is False
    assert result1["upserted"] == result2["upserted"]


def test_jsonb_helper():
    import json

    assert json.loads(_jsonb({"a": 1})) == {"a": 1}
