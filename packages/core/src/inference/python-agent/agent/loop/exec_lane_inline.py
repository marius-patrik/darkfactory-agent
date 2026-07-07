"""Inline execution lane for VS2 tool dispatch."""

from __future__ import annotations

import json
from datetime import UTC, datetime
from typing import Any, Iterator
from uuid import uuid4

from agent.exec_lane.contract import (
    LANE_DAEMON_INLINE,
    ExecHandle,
    ExecSpec,
    ExecStatus,
    register_lane,
)
from agent.loop.tools import TOOLS


class DaemonInlineLane:
    """Synchronous host-bound execution lane for in-process tool calls."""

    def __init__(self) -> None:
        self._results: dict[str, dict[str, Any]] = {}
        self._statuses: dict[str, ExecStatus] = {}

    def submit(self, spec: ExecSpec) -> ExecHandle:
        """Run ``spec.command=[tool_name, json_args]`` synchronously."""
        handle = ExecHandle(id=str(uuid4()), lane=LANE_DAEMON_INLINE, submitted_at=datetime.now(UTC))
        try:
            if not spec.command or len(spec.command) != 2:
                raise ValueError("inline command must be [tool_name, json_args]")
            name, raw_args = spec.command
            if name not in TOOLS:
                raise ValueError(f"unknown tool: {name}")
            args = json.loads(raw_args)
            if spec.working_dir is not None:
                args["_cwd"] = spec.working_dir
            result = TOOLS[name](args)
            self._results[handle.id] = result
            ok = not bool(result.get("is_error"))
            self._statuses[handle.id] = ExecStatus(
                status="succeeded" if ok else "failed",
                exit_code=0 if ok else 1,
                error_message=str(result.get("output")) if not ok else None,
                finished_at=datetime.now(UTC),
            )
        except Exception as exc:
            result = {"output": str(exc), "is_error": True}
            self._results[handle.id] = result
            self._statuses[handle.id] = ExecStatus(
                status="failed",
                exit_code=1,
                error_message=str(exc),
                finished_at=datetime.now(UTC),
            )
        return handle

    def status(self, handle: ExecHandle) -> ExecStatus:
        """Return the stored completion status."""
        return self._statuses[handle.id]

    def logs(self, handle: ExecHandle) -> Iterator[str]:
        """Yield the JSON tool result."""
        yield json.dumps(self._results[handle.id])

    def cancel(self, handle: ExecHandle) -> None:
        """Mark a handle cancelled if it has not completed."""
        if handle.id not in self._statuses:
            self._statuses[handle.id] = ExecStatus(status="cancelled", finished_at=datetime.now(UTC))

    def capabilities(self) -> dict[str, Any]:
        """Return lane capabilities."""
        return {"host_bound": True, "synchronous": True, "tools": sorted(TOOLS)}


register_lane(LANE_DAEMON_INLINE, DaemonInlineLane())
