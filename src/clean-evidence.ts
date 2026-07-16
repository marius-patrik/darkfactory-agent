import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { resolve } from "node:path";
import {
  AUTOREVIEW_RESULT_MARKER,
  AUTOREVIEW_TARGET_VERSION_MARKER_PREFIX,
  isTrustedDarkFactoryComment,
  resolveEffectiveIssueContent
} from "./issue-spec.js";

import {
  buildCleanPlan,
  stableHash,
  verifyCleanPlanAdmission,
  type CleanBranchEvidence,
  type CleanEvidence,
  type CleanPlan,
  type CleanPlanEntry,
  type CleanWorktreeEvidence,
  type DoctorFinding,
  type PullRequestClassification
} from "./operator.js";

const AUTOREVIEW_CHECK_NAME = "DarkFactory Autoreview";
const TRUSTED_ACTIONS_APP_ID = 15368;
const AUTOREVIEW_WORKFLOW = "darkfactory-autoreview.yml";
const CLEAN_CLOSURE_MARKER = "<!-- darkfactory:clean-pr-closure";
const CLEAN_AUTOREVIEW_MARKER = "<!-- darkfactory:clean-autoreview";
const AUTOREVIEW_PENDING_MAX_AGE_MS = 3 * 60 * 60 * 1_000;
const ISSUE_VERSION = /^[0-9a-f]{64}$/;
const PULL_REQUEST_VERSION = /^[0-9a-f]{40}:[0-9a-f]{40}$/;

export interface OperatorGitHubRequester {
  request(route: string, parameters: Record<string, unknown>): Promise<{ data: unknown }>;
  graphql?(query: string, variables?: Record<string, unknown>): Promise<unknown>;
}

export interface RepositoryRef {
  owner: string;
  repo: string;
}

interface LocalCleanEvidence {
  worktreesByBranch: Map<string, CleanWorktreeEvidence[]>;
  rawWorktreeById: Map<string, string>;
  detachedWorktrees: CleanWorktreeEvidence[];
  localBranches: Map<string, {
    head: string;
    tree: string;
    ahead: number | null;
    unpublished: boolean;
    containedBy: string[];
    treeEquivalentTo: string[];
  }>;
  orphanRefs: CleanEvidence["orphanRefs"];
}

export async function collectCleanEvidence(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  localPath = "",
  reviewFindings: DoctorFinding[] = []
): Promise<CleanEvidence> {
  const metadata = asRecord((await github.request("GET /repos/{owner}/{repo}", { ...repository })).data, "repository metadata");
  const defaultBranch = requiredString(metadata.default_branch, "repository default branch");
  const branches = await listPages(github, "GET /repos/{owner}/{repo}/branches", { ...repository });
  const pulls = await listPages(github, "GET /repos/{owner}/{repo}/pulls", { ...repository, state: "all", sort: "updated", direction: "desc" });
  const issues = await listPages(github, "GET /repos/{owner}/{repo}/issues", { ...repository, state: "open", sort: "updated", direction: "desc" });
  const comments = issues.length > 0
    ? await listPages(github, "GET /repos/{owner}/{repo}/issues/comments", { ...repository, sort: "created", direction: "asc" })
    : [];
  const commentsByIssue = groupIssueComments(comments);
  const branchRecords = branches.map((branch, index) => asRecord(branch, `branch ${index}`));
  const branchHeads = new Map<string, string>();
  for (const branch of branchRecords) {
    const name = requiredString(branch.name, "branch name");
    const commit = asRecord(branch.commit, `branch ${name} commit`);
    branchHeads.set(name, requiredString(commit.sha, `branch ${name} head`));
  }
  // Policy names remain immutable even when the corresponding remote ref is
  // missing. A local-only main/dev is recovery evidence, never cleanup.
  const policyBranchNames = new Set([defaultBranch, "main", "dev"]);
  const policyBranches = new Set([...policyBranchNames].filter((name) => branchHeads.has(name)));
  const trees = new Map<string, string>();
  for (const [name, head] of branchHeads) trees.set(name, await commitTree(github, repository, head));
  const local = localPath
    ? collectLocalEvidence(localPath, repository, branchHeads, trees, policyBranchNames)
    : emptyLocalEvidence();

  const normalizedPulls = pulls.map((pull, index) => normalizePull(pull, index, repository));
  const cleanBranches: CleanBranchEvidence[] = [];
  for (const branch of branchRecords) {
    const name = requiredString(branch.name, "branch name");
    const head = branchHeads.get(name)!;
    const openPull = normalizedPulls.find((pull) => pull.state === "open" && pull.headRef === name && pull.headSha === head && pull.sameRepository);
    const mergedPull = normalizedPulls.find((pull) => pull.merged && pull.headRef === name && pull.headSha === head && pull.sameRepository);
    const containedBy: string[] = [];
    for (const policy of policyBranches) {
      if (policy === name) continue;
      if (await isHeadContainedBy(github, repository, head, policy)) containedBy.push(policy);
    }
    const tree = trees.get(name)!;
    const treeEquivalentTo = [...policyBranches]
      .filter((policy) => policy !== name && trees.get(policy) === tree)
      .sort();
    const localBranch = local.localBranches.get(name);
    cleanBranches.push({
      name,
      head,
      tree,
      protected: branch.protected === true,
      policyBranch: policyBranchNames.has(name),
      openPullRequest: openPull?.number ?? null,
      mergedPullRequest: mergedPull?.number ?? null,
      mergedPullHead: mergedPull?.headSha ?? null,
      containedBy: containedBy.sort(),
      treeEquivalentTo,
      localAhead: localBranch?.ahead ?? null,
      localUnpublished: localBranch?.unpublished ?? false,
      worktrees: local.worktreesByBranch.get(name) ?? []
    });
  }

  const localBranches: CleanBranchEvidence[] = [...local.localBranches].map(([name, branch]) => {
    const openPull = normalizedPulls.find((pull) => pull.state === "open" && pull.headRef === name && pull.headSha === branch.head && pull.sameRepository);
    const mergedPull = normalizedPulls.find((pull) => pull.merged && pull.headRef === name && pull.headSha === branch.head && pull.sameRepository);
    return {
      name,
      head: branch.head,
      tree: branch.tree,
      protected: false,
      policyBranch: policyBranchNames.has(name),
      openPullRequest: openPull?.number ?? null,
      mergedPullRequest: mergedPull?.number ?? null,
      mergedPullHead: mergedPull?.headSha ?? null,
      containedBy: branch.containedBy,
      treeEquivalentTo: branch.treeEquivalentTo,
      localAhead: branch.ahead,
      localUnpublished: branch.unpublished,
      worktrees: local.worktreesByBranch.get(name) ?? []
    };
  });

  const stableReviewFindings = reviewFindings.map((finding) => ({
    id: finding.id,
    category: finding.category,
    severity: finding.severity,
    repairClass: finding.repair_class,
    message: finding.message,
    evidence: finding.evidence || [],
    fingerprint: stableHash({
      id: finding.id,
      category: finding.category,
      severity: finding.severity,
      repairClass: finding.repair_class,
      message: finding.message,
      evidence: finding.evidence || [],
      repair: finding.repair || []
    })
  })).sort((a, b) => a.id.localeCompare(b.id));
  const openPulls: CleanEvidence["pullRequests"] = [];
  for (const pull of normalizedPulls.filter((candidate) => candidate.state === "open")) {
    const findingIds = reviewFindings.filter((finding) => findingTouchesPull(finding, pull.number)).map((finding) => finding.id).sort();
    const claimedSuccessor = successorNumber(pull.body);
    let successor: CleanEvidence["pullRequests"][number]["successor"] = null;
    if (claimedSuccessor !== null && claimedSuccessor !== pull.number) {
      const candidate = normalizedPulls.find((entry) => entry.number === claimedSuccessor);
      if (
        candidate
        && pull.sameRepository
        && candidate.sameRepository
        && candidate.baseRef === pull.baseRef
        && (candidate.state === "open" || candidate.merged)
      ) {
        const comparison = await compareCommits(github, repository, pull.headSha, candidate.headSha);
        if (comparison.ancestor) {
          successor = successorEvidence(candidate, "head-ancestry");
        } else {
          const [pullTree, successorTree] = await Promise.all([
            commitTree(github, repository, pull.headSha),
            commitTree(github, repository, candidate.headSha)
          ]);
          if (pullTree === successorTree) successor = successorEvidence(candidate, "tree-equivalence");
        }
      }
    }
    openPulls.push({
      number: pull.number,
      version: pullVersion(pull),
      base: pull.baseRef,
      baseSha: pull.baseSha,
      headRef: pull.headRef,
      head: pull.headSha,
      classification: classifyPullRequest(findingIds, pull.body),
      findingIds,
      autoreview: await observePullAutoreview(github, repository, pull, commentsByIssue.get(pull.number) ?? []),
      successor
    });
  }
  const openIssues: CleanEvidence["issues"] = [];
  for (const issue of issues.filter((candidate) => !asRecord(candidate, "issue").pull_request)) {
    const value = asRecord(issue, "issue");
    const number = requiredNumber(value.number, "issue number");
    const issueFindings = reviewFindings.filter((finding) => findingTouchesIssue(finding, number));
    const findingIds = issueFindings.map((finding) => finding.id).sort();
    const issueComments = commentsByIssue.get(number) ?? [];
    const effective = resolveEffectiveIssueContent(value, issueComments);
    const fingerprint = effective.version;
    openIssues.push({
      number,
      fingerprint,
      classification: findingIds.length ? "finding" as const : "current" as const,
      findingIds,
      reviewable: issueFindings.length > 0 && issueFindings.every((finding) => finding.repair_class === "pr"),
      autoreview: observeIssueAutoreview(issueComments, fingerprint)
    });
  }

  const prdFingerprint = await contentFingerprint(github, repository, "PRD.md", defaultBranch);
  return {
    repository: `${repository.owner}/${repository.repo}`,
    defaultBranch,
    observedRefs: Object.fromEntries([...branchHeads].filter(([name]) => policyBranches.has(name)).sort(([a], [b]) => a.localeCompare(b))),
    branches: cleanBranches.sort((a, b) => a.name.localeCompare(b.name)),
    localBranches: localBranches.sort((a, b) => a.name.localeCompare(b.name)),
    orphanRefs: local.orphanRefs,
    detachedWorktrees: local.detachedWorktrees,
    pullRequests: openPulls.sort((a, b) => a.number - b.number),
    issues: openIssues.sort((a, b) => a.number - b.number),
    reviewFindings: stableReviewFindings,
    pullRequestFingerprint: stableHash(normalizedPulls.map((pull) => ({
      number: pull.number,
      state: pull.state,
      merged: pull.merged,
      headRef: pull.headRef,
      headSha: pull.headSha,
      baseRef: pull.baseRef,
      baseSha: pull.baseSha,
      body: pull.body,
      sameRepository: pull.sameRepository,
      updatedAt: pull.updatedAt
    }))),
    issueLaneFingerprint: stableHash(openIssues.map((issue) => ({
      number: issue.number,
      version: issue.fingerprint,
      autoreview: issue.autoreview,
      findingIds: issue.findingIds
    }))),
    prdFingerprint
  };
}

