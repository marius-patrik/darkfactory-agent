"""Minimal VS2 capability execution."""

from __future__ import annotations

import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal

from agent.capabilities.manifest import CapabilityManifest


@dataclass(frozen=True, slots=True)
class ExecutionResult:
    """Result of invoking a capability."""

    exec_lane: Literal["daemon", "knative"]
    output: str = ""
    routing_only: bool = False
    sub_capabilities: list[str] | None = None
    local_script: Path | None = None


def _substitute_arguments(body: str, arguments: str | None) -> str:
    if arguments is None:
        return body
    return body.replace("$ARGUMENTS", arguments)


def execute(
    manifest: CapabilityManifest,
    arguments: str | None = None,
    *,
    allow_local_script: bool = True,
) -> ExecutionResult:
    """Execute a capability minimally.

    - Instruction-only skill: returns rendered body + sub-capability list.
    - Command: returns body with ``$ARGUMENTS`` substituted.
    - Script-wrapped skill/hook: if a local executable script exists inside the
      capability dir, execute it in the daemon lane; otherwise return the routing
      decision (exec_lane=daemon) without executing arbitrary host commands.
    - Knative-lane capabilities are deferred: record exec_lane=knative and return.
    """
    if manifest.exec_lane == "knative":
        return ExecutionResult(
            exec_lane="knative",
            output="",
            routing_only=True,
            sub_capabilities=manifest.sub_capabilities,
        )

    # Script-wrapped skill/hook
    if manifest.local_script is not None and allow_local_script:
        if manifest.local_script.exists() and manifest.local_script.is_file():
            try:
                proc = subprocess.run(
                    [str(manifest.local_script), arguments or ""],
                    capture_output=True,
                    text=True,
                    timeout=30,
                    cwd=str(manifest.path),
                )
                output = (proc.stdout + proc.stderr).strip()
                return ExecutionResult(
                    exec_lane="daemon",
                    output=output,
                    local_script=manifest.local_script,
                )
            except subprocess.TimeoutExpired:
                return ExecutionResult(
                    exec_lane="daemon",
                    output="[timeout]",
                    routing_only=True,
                    local_script=manifest.local_script,
                )
            except Exception as exc:  # pragma: no cover - defensive
                return ExecutionResult(
                    exec_lane="daemon",
                    output=f"[exec error: {exc}]",
                    routing_only=True,
                    local_script=manifest.local_script,
                )

    # Instruction-only skill / hook body
    if manifest.kind == "skill" or manifest.kind == "hook":
        return ExecutionResult(
            exec_lane="daemon",
            output=_substitute_arguments(manifest.body, arguments),
            sub_capabilities=manifest.sub_capabilities,
        )

    # Command (script-kind invocable)
    if manifest.kind == "script":
        return ExecutionResult(
            exec_lane="daemon",
            output=_substitute_arguments(manifest.body, arguments),
        )

    # Plugin / extension: routing-only in daemon lane for VS2
    return ExecutionResult(
        exec_lane="daemon",
        output=manifest.description,
        routing_only=True,
        sub_capabilities=manifest.sub_capabilities,
    )
