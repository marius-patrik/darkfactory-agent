import assert from "node:assert/strict";
import test from "node:test";

import {
  buildCleanPlan,
  planSetupConvergence,
  verifyCleanPlanAdmission,
  type CleanBranchEvidence,
  type CleanEvidence,
  type DoctorReport
} from "../src/operator.js";

function report(findings: DoctorReport["findings"], repository = "marius-patrik/example"): DoctorReport {
  return {
    schema_version: 2,
    target_repository: repository,
    lifecycle: "active",
    source_refs: { main: "main-sha", dev: "dev-sha" },
    findings
  };
}

test("setup plan orders trusted convergence stages and preserves owner residue", () => {
  const plan = planSetupConvergence([report([
    { id: "root-prd-missing", category: "PRD drift", message: "missing", severity: "error", repair_class: "pr" },
    { id: "runner-health", category: "runner health", message: "offline", severity: "critical", repair_class: "auto" },
    { id: "required-secret-key-missing", category: "configuration prerequisites", message: "owner secret", severity: "critical", repair_class: "owner" },
    { id: "label-df-ready-missing", category: "configuration prerequisites", message: "label", severity: "error", repair_class: "auto" },
    { id: "protection-main-strict-missing", category: "branch protection", message: "strict", severity: "critical", repair_class: "auto" }
  ])]);

  assert.deepEqual(plan.actions.map((action) => action.stage), [
    "machine-wiring",
    "settings-enforcement",
    "settings-enforcement",
    "issue-lane-cut"
  ]);
  assert.deepEqual(plan.residue.map((item) => [item.findingId, item.repairClass]), [
    ["required-secret-key-missing", "owner"]
  ]);
});

test("setup admits only auto-class machine-runtime deltas to the trusted machine-wiring executor", () => {
  const plan = planSetupConvergence([report([{
    id: "provider-route-probe-unavailable",
    category: "machine runtime",
    message: "route probe missing",
    severity: "critical",
    repair_class: "auto"
  }])]);

  assert.deepEqual(plan.actions.map((action) => ({
    stage: action.stage,
    operation: action.operation,
    supported: action.supported
  })), [{ stage: "machine-wiring", operation: "converge-machine-runtime", supported: true }]);
  assert.deepEqual(plan.residue, []);
});

test("setup preserves blocked machine authority findings without creating a mutation action", () => {
  const plan = planSetupConvergence([report([{
    id: "provider-route-probe-unavailable",
    category: "machine runtime",
    message: "route probe missing",
    severity: "critical",
    repair_class: "blocked"
  }])]);

  assert.deepEqual(plan.actions, []);
  assert.deepEqual(plan.residue.map((item) => [item.findingId, item.repairClass]), [["provider-route-probe-unavailable", "blocked"]]);
});

test("setup routes an absent managed registry entry through the reviewed registration stage", () => {
  const plan = planSetupConvergence([report([{
    id: "managed-registry-entry-missing",
    category: "registration",
    message: "target is absent",
    severity: "critical",
    repair_class: "pr"
  }])]);

  assert.deepEqual(plan.actions.map((action) => ({
    stage: action.stage,
    operation: action.operation,
    supported: action.supported
  })), [{ stage: "registration", operation: "converge-registration", supported: true }]);
  assert.deepEqual(plan.residue, []);
});

test("setup initializes an empty repository before managed content and enforcement", () => {
  const plan = planSetupConvergence([report([{
    id: "default-branch-head-missing",
    category: "branch policy",
    message: "empty repository",
    severity: "critical",
    repair_class: "auto"
  }])]);
  assert.deepEqual(plan.actions.map((action) => [action.stage, action.operation]), [["repository-bootstrap", "initialize-repository"]]);
});

test("setup delegates branch reconciliation and release residue to the trusted release engine", () => {
  const plan = planSetupConvergence([report([
    { id: "dev-behind-main", category: "branch convergence", message: "behind", severity: "error", repair_class: "pr" },
    { id: "release-pr-missing", category: "release lane", message: "missing", severity: "error", repair_class: "pr" }
  ])]);
  assert.deepEqual(plan.actions.map((action) => [action.stage, action.operation]), [
    ["verification", "reconcile-branches"],
    ["verification", "converge-release"]
  ]);
});

test("setup blocks code-repository convergence for the exact canonical main-only data repositories", () => {
  for (const repository of ["marius-patrik/Andromeda-data", "MARIUS-PATRIK/DARKFACTORY-DATA"]) {
    const plan = planSetupConvergence([report([{
      id: "protection-main-admin-bypass",
      category: "branch protection",
      message: "unsafe main-only posture",
      severity: "critical",
      repair_class: "auto"
    }], repository)]);

    assert.deepEqual(plan.actions.map((action) => ({
      operation: action.operation,
      supported: action.supported
    })), [{ operation: "main-only-data-boundary", supported: false }]);
    assert.deepEqual(plan.residue.map((item) => [item.findingId, item.repairClass]), [["protection-main-admin-bypass", "blocked"]]);
  }
});

