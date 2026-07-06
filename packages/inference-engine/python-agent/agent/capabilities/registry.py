"""In-memory capability registry with async Postgres sync."""

from __future__ import annotations

import logging
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Iterable, Literal

from agent.capabilities.discovery import CapabilityKind, CapabilityRecord, discover_all
from agent.capabilities.manifest import CapabilityManifest, ValidationError, parse_manifest

try:
    import asyncpg
except ImportError:  # pragma: no cover - exercised by no-asyncpg fallback
    asyncpg = None  # type: ignore[assignment]

logger = logging.getLogger(__name__)

CapabilityKey = tuple[str, CapabilityKind]


@dataclass
class CapabilityRegistry:
    """Validated, in-memory capability registry."""

    _by_key: dict[CapabilityKey, CapabilityManifest] = field(default_factory=dict)
    _errors: list[ValidationError] = field(default_factory=list)

    def add(self, manifest: CapabilityManifest) -> None:
        self._by_key[(manifest.name, manifest.kind)] = manifest

    def get(self, name: str, kind: CapabilityKind) -> CapabilityManifest | None:
        return self._by_key.get((name, kind))

    def list(
        self,
        kind: CapabilityKind | None = None,
        origin: Literal["template", "user"] | None = None,
    ) -> list[CapabilityManifest]:
        manifests = list(self._by_key.values())
        if kind is not None:
            manifests = [m for m in manifests if m.kind == kind]
        if origin is not None:
            manifests = [m for m in manifests if m.origin == origin]
        return manifests

    @property
    def errors(self) -> list[ValidationError]:
        return list(self._errors)

    async def sync_to_pg(self, dsn: str | None = None) -> dict[str, Any]:
        """Upsert all loaded capabilities into the Postgres ``capabilities`` table.

        Idempotent on ``(name, kind, version)``.  Falls back to in-memory-only if
        ``dsn`` is missing or ``asyncpg`` is unavailable.
        """
        dsn = dsn or ""
        if not dsn or asyncpg is None:
            if not dsn:
                logger.warning("sync_to_pg skipped: no ROMMIE_PG_DSN configured")
            else:
                logger.warning("sync_to_pg skipped: asyncpg not installed")
            return {"skipped": True, "upserted": 0, "dsn_present": bool(dsn), "asyncpg_present": asyncpg is not None}

        conn = await asyncpg.connect(dsn)
        try:
            upserted = 0
            for manifest in self._by_key.values():
                await self._upsert(conn, manifest)
                upserted += 1
            return {"skipped": False, "upserted": upserted}
        finally:
            await conn.close()

    async def _upsert(self, conn: Any, manifest: CapabilityManifest) -> None:
        # Normalize fields per registry v2 schema
        io = manifest.io if manifest.io else {"description": manifest.description}
        permissions = manifest.permissions
        host_reqs = manifest.host_reqs if manifest.host_reqs else {}
        metadata = {
            **manifest.metadata,
            "exec_lane": manifest.exec_lane,
            "path": str(manifest.path),
        }
        lineage = {"origin": manifest.origin, **manifest.lineage}
        await conn.execute(
            """
            INSERT INTO capabilities (
                id, kind, name, version, io, permissions, host_reqs,
                scorecard, lineage, latency, cost, safety,
                promotion_state, metadata
            ) VALUES (
                $1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb,
                $8::jsonb, $9::jsonb, $10::jsonb, $11::jsonb, $12::jsonb,
                $13, $14::jsonb
            )
            ON CONFLICT (name, kind, version) DO UPDATE SET
                io = EXCLUDED.io,
                permissions = EXCLUDED.permissions,
                host_reqs = EXCLUDED.host_reqs,
                scorecard = EXCLUDED.scorecard,
                lineage = EXCLUDED.lineage,
                latency = EXCLUDED.latency,
                cost = EXCLUDED.cost,
                safety = EXCLUDED.safety,
                promotion_state = EXCLUDED.promotion_state,
                metadata = EXCLUDED.metadata,
                updated_at = now()
            """,
            uuid.uuid4().hex,
            manifest.kind,
            manifest.name,
            manifest.version,
            _jsonb(io),
            _jsonb(permissions),
            _jsonb(host_reqs),
            _jsonb(manifest.scorecard),
            _jsonb(lineage),
            _jsonb({}),
            _jsonb({}),
            _jsonb({}),
            "candidate",
            _jsonb(metadata),
        )


def _jsonb(value: Any) -> str:
    import json

    return json.dumps(value)


def load_all(roots: Iterable[Path] | None = None) -> CapabilityRegistry:
    """Convenience: discover, validate, and register all capabilities."""
    registry = CapabilityRegistry()
    for record in discover_all(roots):
        try:
            manifest = parse_manifest(record)
        except ValidationError as exc:
            registry._errors.append(exc)
            logger.warning("Rejected invalid capability %s: %s", record.path, exc)
            continue
        registry.add(manifest)
    return registry
