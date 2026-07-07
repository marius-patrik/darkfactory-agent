"""Tests for local secret materialization and audits."""

from __future__ import annotations

import os

from agent.redaction import Redactor
from agent.secrets import EnvSource, MappingSource, audit, materialize, resolve_ref, scan_repo_for_plaintext


def test_materialize_writes_secure_files_and_resolves_refs(tmp_path):
    root = tmp_path / "secrets"
    value = "round-trip-secret-value"

    paths = materialize({"PROVIDER": value}, root=root)

    assert paths == [root / "credentials" / "PROVIDER"]
    if os.name != "nt":
        assert (root.stat().st_mode & 0o777) == 0o700
        assert ((root / "credentials").stat().st_mode & 0o777) == 0o700
        assert (paths[0].stat().st_mode & 0o777) == 0o600
    assert resolve_ref("secret:PROVIDER", root=root) == value
    assert not list((root / "credentials").glob("*.tmp.*"))


def test_audit_ok_and_flags_bad_file_without_contents(tmp_path):
    root = tmp_path / "secrets"
    secret_value = "do-not-report-this"
    materialize({"GOOD": "value"}, root=root)
    bad = root / "credentials" / "BAD"
    bad.write_text(secret_value, encoding="utf-8")
    os.chmod(bad, 0o644)

    report = audit(root=root)

    if os.name == "nt":
        assert report.ok
        assert secret_value not in repr(report)
        return

    assert not report.ok
    assert str(bad) in report.violations[0]
    assert "644" in report.violations[0]
    assert secret_value not in repr(report)

    os.chmod(bad, 0o600)
    assert audit(root=root).ok


def test_scan_repo_for_plaintext_clean_and_planted(tmp_path):
    clean = tmp_path / "clean.py"
    clean.write_text("token = os.environ.get('TOKEN')\n", encoding="utf-8")
    assert scan_repo_for_plaintext(tmp_path, redactor=Redactor()) == []

    token = "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE"
    planted = tmp_path / "planted.txt"
    planted.write_text(f"do not persist {token}\n", encoding="utf-8")

    violations = scan_repo_for_plaintext(tmp_path, redactor=Redactor())

    assert violations == [f"{planted}:1:github-token"]
    assert token not in repr(violations)


def test_sources_load_round_trip(monkeypatch):
    monkeypatch.setenv("ROMMIE_SECRET_ALPHA", "one")
    monkeypatch.setenv("OTHER_SECRET_BETA", "two")

    assert MappingSource({"A": "B"}).load() == {"A": "B"}
    assert EnvSource().load() == {"ALPHA": "one"}