export interface CleanApplyReceipt {
  planId: string;
  repository: string;
  actions: Array<{
    kind: string;
    target: string;
    head: string;
    version?: string;
    base?: string;
    baseSha?: string;
    headRef?: string;
    successor?: CleanPlanEntry["successor"];
    status: "applied" | "skipped";
    reason: string;
  }>;
}

interface CleanApplyOptions {
  localPath?: string;
  onAdmission?: (receipt: CleanApplyReceipt["actions"][number]) => Promise<void>;
  onCompletion?: (receipt: CleanApplyReceipt["actions"][number]) => Promise<void>;
}

export async function applyCleanPlan(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  saved: CleanPlan,
  freshEvidence: CleanEvidence,
  options: CleanApplyOptions = {}
): Promise<CleanApplyReceipt> {
  const fresh = buildCleanPlan(freshEvidence, new Date(saved.createdAt));
  verifyCleanPlanAdmission(saved, fresh);
  const local = options.localPath
    ? collectLocalEvidence(
      options.localPath,
      repository,
      new Map(freshEvidence.branches.map((branch) => [branch.name, branch.head])),
      new Map(freshEvidence.branches.map((branch) => [branch.name, branch.tree])),
      new Set([freshEvidence.defaultBranch, "main", "dev"])
    )
    : emptyLocalEvidence();
  const actions: CleanApplyReceipt["actions"] = [];

  // Remove exact clean worktree copies first. `git worktree remove` has no
  // force flag here and therefore remains fail-closed if Git observes drift.
  for (const entry of saved.entries.filter((candidate) => candidate.kind === "worktree" && candidate.action === "remove")) {
    const rawPath = local.rawWorktreeById.get(entry.target);
    if (!options.localPath || !rawPath) throw new Error(`clean worktree ${entry.target} is no longer observable; apply aborted`);
    await recordAdmission({ kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "admitted exact clean preserved worktree removal" }, options.onAdmission);
    revalidateWorktreeRemoval(options.localPath, rawPath, entry);
    runGit(options.localPath, ["worktree", "remove", "--", rawPath]);
    await recordCompleted(actions, { kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "exact clean preserved worktree removed" }, options.onCompletion);
  }

  for (const entry of saved.entries.filter((candidate) => candidate.kind === "orphan-ref" && candidate.action === "delete")) {
    if (!options.localPath) throw new Error(`clean orphan ref ${entry.target} requires an explicit local checkout`);
    await recordAdmission({ kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "admitted atomic exact-head deletion after preservation proof" }, options.onAdmission);
    runGit(options.localPath, ["update-ref", "-d", entry.target, entry.head]);
    await recordCompleted(actions, { kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "atomic exact-head deletion after independent preservation proof" }, options.onCompletion);
  }

  for (const entry of saved.entries.filter((candidate) => candidate.kind === "local-branch" && candidate.action === "delete")) {
    if (!options.localPath) throw new Error(`clean local branch ${entry.target} requires an explicit local checkout`);
    await recordAdmission({ kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "admitted atomic exact-head deletion after preservation proof" }, options.onAdmission);
    runGit(options.localPath, ["update-ref", "-d", `refs/heads/${entry.target}`, entry.head]);
    await recordCompleted(actions, { kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "atomic exact-head deletion after independent preservation proof" }, options.onCompletion);
  }

  for (const entry of saved.entries.filter((candidate) => candidate.kind === "remote-branch" && candidate.action === "delete")) {
    const current = asRecord((await github.request("GET /repos/{owner}/{repo}/git/ref/{ref}", {
      ...repository,
      ref: `heads/${entry.target}`
    })).data, `remote branch ${entry.target}`);
    const object = asRecord(current.object, `remote branch ${entry.target} object`);
    if (object.sha !== entry.head) throw new Error(`remote branch ${entry.target} drifted immediately before deletion; apply aborted`);
    await recordAdmission({ kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "admitted exact remote head deletion after preservation proof" }, options.onAdmission);
    const admitted = asRecord((await github.request("GET /repos/{owner}/{repo}/git/ref/{ref}", {
      ...repository,
      ref: `heads/${entry.target}`
    })).data, `remote branch ${entry.target}`);
    const admittedObject = asRecord(admitted.object, `remote branch ${entry.target} object`);
    if (admittedObject.sha !== entry.head) throw new Error(`remote branch ${entry.target} drifted after admission; apply aborted`);
    await github.request("DELETE /repos/{owner}/{repo}/git/refs/{ref}", { ...repository, ref: `heads/${entry.target}` });
    await recordCompleted(actions, { kind: entry.kind, target: entry.target, head: entry.head, status: "applied", reason: "exact head independently preserved and re-fetched" }, options.onCompletion);
  }

  for (const entry of saved.entries.filter((candidate) => candidate.action === "autoreview")) {
    await applyAutoreviewAction(github, repository, entry, actions, options);
  }

  for (const entry of saved.entries.filter((candidate) => candidate.kind === "pull-request" && candidate.action === "close")) {
    await applyPullClosureAction(github, repository, entry, actions, options);
  }

  for (const entry of saved.entries.filter((candidate) => candidate.action === "preserve")) {
    await recordCompleted(actions, actionReceipt(entry, "skipped", entry.reasons.join(" ")), options.onCompletion);
  }
  return { planId: saved.planId, repository: saved.repository, actions };
}