test("setup does not infer main-only data policy from a repository-name suffix", () => {
  const plan = planSetupConvergence([report([{
    id: "protection-main-strict-missing",
    category: "branch protection",
    message: "normal code repository drift",
    severity: "critical",
    repair_class: "auto"
  }], "marius-patrik/product-data")]);

  assert.deepEqual(plan.actions.map((action) => ({
    operation: action.operation,
    supported: action.supported
  })), [{ operation: "converge-settings", supported: true }]);
  assert.deepEqual(plan.residue, []);
});

test("clean plan deletes only exact independently preserved branch heads", () => {
  const safe = branch({
    name: "merged-feature",
    head: "feature-sha",
    tree: "feature-tree",
    containedBy: ["main"],
    treeEquivalentTo: []
  });
  const plan = buildCleanPlan(evidence([safe]), new Date("2026-07-15T00:00:00Z"));

  assert.deepEqual(plan.entries.map((entry) => [entry.target, entry.classification, entry.action]), [
    ["merged-feature", "proven-merged", "delete"]
  ]);
});

test("clean plan removes a clean worktree only when its exact head is independently preserved", () => {
  const safe = branch({
    name: "merged-worktree",
    containedBy: ["dev"],
    worktrees: [{ pathId: "wt-safe", branch: "merged-worktree", head: "merged-worktree-sha", dirty: false, untracked: false, submoduleDirty: false }]
  });
  const plan = buildCleanPlan(evidence([safe]), new Date("2026-07-15T00:00:00Z"));

  assert.deepEqual(plan.entries.map((entry) => [entry.kind, entry.action]), [
    ["remote-branch", "delete"],
    ["worktree", "remove"]
  ]);
});

test("clean plan never targets the explicitly supplied root checkout", () => {
  const safe = branch({
    name: "merged-root",
    containedBy: ["dev"],
    worktrees: [{ pathId: "wt-root", branch: "merged-root", head: "merged-root-sha", dirty: false, untracked: false, submoduleDirty: false, rootCheckout: true }]
  });
  const plan = buildCleanPlan(evidence([safe], {
    orphanRefs: [{
      ref: "refs/df/root-evidence",
      head: "merged-root-sha",
      tree: "merged-root-tree",
      independentlyPreservedBy: ["branch:dev"],
      worktree: safe.worktrees[0],
      cleanupCandidate: true
    }]
  }), new Date("2026-07-15T00:00:00Z"));

  assert.ok(plan.entries.some((entry) => entry.kind === "remote-branch" && entry.target === "merged-root" && entry.action === "preserve" && entry.classification === "active-worktree"));
  assert.ok(plan.entries.some((entry) => entry.kind === "worktree" && entry.target === "wt-root" && entry.action === "preserve" && entry.classification === "active-worktree"));
  assert.ok(plan.entries.some((entry) => entry.kind === "orphan-ref" && entry.target === "refs/df/root-evidence" && entry.action === "preserve"));
});

test("clean plan preserves dirty, unpublished, open-PR, and ambiguous human work", () => {
  const branches = [
    branch({ name: "dirty", worktrees: [{ pathId: "wt-dirty", branch: "dirty", head: "dirty-sha", dirty: true, untracked: false, submoduleDirty: false }] }),
    branch({ name: "unpublished", localUnpublished: true }),
    branch({ name: "review", openPullRequest: 7 }),
    branch({ name: "unknown" })
  ];
  const plan = buildCleanPlan(evidence(branches), new Date("2026-07-15T00:00:00Z"));
  const remoteEntries = plan.entries.filter((entry) => entry.kind === "remote-branch");

  assert.deepEqual(remoteEntries.map((entry) => [entry.target, entry.classification, entry.action]), [
    ["dirty", "dirty-worktree", "preserve"],
    ["review", "open-pr", "preserve"],
    ["unknown", "ambiguous", "preserve"],
    ["unpublished", "unpublished", "preserve"]
  ]);
});

test("clean plan enumerates and removes only independently preserved local branches", () => {
  const plan = buildCleanPlan(evidence([], {
    localBranches: [
      branch({ name: "merged-local", containedBy: ["dev"] }),
      branch({ name: "unpublished-local", localUnpublished: true }),
      branch({ name: "dirty-local", worktrees: [{ pathId: "wt-local", branch: "dirty-local", head: "dirty-local-sha", dirty: false, untracked: true, submoduleDirty: false }] })
    ]
  }), new Date("2026-07-15T00:00:00Z"));

  assert.deepEqual(plan.entries.filter((entry) => entry.kind === "local-branch").map((entry) => [entry.target, entry.classification, entry.action]), [
    ["dirty-local", "dirty-worktree", "preserve"],
    ["merged-local", "proven-merged", "delete"],
    ["unpublished-local", "unpublished", "preserve"]
  ]);
  assert.ok(plan.entries.some((entry) => entry.kind === "worktree" && entry.target === "wt-local" && entry.action === "preserve"));
});

