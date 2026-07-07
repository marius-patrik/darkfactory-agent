"""Local secret materialization and repository plaintext audit helpers."""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Mapping, Protocol

from agent.redaction import Redactor

_DEFAULT_ROOT = "~/.rommie/secrets"
_SKIP_DIRS = {".git", "node_modules", ".venv", "__pycache__"}
_SKIP_SUFFIXES = {".lock"}


class SecretSource(Protocol):
    """Source seam for loading secrets before materialization."""

    def load(self) -> dict[str, str]:
        """Load secrets keyed by materialized credential name.

        Returns:
            A dictionary of names to secret values.
        """
        ...


@dataclass(frozen=True)
class MappingSource:
    """Secret source backed by an in-memory mapping."""

    values: Mapping[str, str] = field(default_factory=dict)

    def load(self) -> dict[str, str]:
        """Load a copy of the configured mapping."""
        return dict(self.values)


@dataclass(frozen=True)
class EnvSource:
    """Secret source backed by environment variables."""

    prefix: str = "ROMMIE_SECRET_"

    def load(self) -> dict[str, str]:
        """Load prefixed environment variables with the prefix stripped."""
        return {
            key.removeprefix(self.prefix): value
            for key, value in os.environ.items()
            if key.startswith(self.prefix)
        }


@dataclass
class AuditReport:
    """Secret materialization audit result.

    Attributes:
        ok: True when no violations were found.
        violations: Human-readable path and mode violations, never contents.
    """

    ok: bool
    violations: list[str] = field(default_factory=list)


def materialize(secrets: Mapping[str, str], root: str | Path = _DEFAULT_ROOT) -> list[Path]:
    """Materialize secrets under ``root/credentials``.

    Args:
        secrets: Mapping of credential names to secret values.
        root: Materialized secrets root.

    Returns:
        Paths written.

    Raises:
        OSError: If directory creation or file replacement fails.
    """
    root_path = Path(root).expanduser()
    credentials_path = root_path / "credentials"
    root_path.mkdir(mode=0o700, parents=True, exist_ok=True)
    credentials_path.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(root_path, 0o700)
    os.chmod(credentials_path, 0o700)

    written: list[Path] = []
    for name, value in secrets.items():
        target = credentials_path / name
        tmp = credentials_path / f".{name}.tmp.{os.getpid()}"
        try:
            fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                handle.write(value)
            os.chmod(tmp, 0o600)
            os.replace(tmp, target)
            os.chmod(target, 0o600)
            written.append(target)
        except OSError:
            try:
                tmp.unlink(missing_ok=True)
            except OSError:
                pass
            raise
    return written


def resolve_ref(ref: str, root: str | Path = _DEFAULT_ROOT) -> str:
    """Resolve a ``secret:NAME`` reference to a materialized value.

    Args:
        ref: Secret reference in ``secret:NAME`` form.
        root: Materialized secrets root.

    Returns:
        The materialized secret value.

    Raises:
        KeyError: If the reference is malformed or absent.
    """
    if not ref.startswith("secret:"):
        raise KeyError("Secret reference must use secret:NAME form.")
    name = ref.removeprefix("secret:")
    path = Path(root).expanduser() / "credentials" / name
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:
        raise KeyError(f"Secret {name!r} is not materialized.") from exc


def audit(root: str | Path = _DEFAULT_ROOT) -> AuditReport:
    """Audit materialized secret permissions.

    Args:
        root: Materialized secrets root.

    Returns:
        Audit report listing path and mode violations.
    """
    root_path = Path(root).expanduser()
    violations: list[str] = []
    if not root_path.exists():
        return AuditReport(ok=True, violations=violations)
    _require_mode(root_path, 0o700, violations)
    credentials_path = root_path / "credentials"
    if credentials_path.exists():
        _require_mode(credentials_path, 0o700, violations)
    for path in root_path.rglob("*"):
        if path.is_dir():
            _require_mode(path, 0o700, violations)
        elif path.is_file():
            _require_mode(path, 0o600, violations)
    return AuditReport(ok=not violations, violations=violations)


def scan_repo_for_plaintext(repo_root: str | Path, redactor: Redactor | None = None) -> list[str]:
    """Scan a repository tree for plaintext secret patterns.

    Args:
        repo_root: Repository root to scan.
        redactor: Redactor to use. Defaults to pattern-only redaction.

    Returns:
        ``path:lineno:type`` strings for detected secrets, never values.
    """
    root = Path(repo_root)
    scanner = redactor or Redactor()
    violations: list[str] = []
    for path in _iter_text_files(root):
        try:
            with path.open("r", encoding="utf-8") as handle:
                for index, line in enumerate(handle, start=1):
                    for finding in scanner.findings(line):
                        violations.append(f"{path}:{index}:{finding.type}")
        except UnicodeDecodeError:
            continue
        except OSError:
            continue
    return violations


def _require_mode(path: Path, expected: int, violations: list[str]) -> None:
    if os.name == "nt":
        return
    mode = path.stat().st_mode & 0o777
    if mode != expected:
        violations.append(f"{path}: mode {mode:03o}, expected {expected:03o}")


def _iter_text_files(root: Path) -> list[Path]:
    files: list[Path] = []
    for current_root, dirnames, filenames in os.walk(root):
        dirnames[:] = [name for name in dirnames if name not in _SKIP_DIRS]
        current_path = Path(current_root)
        for filename in filenames:
            path = current_path / filename
            if path.suffix in _SKIP_SUFFIXES:
                continue
            files.append(path)
    return files