async function recordAdmission(
  receipt: CleanApplyReceipt["actions"][number],
  callback?: (receipt: CleanApplyReceipt["actions"][number]) => Promise<void>
): Promise<void> {
  if (callback) await callback(receipt);
}

async function recordCompleted(
  actions: CleanApplyReceipt["actions"],
  receipt: CleanApplyReceipt["actions"][number],
  callback?: (receipt: CleanApplyReceipt["actions"][number]) => Promise<void>
): Promise<void> {
  actions.push(receipt);
  if (callback) await callback(receipt);
}

async function applyAutoreviewAction(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  entry: CleanPlanEntry,
  actions: CleanApplyReceipt["actions"],
  options: CleanApplyOptions
): Promise<void> {
  if ((entry.kind !== "issue" && entry.kind !== "pull-request") || !entry.version) {
    throw new Error(`clean Autoreview target ${entry.target} lacks an exact version`);
  }
  validateAutoreviewVersion(entry.kind, entry.version);
  const number = cleanTargetNumber(entry);
  const before = await readExactAutoreviewTarget(github, repository, entry, number);
  if (before.autoreview === "current" || before.autoreview === "pending") {
    await recordCompleted(
      actions,
      actionReceipt(entry, "skipped", `Exact ${entry.version} Autoreview is already ${before.autoreview}; duplicate dispatch suppressed.`),
      options.onCompletion
    );
    return;
  }
  await assertAutoreviewWorkflowActive(github, repository);

  await recordAdmission(
    actionReceipt(entry, "applied", `Admitted exact ${entry.version} Autoreview dispatch after observing ${before.autoreview} evidence.`),
    options.onAdmission
  );
  const admitted = await readExactAutoreviewTarget(github, repository, entry, number);
  if (admitted.autoreview === "current" || admitted.autoreview === "pending") {
    await recordCompleted(
      actions,
      actionReceipt(entry, "skipped", `Exact ${entry.version} became ${admitted.autoreview} after admission; duplicate dispatch suppressed.`),
      options.onCompletion
    );
    return;
  }

  const pendingResponse = await github.request("POST /repos/{owner}/{repo}/issues/{issue_number}/comments", {
    ...repository,
    issue_number: number,
    body: renderCleanAutoreviewComment(entry, number, "pending", "The exact target was re-fetched after durable admission; workflow dispatch is pending.")
  });
  const pendingComment = asRecord(pendingResponse.data, `clean Autoreview pending comment for ${entry.target}`);
  const pendingCommentId = requiredNumber(pendingComment.id, `clean Autoreview pending comment id for ${entry.target}`);

  const marked = await readExactAutoreviewTarget(github, repository, entry, number);
  if (marked.autoreview === "current") {
    await recordCompleted(
      actions,
      actionReceipt(entry, "applied", `Pending evidence comment #${pendingCommentId} was published, but exact Autoreview completed concurrently; dispatch suppressed.`),
      options.onCompletion
    );
    return;
  }
  const canonicalPending = firstPendingAutoreviewComment(marked.comments, entry.kind, number, entry.version);
  if (canonicalPending !== null && canonicalPending !== pendingCommentId) {
    await recordCompleted(
      actions,
      actionReceipt(entry, "applied", `Pending evidence comment #${pendingCommentId} was published; dispatch deduplicated behind canonical comment #${canonicalPending}.`),
      options.onCompletion
    );
    return;
  }

  await assertAutoreviewWorkflowActive(github, repository);
  await readExactAutoreviewTarget(github, repository, entry, number);
  try {
    await github.request("POST /repos/{owner}/{repo}/actions/workflows/{workflow_id}/dispatches", {
      ...repository,
      workflow_id: AUTOREVIEW_WORKFLOW,
      ref: "main",
      inputs: {
        target_kind: entry.kind === "issue" ? "issue" : "pull_request",
        target_number: String(number),
        target_version: entry.version
      }
    });
  } catch (error) {
    await readExactAutoreviewTarget(github, repository, entry, number);
    await github.request("POST /repos/{owner}/{repo}/issues/{issue_number}/comments", {
      ...repository,
      issue_number: number,
      body: renderCleanAutoreviewComment(entry, number, "failed", "GitHub rejected the evidence-bound workflow dispatch; the next clean plan may retry this exact version.")
    });
    throw error;
  }
  await recordCompleted(
    actions,
    actionReceipt(entry, "applied", `GitHub accepted ${AUTOREVIEW_WORKFLOW} for exact ${entry.version} after pending evidence comment #${pendingCommentId}.`),
    options.onCompletion
  );
}

