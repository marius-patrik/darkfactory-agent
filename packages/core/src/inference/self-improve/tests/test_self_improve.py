from __future__ import annotations

import json
from pathlib import Path

from agents.packages.self_improve.cli import main
from agents.packages.self_improve.evaluator import evaluate_target, evaluate_targets
from agents.packages.self_improve.proposer import materialize_pr_plan, propose_improvements, validate_proposal_target
from agents.packages.self_improve.ratchet import changed_extension_targets, decide, is_auto_merge_path


REPO_ROOT = Path(__file__).resolve().parents[3]
BENCH = REPO_ROOT / ".data/self_improve/heldout_bench.json"


def test_evaluates_existing_skill() -> None:
    report = evaluate_target(Path(".user/skills/pass"), BENCH)
    assert report["score"] > 0.0
    assert report["fixed_budget"] is True
    assert report["trial_count"] >= len(report["checks"])
    assert any(c["name"] == "skill_metadata" for c in report["checks"])
    assert {c["kind"] for c in report["checks"]} == {"skill"}
    assert all(c["trials"] and c["budget_steps"] > 0 for c in report["checks"])
    assert any(c["name"] == "skill_behavior_contract" for c in report["checks"])


def test_evaluates_markdown_commands() -> None:
    report = evaluate_target(Path(".user/commands"), BENCH)
    assert {c["kind"] for c in report["checks"]} == {"command"}
    command_check = next(c for c in report["checks"] if c["name"] == "command_metadata")
    assert command_check["score"] == 1.0


def test_skill_behavior_case_not_satisfied_by_metadata_only(tmp_path) -> None:
    target = tmp_path / ".user" / "skills" / "meta-only"
    target.mkdir(parents=True)
    (target / "SKILL.md").write_text(
        "---\nname: meta-only\ndescription: Metadata only skill.\n---\n\nThis has metadata and enough words. "
        + " ".join(["content"] * 100),
        encoding="utf-8",
    )

    report = evaluate_target(target, BENCH)
    behavior = next(check for check in report["checks"] if check["name"] == "skill_behavior_contract")

    assert behavior["score"] < 1.0
    assert "missing" in behavior["reason"]


def test_plugin_behavior_case_requires_executable_contract_not_manifest_only(tmp_path) -> None:
    target = tmp_path / ".user" / "plugins" / "manifest-only"
    (target / ".codex-plugin").mkdir(parents=True)
    (target / ".codex-plugin" / "plugin.json").write_text(
        json.dumps({"name": "Manifest Only", "version": "0.1.0"}),
        encoding="utf-8",
    )

    report = evaluate_target(target, BENCH)
    manifest = next(check for check in report["checks"] if check["name"] == "plugin_manifest")
    behavior = next(check for check in report["checks"] if check["name"] == "plugin_behavior_contract")

    assert manifest["score"] == 1.0
    assert behavior["score"] < 1.0


def test_command_behavior_case_requires_command_body_contract_not_description_only(tmp_path) -> None:
    target = tmp_path / ".user" / "commands" / "meta-only.md"
    target.parent.mkdir(parents=True)
    target.write_text("---\ndescription: Metadata only command.\n---\n\nNo operational contract.\n", encoding="utf-8")

    report = evaluate_target(target, BENCH)
    metadata = next(check for check in report["checks"] if check["name"] == "command_metadata")
    behavior = next(check for check in report["checks"] if check["name"] == "command_behavior_contract")

    assert metadata["score"] == 1.0
    assert behavior["score"] < 1.0


def test_ratchet_accepts_only_heldout_improvement_inside_extension_paths() -> None:
    baseline = {"score": 0.5}
    candidate = {"score": 0.75}
    decision = decide(baseline, candidate, changed_paths=[".user/skills/pass/SKILL.md"])
    assert decision.accepted
    assert decision.reason == "held-out score improved"


def test_ratchet_rejects_core_changes_even_when_score_improves() -> None:
    decision = decide({"score": 0.5}, {"score": 1.0}, changed_paths=["agent/src/tools/registry.ts"])
    assert not decision.accepted
    assert decision.requires_human_adr


def test_editable_surface_auto_merges_on_heldout_improvement() -> None:
    """I17: editable surface (skills/plugins/commands) auto-merges iff held-out improved."""
    decision = decide(
        {"score": 0.5},
        {"score": 0.8},
        changed_paths=[".user/skills/pass/SKILL.md"],
    )
    assert decision.accepted
    assert not decision.requires_human_adr
    assert decision.reason == "held-out score improved"


