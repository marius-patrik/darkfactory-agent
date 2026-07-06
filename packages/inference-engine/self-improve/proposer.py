from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from pathlib import Path
from pathlib import PurePosixPath
from typing import Any

from .evaluator import evaluate_target
from .ratchet import BLOCKED_AUTO_MERGE_PREFIXES, is_auto_merge_path


@dataclass(frozen=True)
class Proposal:
    target: str
    score: float
    changed_paths: list[str]
    title: str
    body: str

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True)
class PRPlan:
    branch: str
    title: str
    body: str
    labels: list[str]
    draft: bool
    target: str
    changed_paths: list[str]
    commands: list[list[str]]

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


# Re-export the single source of truth from the ratchet so the proposal surface
# and the auto-merge gate can never drift apart.
BLOCKED_TARGET_PREFIXES = BLOCKED_AUTO_MERGE_PREFIXES


def propose_improvements(
    root: Path,
    bench: Path,
    targets: list[str],
    *,
    max_proposals: int = 3,
    min_score: float = 0.95,
) -> dict[str, Any]:
    proposals: list[Proposal] = []
    rejected: list[dict[str, str]] = []
    for target in sorted(set(targets)):
        normalized, reason = validate_proposal_target(root, target)
        if reason:
            rejected.append({"target": target.replace("\\", "/").strip("/"), "reason": reason})
            continue
        report = evaluate_target(root / normalized, bench)
        score = float(report.get("score", 0.0))
        if score >= min_score:
            continue
        changed_paths = _proposal_paths(root / normalized, normalized)
        if not changed_paths:
            rejected.append({"target": normalized, "reason": "no editable proposal path found"})
            continue
        if len(proposals) >= max_proposals:
            continue
        proposals.append(Proposal(
            target=normalized,
            score=score,
            changed_paths=changed_paths,
            title=f"Improve {normalized} held-out score",
            body=_proposal_body(normalized, score, report),
        ))

    return {
        "max_proposals": max_proposals,
        "min_score": min_score,
        "proposals": [proposal.to_dict() for proposal in proposals],
        "rejected": rejected,
    }


def materialize_pr_plan(payload: dict[str, Any], *, branch_prefix: str = "self-improve") -> dict[str, Any]:
    proposals = payload.get("proposals", [])
    if not isinstance(proposals, list) or not proposals:
        return {"plans": [], "reason": "no proposals"}
    proposal = proposals[0]
    if not isinstance(proposal, dict):
        return {"plans": [], "reason": "invalid proposal payload"}
    target = str(proposal["target"])
    changed_paths = [str(path) for path in proposal.get("changed_paths", [])]
    if not changed_paths:
        return {"plans": [], "reason": "proposal has no changed paths"}
    escaped = [path for path in changed_paths if not _path_belongs_to_target(path, target)]
    if escaped:
        return {"plans": [], "reason": "proposal changed_paths escape target", "escaped": escaped}
    branch = f"{branch_prefix}/{_slug(target)}"
    title = str(proposal.get("title") or f"Improve {target} held-out score")
    body = _pr_body(proposal)
    plan = PRPlan(
        branch=branch,
        title=title,
        body=body,
        labels=["self-improve"],
        draft=True,
        target=target,
        changed_paths=changed_paths,
        commands=[
            ["git", "checkout", "-b", branch],
            ["gh", "pr", "create", "--draft", "--label", "self-improve", "--title", title, "--body-file", "SELF_IMPROVE_PR_BODY.md"],
        ],
    )
    return {
        "plans": [plan.to_dict()],
        "rejected": payload.get("rejected", []),
        "constraints": [
            "open at most one self-improve PR",
            "edit only changed_paths from the selected plan",
            "require self-improve-eval and human review gate before merge",
        ],
    }