async function applyPullClosureAction(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  entry: CleanPlanEntry,
  actions: CleanApplyReceipt["actions"],
  options: CleanApplyOptions
): Promise<void> {
  if (!entry.version || !entry.successor || (entry.classification !== "superseded" && entry.classification !== "abandoned")) {
    throw new Error(`clean pull-request closure ${entry.target} lacks exact successor authority`);
  }
  const number = cleanTargetNumber(entry);
  await revalidatePullClosure(github, repository, number, entry);
  await recordAdmission(
    actionReceipt(entry, "applied", `Admitted ${entry.classification} closure after exact ${entry.successor.proof} successor proof.`),
    options.onAdmission
  );

  await revalidatePullClosure(github, repository, number, entry);
  const comments = await listPages(github, "GET /repos/{owner}/{repo}/issues/{issue_number}/comments", {
    ...repository,
    issue_number: number
  });
  const hasClosureReceipt = comments.some((comment) => isTrustedClosureComment(comment, entry));
  if (!hasClosureReceipt) {
    await revalidatePullClosure(github, repository, number, entry);
    await github.request("POST /repos/{owner}/{repo}/issues/{issue_number}/comments", {
      ...repository,
      issue_number: number,
      body: renderPullClosureComment(entry)
    });
  }

  await revalidatePullClosure(github, repository, number, entry);
  await github.request("PATCH /repos/{owner}/{repo}/pulls/{pull_number}", {
    ...repository,
    pull_number: number,
    state: "closed"
  });
  const closed = normalizePull((await github.request("GET /repos/{owner}/{repo}/pulls/{pull_number}", {
    ...repository,
    pull_number: number
  })).data, number, repository);
  assertPullIdentity(closed, entry, false);
  if (closed.state !== "closed") throw new Error(`pull request ${entry.target} closure was not confirmed`);
  await recordCompleted(
    actions,
    actionReceipt(entry, "applied", `Closed with exact ${entry.successor.proof} successor PR #${entry.successor.number}; source and successor versions are receipt-bound.`),
    options.onCompletion
  );
}

function actionReceipt(
  entry: CleanPlanEntry,
  status: "applied" | "skipped",
  reason: string
): CleanApplyReceipt["actions"][number] {
  return {
    kind: entry.kind,
    target: entry.target,
    head: entry.head,
    ...(entry.version ? { version: entry.version } : {}),
    ...(entry.base ? { base: entry.base } : {}),
    ...(entry.baseSha ? { baseSha: entry.baseSha } : {}),
    ...(entry.headRef ? { headRef: entry.headRef } : {}),
    ...(entry.successor ? { successor: entry.successor } : {}),
    status,
    reason
  };
}

async function assertAutoreviewWorkflowActive(
  github: OperatorGitHubRequester,
  repository: RepositoryRef
): Promise<void> {
  const workflow = asRecord((await github.request("GET /repos/{owner}/{repo}/actions/workflows/{workflow_id}", {
    ...repository,
    workflow_id: AUTOREVIEW_WORKFLOW
  })).data, "DarkFactory Autoreview workflow metadata");
  if (workflow.state !== "active") throw new Error("DarkFactory Autoreview workflow is unavailable or inactive");
}

function collectLocalEvidence(
  localPath: string,
  repository: RepositoryRef,
  remoteBranches: Map<string, string>,
  remoteTrees = new Map<string, string>(),
  policyBranches = new Set<string>()
): LocalCleanEvidence {
  const root = resolve(localPath);
  assertLocalRepositoryIdentity(root, repository);
  const worktreesByBranch = new Map<string, CleanWorktreeEvidence[]>();
  const rawWorktreeById = new Map<string, string>();
  const detachedWorktrees: CleanWorktreeEvidence[] = [];
  const porcelain = runGit(root, ["worktree", "list", "--porcelain"]);
  const records = porcelain.split(/\r?\n\r?\n/).map((block) => block.trim()).filter(Boolean);
  for (const record of records) {
    const fields = new Map(record.split(/\r?\n/).map((line) => {
      const separator = line.indexOf(" ");
      return separator === -1 ? [line, ""] : [line.slice(0, separator), line.slice(separator + 1)];
    }));
    const worktreePath = fields.get("worktree") || "";
    const head = fields.get("HEAD") || "";
    const ref = fields.get("branch") || "";
    if (!worktreePath || !head) continue;
    const branch = ref.startsWith("refs/heads/") ? ref.slice("refs/heads/".length) : "(detached)";
    const status = runGit(worktreePath, ["status", "--porcelain=v2", "--untracked-files=all", "--ignore-submodules=none"]);
    const lines = status.split(/\r?\n/).filter(Boolean);
    const pathId = `wt-${createHash("sha256").update(resolve(worktreePath).toLowerCase()).digest("hex").slice(0, 16)}`;
    const evidence: CleanWorktreeEvidence = {
      pathId,
      branch,
      head,
      dirty: lines.some((line) => !line.startsWith("? ")),
      untracked: lines.some((line) => line.startsWith("? ")),
      submoduleDirty: lines.some((line) => /^[12u] .{2}S/.test(line)),
      rootCheckout: resolve(worktreePath).toLowerCase() === root.toLowerCase()
    };
    if (ref.startsWith("refs/heads/")) {
      worktreesByBranch.set(branch, [...(worktreesByBranch.get(branch) || []), evidence]);
    } else {
      detachedWorktrees.push(evidence);
    }
    rawWorktreeById.set(pathId, resolve(worktreePath));
  }

  const localBranches = new Map<string, {
    head: string;
    tree: string;
    ahead: number | null;
    unpublished: boolean;
    containedBy: string[];
    treeEquivalentTo: string[];
  }>();
  const refs = runGit(root, ["for-each-ref", "--format=%(refname:short)%09%(objectname)", "refs/heads"]);
  for (const line of refs.split(/\r?\n/).filter(Boolean)) {
    const [name, head] = line.split("\t");
    if (!name || !head) continue;
    const remoteHead = remoteBranches.get(name);
    const tree = runGit(root, ["rev-parse", `${head}^{tree}`]).trim();
    let ahead: number | null = null;
    if (remoteHead === head) {
      ahead = 0;
    } else if (remoteHead && gitObjectExists(root, remoteHead)) {
      const counts = runGit(root, ["rev-list", "--left-right", "--count", `${remoteHead}...${head}`]).trim().split(/\s+/).map(Number);
      ahead = Number.isFinite(counts[1]) ? counts[1] : null;
    }
    const containedBy: string[] = [];
    const treeEquivalentTo: string[] = [];
    for (const policy of policyBranches) {
      const policyHead = remoteBranches.get(policy);
      if (!policyHead) continue;
      if (remoteTrees.get(policy) === tree) treeEquivalentTo.push(policy);
      if (gitObjectExists(root, policyHead) && gitIsAncestor(root, head, policyHead)) containedBy.push(policy);
    }
    const independentlyPreserved = containedBy.length > 0 || treeEquivalentTo.length > 0;
    const unpublished = remoteHead
      ? remoteHead !== head && (ahead === null || ahead > 0)
      : !independentlyPreserved;
    localBranches.set(name, {
      head,
      tree,
      ahead,
      unpublished,
      containedBy: containedBy.sort(),
      treeEquivalentTo: treeEquivalentTo.sort()
    });
  }

  const orphanRefs: CleanEvidence["orphanRefs"] = [];
  const orphanOutput = runGit(root, ["for-each-ref", "--format=%(refname)%09%(objectname)%09%(*objectname)"]);
  const branchPreservation = new Map<string, string[]>();
  for (const [name, head] of remoteBranches) branchPreservation.set(head, [...(branchPreservation.get(head) || []), `branch:${name}`]);
  for (const line of orphanOutput.split(/\r?\n/).filter(Boolean)) {
    const [ref, object, peeled] = line.split("\t");
    const head = peeled || object;
    if (!ref || !head || ref.startsWith("refs/heads/")) continue;
    const treeProbe = spawnGit(root, ["rev-parse", `${head}^{tree}`]);
    const tree = treeProbe.status === 0 ? String(treeProbe.stdout || "").trim() : "unobservable";
    const preserved = [...(branchPreservation.get(head) || [])];
    if (preserved.length === 0 && tree !== "unobservable") {
      for (const [name, branchHead] of remoteBranches) {
        const branchTree = remoteTrees.get(name);
        if (!branchTree) continue;
        if (branchTree === tree) preserved.push(`tree:branch:${name}`);
      }
    }
    const associatedWorktree = detachedWorktrees.find((worktree) => worktree.head === head) || null;
    orphanRefs.push({
      ref,
      head,
      tree,
      independentlyPreservedBy: preserved.sort(),
      worktree: associatedWorktree,
      cleanupCandidate: /^(?:refs\/(?:df|archive|subtree)\/)/.test(ref)
    });
  }
  return {
    worktreesByBranch,
    rawWorktreeById,
    detachedWorktrees: detachedWorktrees.sort((a, b) => a.pathId.localeCompare(b.pathId)),
    localBranches,
    orphanRefs: orphanRefs.sort((a, b) => a.ref.localeCompare(b.ref))
  };
}

