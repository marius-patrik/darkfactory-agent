"""Permission gate seam for VS2."""

from __future__ import annotations

from enum import Enum


class PermissionMode(str, Enum):
    """Loop permission modes."""

    auto = "auto"
    ask = "ask"


def approve(mode: PermissionMode, tool_name: str, args: dict[str, object]) -> bool:
    """Return True when a tool call is approved.

    VS2 locks full-auto/bypass behavior. ``ask`` is present as the VS4 seam.
    """
    if mode == PermissionMode.auto:
        return True
    return False
