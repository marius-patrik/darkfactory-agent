from __future__ import annotations

import argparse
import json
from pathlib import Path

from .evaluator import aggregate_reports, evaluate_target, evaluate_targets, missing_target_report
from .proposer import dump_proposals, materialize_pr_plan, propose_improvements
from .ratchet import SelfImproveDecision, changed_extension_targets, decide, load_report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="self-improve")
    sub = parser.add_subparsers(dest="command", required=True)

    eval_p = sub.add_parser("evaluate")
    eval_p.add_argument("target")
    eval_p.add_argument("--bench", default=".data/self_improve/heldout_bench.json")

    decide_p = sub.add_parser("decide")
    decide_p.add_argument("candidate_report")
    decide_p.add_argument("--baseline-report")
    decide_p.add_argument("--changed-path", action="append", default=[])

    gate_p = sub.add_parser("gate")
    gate_p.add_argument("--base-root", default=".")
    gate_p.add_argument("--candidate-root", default=".")
    gate_p.add_argument("--bench", default=".data/self_improve/heldout_bench.json")
    gate_p.add_argument("--changed-path", action="append", default=[])
    gate_p.add_argument("--protected-north-star", default=".user/projects/qft/bench/baseline.json")

    propose_p = sub.add_parser("propose")
    propose_p.add_argument("--root", default=".")
    propose_p.add_argument("--bench", default=".data/self_improve/heldout_bench.json")
    propose_p.add_argument("--target", action="append", default=[])
    propose_p.add_argument("--max-proposals", type=int, default=3)
    propose_p.add_argument("--min-score", type=float, default=0.95)
    propose_p.add_argument("--output")

    materialize_p = sub.add_parser("materialize")
    materialize_p.add_argument("proposal_json")
    materialize_p.add_argument("--branch-prefix", default="self-improve")
    materialize_p.add_argument("--output")

    args = parser.parse_args(argv)
    if args.command == "evaluate":
      report = evaluate_target(Path(args.target), Path(args.bench))
      print(json.dumps(report, indent=2, sort_keys=True))
      return 0

    if args.command == "decide":
      baseline = load_report(Path(args.baseline_report)) if args.baseline_report else None
      candidate = load_report(Path(args.candidate_report))
      if candidate is None:
          raise SystemExit("candidate report missing or invalid")
      decision = decide(baseline, candidate, changed_paths=args.changed_path)
      print(json.dumps(decision.to_dict(), indent=2, sort_keys=True))
      return 0 if decision.accepted else 1

    if args.command == "gate":
      targets, ineligible = changed_extension_targets(args.changed_path)
      if ineligible or not targets:
          decision = decide({"score": 0.0}, {"score": 0.0}, changed_paths=args.changed_path)
          payload = {
              "decision": decision.to_dict(),
              "targets": targets,
              "ineligible_paths": ineligible,
              "baseline": None,
              "candidate": None,
          }
          print(json.dumps(payload, indent=2, sort_keys=True))
          return 1

      bench_path = Path(args.bench)
      base_root = Path(args.base_root)
      candidate_root = Path(args.candidate_root)
      north_star = protected_north_star_check(base_root, candidate_root, Path(args.protected_north_star))
      baseline_reports = []
      candidate_reports = []
      target_decisions = []
      for target in targets:
          base_target = base_root / target
          candidate_target = candidate_root / target
          baseline_report = (
              evaluate_target(base_target, bench_path)
              if base_target.exists()
              else missing_target_report(base_target, bench_path)
          )
          candidate_report = evaluate_target(candidate_target, bench_path)
          target_decision = decide(baseline_report, candidate_report, changed_paths=args.changed_path)
          baseline_reports.append(baseline_report)
          candidate_reports.append(candidate_report)
          target_decisions.append({"target": target, **target_decision.to_dict()})

      baseline = aggregate_reports(baseline_reports, bench_path)
      candidate = aggregate_reports(candidate_reports, bench_path)
      failed_targets = [item["target"] for item in target_decisions if not item["accepted"]]
      if not north_star["ok"]:
          failed_targets.append("protected-north-star")
      decision = SelfImproveDecision(
          accepted=not failed_targets,
          baseline_score=float(baseline.get("score", 0.0)),
          candidate_score=float(candidate.get("score", 0.0)),
          requires_human_adr=False,
          reason=(
              "all changed targets improved"
              if not failed_targets
              else "protected north-star regressed"
              if failed_targets == ["protected-north-star"]
              else f"target(s) did not improve: {', '.join(failed_targets)}"
          ),
      )
      payload = {
          "decision": decision.to_dict(),
          "targets": targets,
          "ineligible_paths": [],
          "baseline": baseline,
          "candidate": candidate,
          "target_decisions": target_decisions,
          "protected_north_star": north_star,
      }
      print(json.dumps(payload, indent=2, sort_keys=True))
      return 0 if decision.accepted else 1

    if args.command == "propose":
      payload = propose_improvements(
          Path(args.root),
          Path(args.bench),
          args.target,
          max_proposals=args.max_proposals,
          min_score=args.min_score,
      )
      dump_proposals(payload, Path(args.output) if args.output else None)
      return 0

    if args.command == "materialize":
      payload = json.loads(Path(args.proposal_json).read_text(encoding="utf-8"))
      plan = materialize_pr_plan(payload, branch_prefix=args.branch_prefix)
      dump_proposals(plan, Path(args.output) if args.output else None)
      return 0 if plan.get("plans") else 1

    return 2


def protected_north_star_check(base_root: Path, candidate_root: Path, rel_path: Path) -> dict:
    base_path = base_root / rel_path
    candidate_path = candidate_root / rel_path
    if not base_path.exists() and not candidate_path.exists():
        return {"ok": True, "path": rel_path.as_posix(), "reason": "not present"}
    if base_path.exists() and not candidate_path.exists():
        return {"ok": False, "path": rel_path.as_posix(), "reason": "candidate missing protected report"}
    if not base_path.exists() or not candidate_path.exists():
        return {"ok": True, "path": rel_path.as_posix(), "reason": "base missing protected report"}

    base = json.loads(base_path.read_text(encoding="utf-8"))
    candidate = json.loads(candidate_path.read_text(encoding="utf-8"))
    base_score = float(base.get("truth_score", base.get("score", 0.0)))
    candidate_score = float(candidate.get("truth_score", candidate.get("score", 0.0)))
    return {
        "ok": candidate_score + 1e-12 >= base_score,
        "path": rel_path.as_posix(),
        "baseline_score": base_score,
        "candidate_score": candidate_score,
        "reason": "ok" if candidate_score + 1e-12 >= base_score else "candidate protected score regressed",
    }


if __name__ == "__main__":
    raise SystemExit(main())
