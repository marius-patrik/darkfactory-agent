"""Capability loader / registry v2 (Claude-format)."""

from __future__ import annotations

from agent.capabilities.discovery import CapabilityRecord, CapabilityKind, discover_all, discover
from agent.capabilities.manifest import CapabilityManifest, ValidationError, parse_manifest
from agent.capabilities.registry import CapabilityRegistry, load_all
from agent.capabilities.execute import ExecutionResult, execute

__all__ = [
    "CapabilityRecord",
    "CapabilityKind",
    "discover",
    "discover_all",
    "CapabilityManifest",
    "ValidationError",
    "parse_manifest",
    "CapabilityRegistry",
    "load_all",
    "ExecutionResult",
    "execute",
]