def test_editable_surface_blocked_on_regression() -> None:
    """I17: editable surface is blocked (not auto-merged) when held-out regresses or is flat."""
    regressed = decide(
        {"score": 0.8},
        {"score": 0.5},
        changed_paths=[".user/skills/pass/SKILL.md"],
    )
    assert not regressed.accepted
    assert not regressed.requires_human_adr
    assert regressed.reason == "no held-out improvement"

    flat = decide(
        {"score": 0.8},
        {"score": 0.8},
        changed_paths=[".user/commands/example.md"],
    )
    assert not flat.accepted
    assert not flat.requires_human_adr
    assert flat.reason == "no held-out improvement"


def test_core_infra_always_blocked_pending_adr_even_when_heldout_improves() -> None:
    """I17: core/infra changes are NEVER auto-merged; they require a human-approved ADR."""
    for core_path in (
        "agent/src/tools/registry.ts",
        ".github/workflows/ci.yml",
        "self_improve/ratchet.py",
    ):
        decision = decide({"score": 0.0}, {"score": 1.0}, changed_paths=[core_path])
        assert not decision.accepted, core_path
        assert decision.requires_human_adr, core_path
        assert decision.reason == "changed paths include core/infra; human ADR required"


def test_protected_extension_surfaces_are_blocked_pending_adr(tmp_path, capsys) -> None:
    """Protected infra / vendored surfaces look editable but must require a human ADR.

    `.user/skills/.system/**`, `.user/plugins/cache/**`, and
    `.user/plugins/marketplaces/**` must never auto-merge through the ratchet even
    when the candidate held-out score improves.
    """
    for protected in (
        ".user/skills/.system/secret/SKILL.md",
        ".user/plugins/cache/vendored/plugin.json",
        ".user/plugins/marketplaces/store/plugin.json",
    ):
        assert not is_auto_merge_path(protected), protected
        decision = decide({"score": 0.0}, {"score": 1.0}, changed_paths=[protected])
        assert not decision.accepted, protected
        assert decision.requires_human_adr, protected

    targets, ineligible = changed_extension_targets([".user/skills/.system/secret/SKILL.md"])
    assert targets == []
    assert ineligible == [".user/skills/.system/secret/SKILL.md"]

    # The gate command must refuse to auto-merge a protected-surface change.
    code = main(["gate", "--changed-path", ".user/plugins/cache/vendored/plugin.json"])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert payload["decision"]["requires_human_adr"]
    assert payload["ineligible_paths"] == [".user/plugins/cache/vendored/plugin.json"]


def test_auto_merge_path_classification() -> None:
    assert is_auto_merge_path(".user/skills/pass/SKILL.md")
    assert is_auto_merge_path("./.user/plugins/gateway-pack/plugin.yaml")
    assert not is_auto_merge_path("gateway/gateway/main.py")
    assert not is_auto_merge_path(".user/skills/../src/core.py")
    assert not is_auto_merge_path(".user/plugins/../src/x")


def test_changed_extension_targets_collects_all_targets_and_core_paths() -> None:
    targets, ineligible = changed_extension_targets([
        ".user/skills/pass/SKILL.md",
        ".user/skills/test/SKILL.md",
        ".user/commands/example.md",
        "gateway/gateway/main.py",
    ])
    assert targets == [".user/commands/example.md", ".user/skills/pass", ".user/skills/test"]
    assert ineligible == ["gateway/gateway/main.py"]


def test_changed_extension_targets_rejects_invalid_command_and_traversal_paths() -> None:
    targets, ineligible = changed_extension_targets([
        ".user/commands/nested/example.md",
        ".user/commands/example.txt",
        ".user/skills/../commands/example.md",
    ])
    assert targets == []
    assert ineligible == [
        ".user/commands/nested/example.md",
        ".user/commands/example.txt",
        ".user/skills/../commands/example.md",
    ]


def test_evaluate_targets_aggregates_multiple_extension_scores() -> None:
    report = evaluate_targets([Path(".user/skills/pass"), Path(".user/commands")], BENCH)
    assert report["target"] == "aggregate"
    assert len(report["targets"]) == 2
    assert report["score"] > 0.0
    assert report["fixed_budget"] is True
    assert report["trial_count"] == sum(target["trial_count"] for target in report["targets"])


def test_gate_rejects_mixed_core_and_extension_change(capsys) -> None:
    code = main([
        "gate",
        "--changed-path", ".user/skills/pass/SKILL.md",
        "--changed-path", "gateway/gateway/main.py",
    ])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert payload["decision"]["requires_human_adr"]
    assert payload["ineligible_paths"] == ["gateway/gateway/main.py"]