function emptyLocalEvidence(): LocalCleanEvidence {
  return { worktreesByBranch: new Map(), rawWorktreeById: new Map(), detachedWorktrees: [], localBranches: new Map(), orphanRefs: [] };
}

function assertLocalRepositoryIdentity(root: string, repository: RepositoryRef): void {
  const remote = runGit(root, ["remote", "get-url", "origin"]).trim();
  const observed = normalizeGithubRepository(remote);
  const expected = `${repository.owner}/${repository.repo}`.toLowerCase();
  if (observed !== expected) {
    throw new Error(`local checkout identity mismatch; expected ${expected}, observed ${observed || "unrecognized-origin"}`);
  }
}

function normalizeGithubRepository(remote: string): string {
  const value = remote.trim().replace(/\\/g, "/");
  const match = value.match(/^(?:https?:\/\/github\.com\/|ssh:\/\/git@github\.com\/|git@github\.com:)([^/\s]+)\/([^/\s]+?)(?:\.git)?\/?$/i);
  return match ? `${match[1]}/${match[2]}`.toLowerCase() : "";
}

function revalidateWorktreeRemoval(root: string, rawPath: string, entry: { target: string; head: string }): void {
  const resolvedRoot = resolve(root).toLowerCase();
  const resolvedWorktree = resolve(rawPath).toLowerCase();
  if (resolvedRoot === resolvedWorktree) {
    throw new Error(`clean worktree ${entry.target} is the explicitly supplied root checkout; apply aborted`);
  }
  const head = runGit(rawPath, ["rev-parse", "HEAD"]).trim();
  if (head !== entry.head) throw new Error(`clean worktree ${entry.target} head drifted immediately before removal; apply aborted`);
  const status = runGit(rawPath, ["status", "--porcelain=v2", "--untracked-files=all", "--ignore-submodules=none"]);
  if (status.split(/\r?\n/).some(Boolean)) {
    throw new Error(`clean worktree ${entry.target} became dirty immediately before removal; apply aborted`);
  }
  const branch = runGit(rawPath, ["symbolic-ref", "-q", "HEAD"]).trim();
  if (!branch.startsWith("refs/heads/")) {
    throw new Error(`clean worktree ${entry.target} became detached immediately before removal; apply aborted`);
  }
}

function gitObjectExists(cwd: string, object: string): boolean {
  const result = spawnGit(cwd, ["cat-file", "-e", `${object}^{commit}`]);
  return result.status === 0;
}

function gitIsAncestor(cwd: string, ancestor: string, descendant: string): boolean {
  const result = spawnGit(cwd, ["merge-base", "--is-ancestor", ancestor, descendant]);
  if (result.status === 0) return true;
  if (result.status === 1) return false;
  throw new Error("git ancestry evidence is unobservable");
}

function spawnGit(cwd: string, args: string[]) {
  return spawnSync("git", ["-C", resolve(cwd), ...args], {
    encoding: "utf8",
    windowsHide: true,
    timeout: 30_000,
    maxBuffer: 16 * 1024 * 1024
  });
}

function runGit(cwd: string, args: string[]): string {
  return execFileSync("git", ["-C", resolve(cwd), ...args], {
    encoding: "utf8",
    windowsHide: true,
    timeout: 30_000,
    maxBuffer: 16 * 1024 * 1024
  });
}

type NormalizedPull = ReturnType<typeof normalizePull>;

type AutoreviewObservation = Readonly<{
  state: "current" | "pending" | "failed" | "stale";
  version: string | null;
  timestamp: number;
  id: number;
}>;

