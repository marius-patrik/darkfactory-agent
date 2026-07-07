"""One-way ratchet decisions for generic skills/plugins self-improvement."""

from __future__ import annotations

from dataclasses import dataclass, asdict
from pathlib import Path
from typing import Any, Iterable


ALLOWED_AUTO_MERGE_PREFIXES = (
    ".user/skills/",
    ".user/plugins/",
    ".user/commands/",
)

# Surfaces that *look* like the editable extension surface but are protected
# infra or vendored third-party content. They must NEVER auto-merge through the
# ratchet — a change here is treated like core/infra and requires a human ADR.
# This is the single source of truth shared with the proposer.
BLOCKED_AUTO_MERGE_PREFIXES = (
    ".user/skills/.system/",
    ".user/plugins/cache/",
    ".user/plugins/marketplaces/",
)


@dataclass(frozen=True)
class SelfImproveDecision:
    accepted: bool
    baseline_score: float
    candidate_score: float
    requires_human_adr: bool
    reason: str

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


def decide(
    baseline_report: dict[str, Any] | None,
    candidate_report: dict[str, Any],
    *,
    changed_paths: Iterable[str] = (),
) -> SelfImproveDecision:
    paths = [p for p in changed_paths if p]
    requires_human_adr = any(not is_auto_merge_path(p) for p in paths)
    baseline_score = float(baseline_report.get("score", 0.0)) if baseline_report else 0.0
    candidate_score = float(candidate_report.get("score", 0.0))

    if requires_human_adr:
        return SelfImproveDecision(
            accepted=False,
            baseline_score=baseline_score,
            candidate_score=candidate_score,
            requires_human_adr=True,
            reason="changed paths include core/infra; human ADR required",
        )

    if candidate_score > baseline_score + 1e-12:
        return SelfImproveDecision(
            accepted=True,
            baseline_score=baseline_score,
            candidate_score=candidate_score,
            requires_human_adr=False,
            reason="held-out score improved",
        )

    return SelfImproveDecision(
        accepted=False,
        baseline_score=baseline_score,
        candidate_score=candidate_score,
        requires_human_adr=False,
        reason="no held-out improvement",
    )


def is_auto_merge_path(path: str) -> bool:
    normalized = path.replace("\\", "/")
    while normalized.startswith("./"):
        normalized = normalized[2:]
    if normalized.startswith("/") or "/../" in f"/{normalized}/" or normalized.startswith("../"):
        return False
    if normalized.startswith(BLOCKED_AUTO_MERGE_PREFIXES):
        return False
    return normalized.startswith(ALLOWED_AUTO_MERGE_PREFIXES)


def changed_extension_targets(changed_paths: Iterable[str]) -> tuple[list[str], list[str]]:
    """Return aggregate extension targets and ineligible changed paths.

    A self-improvement PR may touch several files inside one extension target,
    but every changed path must stay inside the allowed extension surfaces. If a
    PR touches multiple extension targets, all of them must be evaluated; using
    only the first target would let regressions hide behind a single improvement.
    """
    targets: set[str] = set()
    ineligible: list[str] = []

    for raw in changed_paths:
        normalized = raw.replace("\\", "/")
        while normalized.startswith("./"):
            normalized = normalized[2:]
        if not normalized:
            continue
        if normalized.startswith("/") or "/../" in f"/{normalized}/" or normalized.startswith("../"):
            ineligible.append(normalized)
            continue
        if normalized.startswith(BLOCKED_AUTO_MERGE_PREFIXES):
            ineligible.append(normalized)
            continue
        parts = normalized.split("/")
        if normalized.startswith(".user/skills/") and len(parts) >= 3:
            targets.add("/".join(parts[:3]))
        elif normalized.startswith(".user/plugins/") and len(parts) >= 3:
            targets.add("/".join(parts[:3]))
        elif normalized.startswith(".user/commands/") and len(parts) == 3 and normalized.endswith(".md"):
            targets.add(normalized)
        elif normalized.startswith(".user/commands/"):
            ineligible.append(normalized)
        else:
            ineligible.append(normalized)

    return sorted(targets), ineligible


def load_report(path: Path) -> dict[str, Any] | None:
    if not path.exists():
        return None
    import json

    data = json.loads(path.read_text(encoding="utf-8"))
    return data if isinstance(data, dict) else None