def test_gate_rejects_per_target_regression_even_when_aggregate_improves(tmp_path, capsys) -> None:
    base = tmp_path / "base"
    candidate = tmp_path / "candidate"
    _write_skill(base / ".user" / "skills" / "steady", "steady", placeholder=False)
    _write_skill(candidate / ".user" / "skills" / "steady", "steady", placeholder=True)
    _write_skill(candidate / ".user" / "skills" / "new", "new", placeholder=False)

    code = main([
        "gate",
        "--base-root", str(base),
        "--candidate-root", str(candidate),
        "--changed-path", ".user/skills/steady/SKILL.md",
        "--changed-path", ".user/skills/new/SKILL.md",
    ])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert payload["candidate"]["score"] > payload["baseline"]["score"]
    assert not payload["decision"]["accepted"]
    assert payload["decision"]["reason"] == "target(s) did not improve: .user/skills/steady"
    steady_decision = next(item for item in payload["target_decisions"] if item["target"] == ".user/skills/steady")
    new_decision = next(item for item in payload["target_decisions"] if item["target"] == ".user/skills/new")
    assert not steady_decision["accepted"]
    assert new_decision["baseline_score"] == 0.0
    assert new_decision["accepted"]


def test_gate_accepts_existing_extension_target_only_after_heldout_improvement(tmp_path, capsys) -> None:
    base = tmp_path / "base"
    candidate = tmp_path / "candidate"
    _write_skill(base / ".user" / "skills" / "steady", "steady", placeholder=True)
    _write_skill(candidate / ".user" / "skills" / "steady", "steady", placeholder=False)

    code = main([
        "gate",
        "--base-root", str(base),
        "--candidate-root", str(candidate),
        "--changed-path", ".user/skills/steady/SKILL.md",
    ])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["decision"]["accepted"] is True
    assert payload["decision"]["reason"] == "all changed targets improved"
    assert payload["candidate"]["score"] > payload["baseline"]["score"]
    assert payload["target_decisions"][0]["accepted"] is True
    assert payload["target_decisions"][0]["candidate_score"] > payload["target_decisions"][0]["baseline_score"]


def test_gate_rejects_per_command_file_regression_even_when_commands_aggregate_improves(tmp_path, capsys) -> None:
    base = tmp_path / "base"
    candidate = tmp_path / "candidate"
    _write_command(base / ".user" / "commands" / "a.md", "A", description=True)
    _write_command(base / ".user" / "commands" / "b.md", "B", description=False)
    _write_command(candidate / ".user" / "commands" / "a.md", "A", description=False)
    _write_command(candidate / ".user" / "commands" / "b.md", "B", description=True)
    _write_command(candidate / ".user" / "commands" / "c.md", "C", description=True)

    code = main([
        "gate",
        "--base-root", str(base),
        "--candidate-root", str(candidate),
        "--changed-path", ".user/commands/a.md",
        "--changed-path", ".user/commands/b.md",
        "--changed-path", ".user/commands/c.md",
    ])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert payload["candidate"]["score"] > payload["baseline"]["score"]
    assert not payload["decision"]["accepted"]
    assert payload["decision"]["reason"] == "target(s) did not improve: .user/commands/a.md"
    a_decision = next(item for item in payload["target_decisions"] if item["target"] == ".user/commands/a.md")
    b_decision = next(item for item in payload["target_decisions"] if item["target"] == ".user/commands/b.md")
    c_decision = next(item for item in payload["target_decisions"] if item["target"] == ".user/commands/c.md")
    assert not a_decision["accepted"]
    assert b_decision["accepted"]
    assert c_decision["baseline_score"] == 0.0
    assert c_decision["accepted"]


def test_gate_rejects_deleted_command_file(tmp_path, capsys) -> None:
    base = tmp_path / "base"
    candidate = tmp_path / "candidate"
    _write_command(base / ".user" / "commands" / "a.md", "A", description=True)
    (candidate / ".user" / "commands").mkdir(parents=True)

    code = main([
        "gate",
        "--base-root", str(base),
        "--candidate-root", str(candidate),
        "--changed-path", ".user/commands/a.md",
    ])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert not payload["decision"]["accepted"]
    assert payload["decision"]["reason"] == "target(s) did not improve: .user/commands/a.md"
    assert payload["target_decisions"][0]["candidate_score"] == 0.0


