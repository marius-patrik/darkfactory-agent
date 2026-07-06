"""Small Docker CLI helpers for engine adapters."""

from __future__ import annotations

import re
import subprocess
from collections.abc import Callable, Sequence


Runner = Callable[..., subprocess.CompletedProcess[str]]


class DockerError(RuntimeError):
    pass


class DockerClient:
    def __init__(self, runner: Runner | None = None) -> None:
        self._runner = runner or subprocess.run

    def run(self, args: Sequence[str], *, check: bool = False) -> subprocess.CompletedProcess[str]:
        return self._runner(
            list(args),
            check=check,
            text=True,
            capture_output=True,
        )

    def exists(self, name: str) -> bool:
        result = self.run(["docker", "inspect", name])
        return result.returncode == 0

    def running(self, name: str) -> bool:
        result = self.run(
            ["docker", "inspect", "-f", "{{.State.Running}}", name]
        )
        return result.returncode == 0 and result.stdout.strip() == "true"

    def remove(self, name: str) -> None:
        require_engine_container(name)
        self.run(["docker", "rm", "-f", name], check=True)

    def logs(self, name: str) -> str:
        require_engine_container(name)
        result = self.run(["docker", "logs", "--tail", "200", name])
        return (result.stdout or "") + (result.stderr or "")

    def ensure_model(
        self,
        name: str,
        run_args: Sequence[str],
        health_check: Callable[[], bool],
    ) -> bool:
        """Ensure an engine container exists.

        Returns True when a new container was started, False for healthy no-op.
        """
        require_engine_container(name)
        if self.exists(name):
            if self.running(name) and health_check():
                return False
            self.remove(name)
        self.run(run_args, check=True)
        return True

    def unload_model(self, name: str) -> None:
        require_engine_container(name)
        if self.exists(name):
            self.remove(name)


def container_name(engine_id: str) -> str:
    safe = re.sub(r"[^A-Za-z0-9_.-]+", "-", engine_id).strip("-")
    if not safe:
        raise ValueError("engine id must contain at least one safe character")
    return f"rommie-engine-{safe}"


def require_engine_container(name: str) -> None:
    if not name.startswith("rommie-engine-"):
        raise ValueError(f"refusing to manage non-engine container {name!r}")