def validate_proposal_target(root: Path, target: str) -> tuple[str, str | None]:
    if "\\" in target:
        return target, "target must use forward slashes"
    raw_input = target.strip()
    if raw_input.startswith("/"):
        return raw_input, "target must be a relative extension path without traversal"
    raw = raw_input.strip("/")
    if not raw:
        return raw, "empty target"
    posix = PurePosixPath(raw)
    if posix.is_absolute() or ".." in posix.parts:
        return raw, "target must be a relative extension path without traversal"
    normalized = posix.as_posix()
    if any(normalized.startswith(prefix) for prefix in BLOCKED_TARGET_PREFIXES):
        return normalized, "target is excluded from self-improve proposal surface"
    if not is_auto_merge_path(normalized + ("" if normalized.endswith("/") else "/")) and not is_auto_merge_path(normalized):
        return normalized, "outside self-improve extension surface"

    root_resolved = root.resolve()
    target_path = (root / normalized).resolve()
    try:
        target_path.relative_to(root_resolved)
    except ValueError:
        return normalized, "target escapes repository root"

    parts = normalized.split("/")
    if len(parts) == 3 and parts[:2] == [".user", "skills"] and (target_path / "SKILL.md").is_file():
        return normalized, None
    if len(parts) == 3 and parts[:2] == [".user", "plugins"] and _plugin_manifest_exists(target_path):
        return normalized, None
    if len(parts) == 3 and parts[:2] == [".user", "commands"] and target_path.is_file() and target_path.suffix == ".md":
        return normalized, None
    return normalized, "target must be an existing skill, plugin, or command file"


def _proposal_paths(target_path: Path, normalized: str) -> list[str]:
    if normalized.startswith(".user/skills/"):
        return [f"{normalized}/SKILL.md"]
    if normalized.startswith(".user/plugins/"):
        for rel in (".codex-plugin/plugin.json", "plugin.json", "plugin.yaml", "plugin.yml"):
            if (target_path / rel).exists():
                return [f"{normalized}/{rel}"]
    if _is_command_file(normalized):
        return [normalized]
    return []


def _plugin_manifest_exists(target_path: Path) -> bool:
    return any((target_path / rel).exists() for rel in (".codex-plugin/plugin.json", "plugin.json", "plugin.yaml", "plugin.yml"))


def _is_command_file(normalized: str) -> bool:
    return (
        normalized.startswith(".user/commands/")
    ) and normalized.endswith(".md")


def _path_belongs_to_target(path: str, target: str) -> bool:
    normalized_path = PurePosixPath(path.replace("\\", "/").strip("/"))
    normalized_target = PurePosixPath(target.replace("\\", "/").strip("/"))
    if ".." in normalized_path.parts or ".." in normalized_target.parts:
        return False
    if _is_command_file(normalized_target.as_posix()):
        return normalized_path == normalized_target
    return normalized_path == normalized_target or normalized_target in normalized_path.parents


def _proposal_body(target: str, score: float, report: dict[str, Any]) -> str:
    weak = [
        f"- {check['name']}: {check['reason']}"
        for check in report.get("checks", [])
        if float(check.get("score", 0.0)) < 1.0
    ]
    weak_text = "\n".join(weak) if weak else "- No failing check was reported; inspect trace variance."
    return (
        f"Bounded self-improvement proposal for `{target}`.\n\n"
        f"Current held-out score: `{score:.6f}`.\n\n"
        "Weak checks:\n"
        f"{weak_text}\n\n"
        "Constraints:\n"
        "- Edit only the listed extension target paths.\n"
        "- Do not touch core, infrastructure, workflows, evaluator code, or protected north-star files.\n"
        "- Open at most one PR for this proposal and do not spawn successor work on rejection.\n"
    )


def _pr_body(proposal: dict[str, Any]) -> str:
    changed = "\n".join(f"- `{path}`" for path in proposal.get("changed_paths", []))
    return (
        f"{proposal.get('body', '').strip()}\n\n"
        "PR opener constraints:\n"
        "- This PR must carry the `self-improve` label.\n"
        "- Keep the PR draft until the proposed edits are present.\n"
        "- Do not merge without the self-improve ratchet and review gate.\n\n"
        "Allowed changed paths:\n"
        f"{changed}\n"
    )


def _slug(value: str) -> str:
    slug = "".join(ch.lower() if ch.isalnum() else "-" for ch in value)
    while "--" in slug:
        slug = slug.replace("--", "-")
    return slug.strip("-")[:80] or "proposal"


def dump_proposals(payload: dict[str, Any], output: Path | None) -> None:
    text = json.dumps(payload, indent=2, sort_keys=True) + "\n"
    if output:
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(text, encoding="utf-8")
    else:
        print(text, end="")