def test_gate_rejected_candidate_terminates_without_successor_plan(tmp_path, capsys) -> None:
    base = tmp_path / "base"
    candidate = tmp_path / "candidate"
    _write_skill(base / ".user" / "skills" / "steady", "steady", placeholder=False)
    _write_skill(candidate / ".user" / "skills" / "steady", "steady", placeholder=False)

    code = main([
        "gate",
        "--base-root", str(base),
        "--candidate-root", str(candidate),
        "--changed-path", ".user/skills/steady/SKILL.md",
    ])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert payload["decision"]["accepted"] is False
    assert payload["decision"]["reason"] == "target(s) did not improve: .user/skills/steady"
    assert payload["target_decisions"][0]["accepted"] is False
    assert "plans" not in payload
    assert "proposals" not in payload


def test_gate_uses_zero_baseline_for_new_targets(tmp_path, capsys) -> None:
    base = tmp_path / "base"
    candidate = tmp_path / "candidate"
    base.mkdir()
    _write_skill(candidate / ".user" / "skills" / "new", "new", placeholder=False)

    code = main([
        "gate",
        "--base-root", str(base),
        "--candidate-root", str(candidate),
        "--changed-path", ".user/skills/new/SKILL.md",
    ])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["baseline"]["targets"][0]["score"] == 0.0
    assert payload["baseline"]["targets"][0]["reason"] == "target missing on base"
    assert payload["target_decisions"][0]["accepted"]


def test_gate_rejects_protected_north_star_regression(tmp_path, capsys) -> None:
    base = tmp_path / "base"
    candidate = tmp_path / "candidate"
    _write_skill(base / ".user" / "skills" / "steady", "steady", placeholder=True)
    _write_skill(candidate / ".user" / "skills" / "steady", "steady", placeholder=False)
    _write_json(base / ".user" / "projects" / "qft" / "bench" / "baseline.json", {"truth_score": 0.8})
    _write_json(candidate / ".user" / "projects" / "qft" / "bench" / "baseline.json", {"truth_score": 0.7})

    code = main([
        "gate",
        "--base-root", str(base),
        "--candidate-root", str(candidate),
        "--changed-path", ".user/skills/steady/SKILL.md",
    ])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert not payload["decision"]["accepted"]
    assert payload["decision"]["reason"] == "protected north-star regressed"
    assert payload["protected_north_star"]["baseline_score"] == 0.8
    assert payload["protected_north_star"]["candidate_score"] == 0.7


def test_proposer_bounds_extension_only_targets(tmp_path) -> None:
    root = tmp_path / "repo"
    _write_skill(root / ".user" / "skills" / "weak", "weak", placeholder=True)
    (root / "src").mkdir(parents=True)
    payload = propose_improvements(
        root,
        BENCH,
        [".user/skills/weak", "src/core"],
        max_proposals=1,
        min_score=1.0,
    )
    assert len(payload["proposals"]) == 1
    proposal = payload["proposals"][0]
    assert proposal["target"] == ".user/skills/weak"
    assert proposal["changed_paths"] == [".user/skills/weak/SKILL.md"]
    assert "Do not touch core" in proposal["body"]
    assert payload["rejected"] == [{"target": "src/core", "reason": "outside self-improve extension surface"}]


def test_proposer_rejects_registry_and_traversal_targets(tmp_path) -> None:
    root = tmp_path / "repo"
    (root / ".user" / "plugins").mkdir(parents=True)
    (root / ".user" / "plugins" / "blocklist.json").write_text("{}", encoding="utf-8")
    payload = propose_improvements(
        root,
        BENCH,
        [".user/plugins/blocklist.json", ".user/skills/../src/core.py"],
        max_proposals=2,
        min_score=1.0,
    )

    assert payload["proposals"] == []
    assert {"target": ".user/plugins/blocklist.json", "reason": "target must be an existing skill, plugin, or command file"} in payload["rejected"]
    assert {"target": ".user/skills/../src/core.py", "reason": "target must be a relative extension path without traversal"} in payload["rejected"]


def test_proposer_supports_specific_command_file_target(tmp_path) -> None:
    root = tmp_path / "repo"
    command = root / ".user" / "commands" / "weak.md"
    command.parent.mkdir(parents=True)
    command.write_text("# Weak\n\nRun this command.", encoding="utf-8")

    payload = propose_improvements(root, BENCH, [".user/commands/weak.md"], max_proposals=1, min_score=1.0)

    assert payload["proposals"][0]["target"] == ".user/commands/weak.md"
    assert payload["proposals"][0]["changed_paths"] == [".user/commands/weak.md"]


def test_materialize_rejects_changed_paths_that_escape_target() -> None:
    payload = {
        "proposals": [{
            "target": ".user/skills/demo",
            "score": 0.4,
            "changed_paths": [".user/skills/other/SKILL.md"],
            "title": "Improve demo",
            "body": "Bounded proposal",
        }],
    }

    plan = materialize_pr_plan(payload)

    assert plan["plans"] == []
    assert plan["reason"] == "proposal changed_paths escape target"