function groupIssueComments(values: unknown[]): Map<number, Record<string, unknown>[]> {
  const grouped = new Map<number, Record<string, unknown>[]>();
  for (const [index, value] of values.entries()) {
    const comment = asRecord(value, `issue comment ${index}`);
    const issueUrl = requiredString(comment.issue_url, `issue comment ${index} issue_url`);
    const match = /\/issues\/(\d+)(?:$|[?#])/.exec(issueUrl);
    if (!match) throw new Error(`issue comment ${index} has an unobservable issue identity`);
    const number = Number(match[1]);
    if (!Number.isSafeInteger(number) || number < 1) throw new Error(`issue comment ${index} has an invalid issue identity`);
    grouped.set(number, [...(grouped.get(number) ?? []), comment]);
  }
  for (const comments of grouped.values()) {
    comments.sort((left, right) => timestamp(left.created_at) - timestamp(right.created_at) || commentId(left) - commentId(right));
  }
  return grouped;
}

function successorNumber(body: string): number | null {
  const values = [...body.matchAll(/^\s*(?:[-*]\s*)?(?:successor|superseded[- ]by|replaced[- ]by)\s*:?\s*#(\d+)\s*$/gim)]
    .map((match) => Number(match[1]))
    .filter((value) => Number.isSafeInteger(value) && value > 0);
  const unique = [...new Set(values)];
  return unique.length === 1 ? unique[0] : null;
}

function pullVersion(pull: Pick<NormalizedPull, "baseSha" | "headSha">): string {
  const version = `${pull.baseSha}:${pull.headSha}`;
  if (!PULL_REQUEST_VERSION.test(version)) throw new Error("pull request base/head identity is malformed or unobservable");
  return version;
}

function successorEvidence(
  pull: NormalizedPull,
  proof: "head-ancestry" | "tree-equivalence"
): NonNullable<CleanEvidence["pullRequests"][number]["successor"]> {
  return {
    number: pull.number,
    version: pullVersion(pull),
    base: pull.baseRef,
    baseSha: pull.baseSha,
    headRef: pull.headRef,
    head: pull.headSha,
    proof
  };
}

async function compareCommits(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  ancestor: string,
  descendant: string
): Promise<{ ancestor: boolean }> {
  const comparison = asRecord((await github.request("GET /repos/{owner}/{repo}/compare/{basehead}", {
    ...repository,
    basehead: `${ancestor}...${descendant}`
  })).data, `comparison ${ancestor}...${descendant}`);
  const mergeBase = asRecord(comparison.merge_base_commit, `comparison ${ancestor}...${descendant} merge base`);
  return { ancestor: mergeBase.sha === ancestor };
}

async function observePullAutoreview(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  pull: NormalizedPull,
  comments: readonly Record<string, unknown>[]
): Promise<CleanEvidence["pullRequests"][number]["autoreview"]> {
  const version = pullVersion(pull);
  const observations = commentAutoreviewObservations(comments);
  for (const check of await listCheckRuns(github, repository, pull.headSha)) {
    const value = asRecord(check, `check run for ${pull.headSha}`);
    const app = asRecord(value.app, `check run app for ${pull.headSha}`);
    if (value.name !== AUTOREVIEW_CHECK_NAME || app.id !== TRUSTED_ACTIONS_APP_ID) continue;
    const headSha = typeof value.head_sha === "string" ? value.head_sha : "";
    const status = typeof value.status === "string" ? value.status : "";
    const exactHeadPending = headSha === pull.headSha && status !== "completed";
    observations.push({
      // A check run binds the head SHA but not the base SHA. It can suppress a
      // duplicate while pending; completed evidence becomes exact only through
      // the versioned App result comment.
      state: exactHeadPending ? "pending" : "stale",
      version: exactHeadPending ? version : null,
      timestamp: timestamp(value.completed_at) || timestamp(value.started_at),
      id: typeof value.id === "number" && Number.isSafeInteger(value.id) ? value.id : 0
    });
  }
  return resolveAutoreviewState(observations, version);
}

function observeIssueAutoreview(
  comments: readonly Record<string, unknown>[],
  version: string
): CleanEvidence["issues"][number]["autoreview"] {
  return resolveAutoreviewState(commentAutoreviewObservations(comments), version);
}

function commentAutoreviewObservations(comments: readonly Record<string, unknown>[]): AutoreviewObservation[] {
  const observations: AutoreviewObservation[] = [];
  for (const comment of comments) {
    if (!isTrustedDarkFactoryComment(comment) || typeof comment.body !== "string") continue;
    const body = comment.body;
    const time = commentTimestamp(comment);
    const id = commentId(comment);
    const cleanMarker = parseCleanAutoreviewMarker(body);
    if (cleanMarker) {
      observations.push({
        state: cleanMarker.status === "pending" && time > 0 && Date.now() - time <= AUTOREVIEW_PENDING_MAX_AGE_MS
          ? "pending"
          : cleanMarker.status === "failed"
            ? "failed"
            : "stale",
        version: cleanMarker.version,
        timestamp: time,
        id
      });
    }
    if (!body.startsWith(AUTOREVIEW_RESULT_MARKER)) continue;
    const version = autoreviewVersionFromBody(body);
    observations.push({
      state: version === null
        ? "stale"
        : /\*\*Verdict:\*\* (?:Clean high confirmation|Auditable owner override)/.test(body)
          ? "current"
          : "failed",
      version,
      timestamp: time,
      id
    });
  }
  return observations;
}

function resolveAutoreviewState(
  observations: readonly AutoreviewObservation[],
  version: string
): "current" | "pending" | "missing" | "failed" | "stale" {
  const exact = observations
    .filter((observation) => observation.version === version)
    .sort((left, right) => right.timestamp - left.timestamp || right.id - left.id);
  if (exact.length > 0) return exact[0]!.state;
  return observations.length > 0 ? "stale" : "missing";
}

function autoreviewVersionFromBody(body: string): string | null {
  const escaped = AUTOREVIEW_TARGET_VERSION_MARKER_PREFIX.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`^${escaped}([0-9a-f]{64}|[0-9a-f]{40}:[0-9a-f]{40}) -->$`, "m").exec(body);
  return match ? match[1] : null;
}

function parseCleanAutoreviewMarker(body: string): {
  kind: "issue" | "pull-request";
  number: number;
  version: string;
  status: "pending" | "failed";
} | null {
  if (!body.startsWith(CLEAN_AUTOREVIEW_MARKER)) return null;
  const firstLine = body.split(/\r?\n/, 1)[0] ?? "";
  const match = /^<!-- darkfactory:clean-autoreview schema=1 kind=(issue|pull-request) number=(\d+) version=([0-9a-f]{64}|[0-9a-f]{40}:[0-9a-f]{40}) status=(pending|failed) -->$/.exec(firstLine);
  if (!match) throw new Error("Trusted clean Autoreview marker is malformed");
  const number = Number(match[2]);
  if (!Number.isSafeInteger(number) || number < 1) return null;
  return { kind: match[1] as "issue" | "pull-request", number, version: match[3], status: match[4] as "pending" | "failed" };
}

function renderCleanAutoreviewComment(
  entry: CleanPlanEntry,
  number: number,
  status: "pending" | "failed",
  detail: string
): string {
  if (!entry.version) throw new Error(`clean Autoreview target ${entry.target} lacks an exact version`);
  validateAutoreviewVersion(entry.kind, entry.version);
  return [
    `<!-- darkfactory:clean-autoreview schema=1 kind=${entry.kind} number=${number} version=${entry.version} status=${status} -->`,
    "## DarkFactory clean Autoreview dispatch",
    "",
    detail,
    "",
    `Target: \`${entry.target}\` at exact version \`${entry.version}\`.`
  ].join("\n");
}

function firstPendingAutoreviewComment(
  comments: readonly Record<string, unknown>[],
  kind: CleanPlanEntry["kind"],
  number: number,
  version: string
): number | null {
  const ids = comments.flatMap((comment) => {
    if (!isTrustedDarkFactoryComment(comment) || typeof comment.body !== "string") return [];
    const marker = parseCleanAutoreviewMarker(comment.body);
    if (!marker || marker.status !== "pending" || marker.kind !== kind || marker.number !== number || marker.version !== version) return [];
    const observedAt = commentTimestamp(comment);
    if (observedAt <= 0 || Date.now() - observedAt > AUTOREVIEW_PENDING_MAX_AGE_MS) return [];
    const id = commentId(comment);
    return id > 0 ? [id] : [];
  });
  return ids.length > 0 ? Math.min(...ids) : null;
}

async function listCheckRuns(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  head: string
): Promise<unknown[]> {
  const runs: unknown[] = [];
  for (let page = 1; page <= 20; page += 1) {
    const payload = asRecord((await github.request("GET /repos/{owner}/{repo}/commits/{ref}/check-runs", {
      ...repository,
      ref: head,
      filter: "all",
      per_page: 100,
      page
    })).data, `check runs for ${head}`);
    if (!Array.isArray(payload.check_runs)) throw new Error(`check runs for ${head} are malformed or unobservable`);
    runs.push(...payload.check_runs);
    if (payload.check_runs.length < 100) return runs;
  }
  throw new Error(`check runs for ${head} exceeded the bounded 2000-record evidence window`);
}

async function readExactAutoreviewTarget(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  entry: CleanPlanEntry,
  number: number
): Promise<{ autoreview: "current" | "pending" | "missing" | "failed" | "stale"; comments: Record<string, unknown>[] }> {
  if (!entry.version) throw new Error(`clean target ${entry.target} lacks an exact version`);
  if (entry.kind === "issue") {
    const first = asRecord((await github.request("GET /repos/{owner}/{repo}/issues/{issue_number}", {
      ...repository,
      issue_number: number
    })).data, `issue ${entry.target}`);
    const firstComments = (await listPages(github, "GET /repos/{owner}/{repo}/issues/{issue_number}/comments", {
      ...repository,
      issue_number: number
    })).map((comment, index) => asRecord(comment, `issue ${entry.target} comment ${index}`));
    const second = asRecord((await github.request("GET /repos/{owner}/{repo}/issues/{issue_number}", {
      ...repository,
      issue_number: number
    })).data, `issue ${entry.target}`);
    const secondComments = (await listPages(github, "GET /repos/{owner}/{repo}/issues/{issue_number}/comments", {
      ...repository,
      issue_number: number
    })).map((comment, index) => asRecord(comment, `issue ${entry.target} confirmation comment ${index}`));
    for (const [issue, comments] of [[first, firstComments], [second, secondComments]] as const) {
      if (issue.pull_request !== undefined || issue.state !== "open") throw new Error(`issue ${entry.target} is no longer an open issue`);
      const effective = resolveEffectiveIssueContent(issue, comments);
      if (effective.version !== entry.version) throw new Error(`issue ${entry.target} drifted from exact version ${entry.version}`);
    }
    return { autoreview: observeIssueAutoreview(secondComments, entry.version), comments: secondComments };
  }
  if (entry.kind !== "pull-request") throw new Error(`clean target ${entry.target} cannot be Autoreviewed`);
  const first = normalizePull((await github.request("GET /repos/{owner}/{repo}/pulls/{pull_number}", {
    ...repository,
    pull_number: number
  })).data, number, repository);
  assertPullIdentity(first, entry, true);
  const comments = (await listPages(github, "GET /repos/{owner}/{repo}/issues/{issue_number}/comments", {
    ...repository,
    issue_number: number
  })).map((comment, index) => asRecord(comment, `pull request ${entry.target} comment ${index}`));
  const autoreview = await observePullAutoreview(github, repository, first, comments);
  const second = normalizePull((await github.request("GET /repos/{owner}/{repo}/pulls/{pull_number}", {
    ...repository,
    pull_number: number
  })).data, number, repository);
  assertPullIdentity(second, entry, true);
  return { autoreview, comments };
}

async function revalidatePullClosure(
  github: OperatorGitHubRequester,
  repository: RepositoryRef,
  number: number,
  entry: CleanPlanEntry
): Promise<void> {
  if (!entry.successor) throw new Error(`clean pull-request closure ${entry.target} lacks a successor`);
  const [sourceValue, successorValue] = await Promise.all([
    github.request("GET /repos/{owner}/{repo}/pulls/{pull_number}", { ...repository, pull_number: number }),
    github.request("GET /repos/{owner}/{repo}/pulls/{pull_number}", { ...repository, pull_number: entry.successor.number })
  ]);
  const source = normalizePull(sourceValue.data, number, repository);
  const successor = normalizePull(successorValue.data, entry.successor.number, repository);
  assertPullIdentity(source, entry, true);
  assertSuccessorIdentity(successor, entry.successor);
  if (successorNumber(source.body) !== entry.successor.number) {
    throw new Error(`pull request ${entry.target} successor pointer drifted`);
  }
  if (!source.sameRepository || !successor.sameRepository || source.baseRef !== successor.baseRef) {
    throw new Error(`pull request ${entry.target} successor is not a same-repository, same-base preservation lane`);
  }
  if (successor.state !== "open" && !successor.merged) {
    throw new Error(`pull request ${entry.target} successor is neither open nor merged`);
  }
  if (entry.successor.proof === "head-ancestry") {
    const comparison = await compareCommits(github, repository, source.headSha, successor.headSha);
    if (!comparison.ancestor) throw new Error(`pull request ${entry.target} successor ancestry proof drifted`);
  } else {
    const [sourceTree, successorTree] = await Promise.all([
      commitTree(github, repository, source.headSha),
      commitTree(github, repository, successor.headSha)
    ]);
    if (sourceTree !== successorTree) throw new Error(`pull request ${entry.target} successor tree proof drifted`);
  }
}

function assertPullIdentity(pull: NormalizedPull, entry: CleanPlanEntry, requireOpen: boolean): void {
  if (!entry.version || !entry.base || !entry.baseSha || !entry.headRef) throw new Error(`pull request ${entry.target} plan identity is incomplete`);
  if (pull.number !== cleanTargetNumber(entry)
    || pullVersion(pull) !== entry.version
    || pull.baseRef !== entry.base
    || pull.baseSha !== entry.baseSha
    || pull.headRef !== entry.headRef
    || pull.headSha !== entry.head
    || !pull.sameRepository) {
    throw new Error(`pull request ${entry.target} drifted from its exact base/head identity`);
  }
  if (requireOpen && pull.state !== "open") throw new Error(`pull request ${entry.target} is no longer open`);
}

function assertSuccessorIdentity(
  pull: NormalizedPull,
  expected: NonNullable<CleanPlanEntry["successor"]>
): void {
  if (pull.number !== expected.number
    || pullVersion(pull) !== expected.version
    || pull.baseRef !== expected.base
    || pull.baseSha !== expected.baseSha
    || pull.headRef !== expected.headRef
    || pull.headSha !== expected.head
    || !pull.sameRepository) {
    throw new Error(`successor pull request #${expected.number} drifted from its exact base/head identity`);
  }
}

function renderPullClosureComment(entry: CleanPlanEntry): string {
  if (!entry.version || !entry.successor) throw new Error(`clean pull-request closure ${entry.target} lacks exact evidence`);
  return [
    closureMarker(entry),
    "## DarkFactory clean closure",
    "",
    `This ${entry.classification} pull request is closed only because PR #${entry.successor.number} independently preserves its exact head through ${entry.successor.proof}.`,
    "",
    `Source version: \`${entry.version}\``,
    `Successor version: \`${entry.successor.version}\``
  ].join("\n");
}

function closureMarker(entry: CleanPlanEntry): string {
  if (!entry.version || !entry.successor) throw new Error(`clean pull-request closure ${entry.target} lacks exact evidence`);
  return `${CLEAN_CLOSURE_MARKER} schema=1 source=${entry.version} successor=${entry.successor.number} successor-version=${entry.successor.version} proof=${entry.successor.proof} -->`;
}

function isTrustedClosureComment(value: unknown, entry: CleanPlanEntry): boolean {
  return isTrustedDarkFactoryComment(value)
    && typeof (value as Record<string, unknown>).body === "string"
    && ((value as Record<string, unknown>).body as string).split(/\r?\n/, 1)[0] === closureMarker(entry);
}

function cleanTargetNumber(entry: CleanPlanEntry): number {
  const match = /^#(\d+)$/.exec(entry.target);
  const number = match ? Number(match[1]) : 0;
  if (!Number.isSafeInteger(number) || number < 1) throw new Error(`clean target ${entry.target} has an invalid identity`);
  return number;
}

function validateAutoreviewVersion(kind: CleanPlanEntry["kind"], version: string): void {
  const valid = kind === "issue" ? ISSUE_VERSION.test(version) : kind === "pull-request" && PULL_REQUEST_VERSION.test(version);
  if (!valid) throw new Error(`clean ${kind} Autoreview version is malformed`);
}

function commentTimestamp(comment: Record<string, unknown>): number {
  return timestamp(comment.updated_at) || timestamp(comment.created_at);
}

function commentId(comment: Record<string, unknown>): number {
  return typeof comment.id === "number" && Number.isSafeInteger(comment.id) ? comment.id : 0;
}

function timestamp(value: unknown): number {
  if (typeof value !== "string") return 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function classifyPullRequest(findingIds: string[], body = ""): PullRequestClassification {
  if (findingIds.some((id) => /(?:superseded|obsolete)/.test(id))) return "superseded";
  if (findingIds.some((id) => /abandoned/.test(id))) return "abandoned";
  if (/^\s*(?:[-*]\s*)?status\s*:\s*superseded\s*$/im.test(body)) return "superseded";
  if (/^\s*(?:[-*]\s*)?status\s*:\s*abandoned\s*$/im.test(body)) return "abandoned";
  if (/^\s*(?:[-*]\s*)?superseded[- ]by\s*:?\s*#\d+\s*$/im.test(body)) return "superseded";
  if (findingIds.some((id) => /(?:-red|-not-mergeable|-checks-(?:missing|stuck|unobservable))$/.test(id))) return "red";
  if (findingIds.some((id) => /-stale$/.test(id))) return "stale";
  return "active";
}

function findingTouchesPull(finding: DoctorFinding, number: number): boolean {
  return new RegExp(`(?:^|-)pr-${number}(?:-|$)`).test(finding.id)
    || (finding.evidence || []).some((item) => new RegExp(`/pull/${number}(?:$|[?#])`).test(item.url || ""));
}

function findingTouchesIssue(finding: DoctorFinding, number: number): boolean {
  return new RegExp(`(?:^|-)issue-${number}(?:-|$)`).test(finding.id)
    || (finding.evidence || []).some((item) => new RegExp(`/issues/${number}(?:$|[?#])`).test(item.url || ""))
    || new RegExp(`(?:^|\\s)#${number}(?!\\d)`).test(finding.message);
}

async function listPages(github: OperatorGitHubRequester, route: string, parameters: Record<string, unknown>): Promise<unknown[]> {
  const values: unknown[] = [];
  for (let page = 1; page <= 20; page += 1) {
    const response = await github.request(route, { ...parameters, per_page: 100, page });
    if (!Array.isArray(response.data)) throw new Error(`${route} returned malformed pagination data`);
    values.push(...response.data);
    if (response.data.length < 100) return values;
  }
  throw new Error(`${route} exceeded the bounded 2000-record evidence window`);
}

async function commitTree(github: OperatorGitHubRequester, repository: RepositoryRef, sha: string): Promise<string> {
  const commit = asRecord((await github.request("GET /repos/{owner}/{repo}/git/commits/{commit_sha}", {
    ...repository,
    commit_sha: sha
  })).data, `commit ${sha}`);
  return requiredString(asRecord(commit.tree, `commit ${sha} tree`).sha, `commit ${sha} tree sha`);
}

async function isHeadContainedBy(github: OperatorGitHubRequester, repository: RepositoryRef, branchHead: string, policyBranch: string): Promise<boolean> {
  const comparison = asRecord((await github.request("GET /repos/{owner}/{repo}/compare/{basehead}", {
    ...repository,
    basehead: `${branchHead}...${policyBranch}`
  })).data, `comparison ${branchHead}...${policyBranch}`);
  const mergeBase = asRecord(comparison.merge_base_commit, "comparison merge base");
  return mergeBase.sha === branchHead;
}

async function contentFingerprint(github: OperatorGitHubRequester, repository: RepositoryRef, path: string, ref: string): Promise<string> {
  try {
    const content = asRecord((await github.request("GET /repos/{owner}/{repo}/contents/{path}", { ...repository, path, ref })).data, `${path} content`);
    return typeof content.sha === "string" ? content.sha : stableHash(content);
  } catch (error) {
    if (isStatus(error, 404)) return "missing";
    throw error;
  }
}

function normalizePull(value: unknown, index: number, repository: RepositoryRef): {
  number: number;
  state: string;
  merged: boolean;
  body: string;
  headRef: string;
  headSha: string;
  baseRef: string;
  baseSha: string;
  sameRepository: boolean;
  updatedAt: string;
} {
  const pull = asRecord(value, `pull request ${index}`);
  const head = asRecord(pull.head, `pull request ${index} head`);
  const base = asRecord(pull.base, `pull request ${index} base`);
  const headRepo = head.repo ? asRecord(head.repo, `pull request ${index} head repo`) : {};
  const owner = headRepo.owner ? asRecord(headRepo.owner, `pull request ${index} head owner`) : {};
  return {
    number: requiredNumber(pull.number, `pull request ${index} number`),
    state: requiredString(pull.state, `pull request ${index} state`),
    merged: Boolean(pull.merged_at),
    body: typeof pull.body === "string" ? pull.body : "",
    headRef: requiredString(head.ref, `pull request ${index} head ref`),
    headSha: requiredString(head.sha, `pull request ${index} head sha`),
    baseRef: requiredString(base.ref, `pull request ${index} base ref`),
    baseSha: requiredString(base.sha, `pull request ${index} base sha`),
    sameRepository: String(owner.login || "").toLowerCase() === repository.owner.toLowerCase()
      && String(headRepo.name || "").toLowerCase() === repository.repo.toLowerCase(),
    updatedAt: requiredString(pull.updated_at, `pull request ${index} updated_at`)
  };
}

function asRecord(value: unknown, label: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`${label} is malformed or unobservable`);
  return value as Record<string, unknown>;
}

function requiredString(value: unknown, label: string): string {
  if (typeof value !== "string" || !value) throw new Error(`${label} is malformed or unobservable`);
  return value;
}

function requiredNumber(value: unknown, label: string): number {
  if (!Number.isInteger(value)) throw new Error(`${label} is malformed or unobservable`);
  return value as number;
}

function isStatus(error: unknown, status: number): boolean {
  return Boolean(error && typeof error === "object" && "status" in error && (error as { status?: number }).status === status);
}