test("clean plan classifies every open PR and issue while scheduling only evidence-backed review work", () => {
  const baseSha = "b".repeat(40);
  const head3 = "3".repeat(40);
  const head4 = "4".repeat(40);
  const plan = buildCleanPlan(evidence([], {
    pullRequests: [
      { number: 3, version: `${baseSha}:${head3}`, base: "dev", baseSha, headRef: "feature/3", head: head3, classification: "active", findingIds: [], autoreview: "current", successor: null },
      { number: 4, version: `${baseSha}:${head4}`, base: "dev", baseSha, headRef: "feature/4", head: head4, classification: "red", findingIds: ["pr-4-red"], autoreview: "failed", successor: null }
    ],
    issues: [
      { number: 7, fingerprint: "7".repeat(64), classification: "current", findingIds: [], reviewable: false, autoreview: "missing" },
      { number: 8, fingerprint: "8".repeat(64), classification: "finding", findingIds: ["issue-8-blocker-2-missing"], reviewable: true, autoreview: "stale" }
    ],
    reviewFindings: [{ id: "issue-8-blocker-2-missing", category: "issue lane", severity: "error", repairClass: "pr", message: "Issue #8 names a missing blocker.", evidence: [], fingerprint: "finding-8" }]
  }), new Date("2026-07-15T00:00:00Z"));

  assert.deepEqual(plan.entries.filter((entry) => entry.kind === "pull-request").map((entry) => [entry.target, entry.classification, entry.action]), [
    ["#3", "active", "preserve"],
    ["#4", "red", "autoreview"]
  ]);
  assert.deepEqual(plan.entries.filter((entry) => entry.kind === "issue").map((entry) => [entry.target, entry.classification, entry.action]), [
    ["#7", "current", "preserve"],
    ["#8", "finding", "autoreview"]
  ]);
  assert.ok(plan.entries.some((entry) => entry.kind === "lane-finding" && entry.target === "issue-8-blocker-2-missing"));
  assert.equal(plan.entries.find((entry) => entry.kind === "lane-finding")?.action, "preserve");
});

test("clean plan closes only superseded or abandoned PRs with an exact independently proven successor", () => {
  const baseSha = "b".repeat(40);
  const sourceHead = "1".repeat(40);
  const successorHead = "2".repeat(40);
  const successor = {
    number: 20,
    version: `${baseSha}:${successorHead}`,
    base: "dev",
    baseSha,
    headRef: "feature/successor",
    head: successorHead,
    proof: "head-ancestry" as const
  };
  const pull = (number: number, classification: "active" | "superseded" | "abandoned", withSuccessor: boolean) => ({
    number,
    version: `${baseSha}:${sourceHead}`,
    base: "dev",
    baseSha,
    headRef: `feature/${number}`,
    head: sourceHead,
    classification,
    findingIds: [],
    autoreview: "current" as const,
    successor: withSuccessor ? successor : null
  });
  const plan = buildCleanPlan(evidence([], {
    pullRequests: [
      pull(1, "superseded", true),
      pull(2, "abandoned", true),
      pull(3, "superseded", false),
      pull(4, "abandoned", false),
      pull(5, "active", true)
    ]
  }));

  assert.deepEqual(plan.entries.filter((entry) => entry.kind === "pull-request").map((entry) => [entry.target, entry.action]), [
    ["#1", "close"],
    ["#2", "close"],
    ["#3", "preserve"],
    ["#4", "preserve"],
    ["#5", "preserve"]
  ]);
});

test("clean apply admission aborts when any observed fact drifts", () => {
  const original = buildCleanPlan(evidence([branch({ name: "merged", containedBy: ["main"] })]), new Date("2026-07-15T00:00:00Z"));
  const drifted = buildCleanPlan(evidence([branch({ name: "merged", head: "new-head", containedBy: ["main"] })]), new Date("2026-07-15T00:01:00Z"));

  assert.throws(() => verifyCleanPlanAdmission(original, drifted), /evidence drifted/);
});

function branch(overrides: Partial<CleanBranchEvidence>): CleanBranchEvidence {
  return {
    name: "feature",
    head: `${overrides.name || "feature"}-sha`,
    tree: `${overrides.name || "feature"}-tree`,
    protected: false,
    policyBranch: false,
    openPullRequest: null,
    mergedPullRequest: null,
    mergedPullHead: null,
    containedBy: [],
    treeEquivalentTo: [],
    localAhead: null,
    localUnpublished: false,
    worktrees: [],
    ...overrides
  };
}

function evidence(branches: CleanBranchEvidence[], overrides: Partial<CleanEvidence> = {}): CleanEvidence {
  return {
    repository: "marius-patrik/example",
    defaultBranch: "main",
    observedRefs: { main: "main-sha", dev: "dev-sha" },
    branches,
    localBranches: [],
    orphanRefs: [],
    detachedWorktrees: [],
    pullRequests: [],
    issues: [],
    reviewFindings: [],
    pullRequestFingerprint: "prs-v1",
    issueLaneFingerprint: "issues-v1",
    prdFingerprint: "prd-v1",
    ...overrides
  };
}