def test_validate_proposal_target_requires_existing_structural_target(tmp_path) -> None:
    root = tmp_path / "repo"
    _write_skill(root / ".user" / "skills" / "demo", "demo", placeholder=True)

    assert validate_proposal_target(root, ".user/skills/demo") == (".user/skills/demo", None)
    assert validate_proposal_target(root, "/.user/skills/demo")[1] == "target must be a relative extension path without traversal"
    assert validate_proposal_target(root, "skills\\.system\\x")[1] == "target must use forward slashes"
    assert validate_proposal_target(root, ".user/skills/.system/secret")[1] == "target is excluded from self-improve proposal surface"


def test_propose_cli_writes_json(tmp_path) -> None:
    root = tmp_path / "repo"
    _write_skill(root / ".user" / "skills" / "weak", "weak", placeholder=True)
    output = tmp_path / "proposal.json"

    code = main([
        "propose",
        "--root", str(root),
        "--target", ".user/skills/weak",
        "--max-proposals", "1",
        "--min-score", "1.0",
        "--output", str(output),
    ])
    assert code == 0
    payload = json.loads(output.read_text(encoding="utf-8"))
    assert payload["proposals"][0]["target"] == ".user/skills/weak"


def test_materialize_pr_plan_opens_one_draft_self_improve_plan(tmp_path) -> None:
    root = tmp_path / "repo"
    _write_skill(root / ".user" / "skills" / "weak", "weak", placeholder=True)
    proposals = propose_improvements(
        root,
        BENCH,
        [".user/skills/weak"],
        max_proposals=1,
        min_score=1.0,
    )
    plan = materialize_pr_plan(proposals)
    assert len(plan["plans"]) == 1
    first = plan["plans"][0]
    assert first["branch"] == "self-improve/user-skills-weak"
    assert first["draft"] is True
    assert first["labels"] == ["self-improve"]
    assert first["changed_paths"] == [".user/skills/weak/SKILL.md"]
    assert "self-improve-eval" in "\n".join(plan["constraints"])
    assert "gh" in first["commands"][1]


def test_materialize_cli_writes_pr_plan(tmp_path) -> None:
    proposal = {
        "proposals": [{
            "target": ".user/plugins/demo",
            "score": 0.4,
            "changed_paths": [".user/plugins/demo/plugin.json"],
            "title": "Improve demo plugin held-out score",
            "body": "Bounded proposal",
        }],
        "rejected": [],
    }
    proposal_path = tmp_path / "proposal.json"
    output = tmp_path / "plan.json"
    proposal_path.write_text(json.dumps(proposal), encoding="utf-8")

    code = main([
        "materialize",
        str(proposal_path),
        "--output", str(output),
    ])
    assert code == 0
    payload = json.loads(output.read_text(encoding="utf-8"))
    assert payload["plans"][0]["branch"] == "self-improve/user-plugins-demo"
    assert payload["plans"][0]["labels"] == ["self-improve"]


def test_self_improve_propose_workflow_is_manual_and_candidate_branch_gated() -> None:
    workflow = (REPO_ROOT / ".github/workflows/self-improve-propose.yml").read_text(encoding="utf-8")

    assert "workflow_dispatch:" in workflow
    assert "concurrency:" in workflow
    assert "schedule:" not in workflow
    assert "pull_request:" not in workflow
    assert "candidate_branch:" in workflow
    assert "--max-proposals 1" in workflow
    assert "Candidate branch must change exactly one extension target" in workflow
    assert "--draft" in workflow


def _write_skill(path: Path, name: str, *, placeholder: bool) -> None:
    skill_path = path / "SKILL.md"
    skill_path.parent.mkdir(parents=True, exist_ok=True)
    filler = " ".join(["workflow steps use when review execute verify failure fallback expected output"] * 14)
    suffix = " placeholder" if placeholder else ""
    skill_path.write_text(
        f"---\nname: {name}\ndescription: Test skill {name}\n---\n\n"
        f"Use when a deterministic self improvement gate test needs a skill. {filler}{suffix}\n",
        encoding="utf-8",
    )


def _write_command(path: Path, title: str, *, description: bool) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    frontmatter = "---\n"
    if description:
        frontmatter += f"description: Command {title}\n"
    frontmatter += "---\n\n"
    path.write_text(f"{frontmatter}# {title}\n\nUsage: /{title.lower()} $ARGUMENTS\n\nRun this command and report the expected output validation result.\n", encoding="utf-8")


def _write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload), encoding="utf-8")

