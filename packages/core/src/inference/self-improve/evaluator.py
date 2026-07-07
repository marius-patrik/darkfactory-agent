"""Deterministic held-out evaluator for skill/plugin self-improvement.

This is intentionally not a unit-test proxy. It scores candidate extension
artifacts against a frozen, versioned bench contract so the future auto-merge
gate has a stable signal to ratchet on.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any


PLACEHOLDERS = ("todo", "tbd", "placeholder", "fixme", "lorem ipsum")


@dataclass(frozen=True)
class EvalCase:
    id: str
    kind: str
    weight: float
    description: str
    trials: tuple[str, ...]
    budget_steps: int


def load_bench(path: Path) -> dict[str, Any]:
    if not path.exists() and not path.is_absolute():
        repo_candidate = Path(__file__).resolve().parents[2] / path
        if repo_candidate.exists():
            path = repo_candidate
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data.get("cases"), list):
        raise ValueError("bench must contain cases")
    return data


def evaluate_target(target: Path, bench_path: Path) -> dict[str, Any]:
    if not target.exists() and not target.is_absolute():
        repo_target = Path(__file__).resolve().parents[2] / target
        if repo_target.exists():
            target = repo_target
    target = target.resolve()
    bench = load_bench(bench_path)
    cases = [
        EvalCase(
            id=str(raw["id"]),
            kind=str(raw["kind"]),
            weight=float(raw.get("weight", 1.0)),
            description=str(raw.get("description", "")),
            trials=tuple(str(item) for item in raw.get("trials", [raw["id"]])),
            budget_steps=int(raw.get("budget_steps", 1)),
        )
        for raw in bench["cases"]
    ]

    kinds = _target_kinds(target)
    checks = [_evaluate_case(target, case) for case in cases if case.kind in kinds]
    total_weight = sum(float(c["weight"]) for c in checks)
    earned = sum(float(c["weight"]) * float(c["score"]) for c in checks)
    score = earned / total_weight if total_weight else 0.0
    trial_count = sum(len(c.get("trials", [])) for c in checks)
    return {
        "bench": bench.get("name", "unknown"),
        "bench_version": bench.get("version", "unknown"),
        "target": str(target),
        "score": round(score, 6),
        "trial_count": trial_count,
        "fixed_budget": True,
        "checks": checks,
    }


def evaluate_targets(targets: list[Path], bench_path: Path) -> dict[str, Any]:
    return aggregate_reports([evaluate_target(target, bench_path) for target in targets], bench_path)


def aggregate_reports(reports: list[dict[str, Any]], bench_path: Path) -> dict[str, Any]:
    score = sum(float(report["score"]) for report in reports) / len(reports) if reports else 0.0
    bench = load_bench(bench_path)
    return {
        "bench": bench.get("name", "unknown"),
        "bench_version": bench.get("version", "unknown"),
        "target": "aggregate",
        "targets": reports,
        "score": round(score, 6),
        "trial_count": sum(int(report.get("trial_count", 0)) for report in reports),
        "fixed_budget": all(bool(report.get("fixed_budget", False)) for report in reports) if reports else True,
        "checks": [check for report in reports for check in report.get("checks", [])],
    }


def missing_target_report(target: Path, bench_path: Path) -> dict[str, Any]:
    bench = load_bench(bench_path)
    return {
        "bench": bench.get("name", "unknown"),
        "bench_version": bench.get("version", "unknown"),
        "target": str(target),
        "score": 0.0,
        "trial_count": 0,
        "fixed_budget": True,
        "checks": [],
        "reason": "target missing on base",
    }


def _evaluate_case(target: Path, case: EvalCase) -> dict[str, Any]:
    trials = []
    for trial_id in case.trials:
        if case.kind == "skill":
            score, reason = _evaluate_skill_case(target, case.id)
        elif case.kind == "plugin":
            score, reason = _evaluate_plugin_case(target, case.id)
        elif case.kind == "command":
            score, reason = _evaluate_command_case(target, case.id)
        else:
            score, reason = 0.0, f"unknown case kind: {case.kind}"
        trials.append({
            "id": trial_id,
            "ok": score >= 1.0,
            "score": score,
            "reason": reason,
            "budget_steps": case.budget_steps,
            "trace": [
                {"step": "load_target", "ok": target.exists()},
                {"step": "evaluate_assertion", "ok": score > 0.0},
                {"step": "score_trial", "ok": True},
            ][:case.budget_steps],
        })
    score = sum(float(trial["score"]) for trial in trials) / len(trials) if trials else 0.0
    reason = "; ".join(sorted({str(trial["reason"]) for trial in trials})) if trials else "no trials configured"
    return {
        "name": case.id,
        "kind": case.kind,
        "ok": score >= 1.0,
        "score": round(score, 6),
        "weight": case.weight,
        "reason": reason,
        "budget_steps": case.budget_steps,
        "trials": trials,
    }


def _target_kinds(target: Path) -> set[str]:
    normalized = str(target).replace("\\", "/")
    kinds: set[str] = set()
    if _skill_text(target) is not None:
        kinds.add("skill")
    plugin_markers = [
        target / ".codex-plugin" / "plugin.json",
        target / "plugin.json",
        target / "plugin.yaml",
        target / "plugin.yml",
    ]
    if target.is_dir() and any(p.exists() for p in plugin_markers):
        kinds.add("plugin")
    if "/.user/commands" in f"/{normalized}":
        if target.is_file() and target.suffix == ".md" or target.is_dir():
            kinds.add("command")
    return kinds or {"skill", "plugin", "command"}


def _skill_text(target: Path) -> str | None:
    skill = target / "SKILL.md" if target.is_dir() else target
    if skill.name != "SKILL.md" or not skill.exists():
        return None
    return skill.read_text(encoding="utf-8")


def _frontmatter(text: str) -> dict[str, str]:
    if not text.startswith("---\n"):
        return {}
    end = text.find("\n---", 4)
    if end < 0:
        return {}
    meta: dict[str, str] = {}
    for line in text[4:end].splitlines():
        if ":" not in line:
            continue
        key, value = line.split(":", 1)
        meta[key.strip()] = value.strip().strip("\"'")
    return meta


def _evaluate_skill_case(target: Path, case_id: str) -> tuple[float, str]:
    text = _skill_text(target)
    if text is None:
        return 0.0, "target is not a SKILL.md file or skill directory"

    if case_id == "skill_metadata":
        meta = _frontmatter(text)
        if meta.get("name") and meta.get("description"):
            return 1.0, "frontmatter has name and description"
        if re.search(r"\*Description:\*", text, re.IGNORECASE):
            return 0.7, "legacy description present but frontmatter missing"
        return 0.0, "missing skill name/description metadata"

    if case_id == "skill_actionability":
        lower = text.lower()
        has_trigger = any(phrase in lower for phrase in ("use when", "when to use", "trigger", "workflow", "steps"))
        has_body = len(text.split()) >= 80
        if has_trigger and has_body:
            return 1.0, "actionable trigger/workflow guidance present"
        if has_trigger or has_body:
            return 0.5, "partially actionable"
        return 0.0, "not enough actionable guidance"

    if case_id == "skill_no_placeholders":
        lower = text.lower()
        hits = [word for word in PLACEHOLDERS if word in lower]
        if hits:
            return 0.0, f"placeholder markers found: {', '.join(hits)}"
        return 1.0, "no placeholder markers"

    if case_id == "skill_behavior_contract":
        lower = text.lower()
        checks = {
            "trigger": any(phrase in lower for phrase in ("use when", "when to use", "trigger")),
            "action": any(phrase in lower for phrase in ("steps", "workflow", "procedure", "runbook")),
            "verify": any(phrase in lower for phrase in ("verify", "verification", "test command", "expected output")),
            "failure": any(phrase in lower for phrase in ("fail", "failure", "blocked", "fallback", "reject")),
        }
        score = sum(1 for ok in checks.values() if ok) / len(checks)
        if score == 1.0:
            return 1.0, "skill behavior contract covers trigger/action/verification/failure"
        missing = ", ".join(name for name, ok in checks.items() if not ok)
        return score, f"skill behavior contract missing: {missing}"

    return 0.0, "case does not apply to skills"


def _evaluate_plugin_case(target: Path, case_id: str) -> tuple[float, str]:
    candidates = [
        target / ".codex-plugin" / "plugin.json",
        target / "plugin.json",
        target / "plugin.yaml",
        target / "plugin.yml",
    ]
    manifest = next((p for p in candidates if p.exists()), None)
    if manifest is None:
        return 0.0, "no plugin manifest found"
    text = manifest.read_text(encoding="utf-8")
    if case_id == "plugin_behavior_contract":
        lower = text.lower()
        checks = {
            "entrypoint": any(phrase in lower for phrase in ("command", "entrypoint", "handler", "main")),
            "permissions": any(phrase in lower for phrase in ("permission", "sandbox", "scope", "allowlist")),
            "verification": any(phrase in lower for phrase in ("verify", "test", "health", "validation")),
        }
        score = sum(1 for ok in checks.values() if ok) / len(checks)
        if score == 1.0:
            return 1.0, "plugin behavior contract covers entrypoint/permissions/verification"
        missing = ", ".join(name for name, ok in checks.items() if not ok)
        return score, f"plugin behavior contract missing: {missing}"
    if case_id != "plugin_manifest":
        return 0.0, "case does not apply to plugins"
    has_name = re.search(r'"name"\s*:', text) or re.search(r"^name\s*:", text, re.MULTILINE)
    has_version = re.search(r'"version"\s*:', text) or re.search(r"^version\s*:", text, re.MULTILINE)
    if has_name and has_version:
        return 1.0, "manifest has name and version"
    return 0.5, "manifest present but incomplete"


def _evaluate_command_case(target: Path, case_id: str) -> tuple[float, str]:
    if case_id not in {"command_metadata", "command_behavior_contract"}:
        return 0.0, "case does not apply to commands"
    files = [target] if target.is_file() and target.suffix == ".md" else sorted(target.glob("*.md")) if target.is_dir() else []
    if not files:
        return 0.0, "no markdown command files found"
    if case_id == "command_behavior_contract":
        scored = 0.0
        for path in files:
            text = path.read_text(encoding="utf-8")
            lower = text.lower()
            checks = [
                any(phrase in lower for phrase in ("usage", "arguments", "$arguments", "input")),
                any(phrase in lower for phrase in ("run", "execute", "create", "update", "analyze", "verify")),
                any(phrase in lower for phrase in ("output", "result", "expected", "validate", "report")),
            ]
            scored += sum(1 for ok in checks if ok) / len(checks)
        score = scored / len(files)
        if score == 1.0:
            return 1.0, "command behavior contract covers usage/action/output"
        return score, "command behavior contract incomplete"
    scored = 0
    for path in files:
        text = path.read_text(encoding="utf-8")
        meta = _frontmatter(text)
        if meta.get("description"):
            scored += 1
    return scored / len(files), f"{scored}/{len(files)} commands have description frontmatter"
