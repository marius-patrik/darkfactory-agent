import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
  DARK_FACTORY_DATA_REPO,
  createGithubClient,
  listActiveManagedRepos,
  normalizeWorkerPullRequestActor,
  parseRepo,
  repoName,
  writeRunLedger
} from "./df-lib.mjs";
import {
  AUTOREVIEW_RESULT_MARKER,
  AUTOREVIEW_TARGET_VERSION_MARKER_PREFIX,
  resolveEffectiveIssueContent
} from "../../src/issue-spec.ts";

const CONTROL_REPOSITORY = { owner: "marius-patrik", repo: "DarkFactory" };
const AUTOREVIEW_WORKFLOW = "darkfactory-autoreview.yml";
const TRUSTED_GATE_APP_ID = 15368;
const SHA = /^[0-9a-f]{40}$/;
const REPOSITORY = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const PENDING_MAX_AGE_MS = 3 * 60 * 60 * 1_000;
const CONTROL_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const DATA_POLICY_PATH = path.join(CONTROL_ROOT, ".darkfactory", "data-repository-policy.json");
const PENDING_MARKER = "<!-- darkfactory:clean-autoreview";

let gh;
let ledgerGh;
let runtimeOptions = {};

export function configureAutoreviewRecoveryRuntime(options) {
  gh = options.gh;
  ledgerGh = options.ledgerGh || options.gh;
  runtimeOptions = options;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    const secrets = [process.env.DARK_FACTORY_TOKEN, process.env.DF_LEDGER_TOKEN].filter(Boolean);
    let message = String(error?.stack || error?.message || error);
    for (const secret of secrets) message = message.split(secret).join("***");
    console.error(message);
    process.exitCode = 1;
  });
}

async function main() {
  const token = requiredEnv("DARK_FACTORY_TOKEN");
  const ledgerToken = process.env.DF_LEDGER_TOKEN?.trim() || token;
  configureAutoreviewRecoveryRuntime({
    gh: createGithubClient(token, "darkfactory-autoreview-recovery"),
    ledgerGh: createGithubClient(ledgerToken, "darkfactory-autoreview-recovery-ledger"),
    controlRepo: CONTROL_REPOSITORY,
    controlRevision: requiredExactSha("DF_CONTROL_REVISION"),
    root: CONTROL_ROOT,
    now: Date.now()
  });
  const repository = process.env.DF_TARGET_REPO?.trim() || "";
  if (process.env.DF_AUTOREVIEW_RECOVERY_MODE === "list") {
    console.log(JSON.stringify({ schemaVersion: 1, mode: "list", repositories: await listRecoveryRepositories({ repository }) }, null, 2));
    return;
  }
  const result = await recoverAutoreviews({
    kind: process.env.DF_TARGET_KIND?.trim() || "all",
    repository,
    maxDispatches: boundedInteger(process.env.DF_MAX_RECOVERY_DISPATCHES, 4, 1, 20),
    trigger: process.env.DF_TRIGGER?.trim() || "manual"
  });
  console.log(JSON.stringify(result, null, 2));
}

export async function listRecoveryRepositories({ repository = "" } = {}) {
  assertRuntime();
  const controlRepo = runtimeOptions.controlRepo || CONTROL_REPOSITORY;
  const active = runtimeOptions.activeRepositories || await listActiveManagedRepos(gh, controlRepo, {
    ...(runtimeOptions.registry ? { registry: runtimeOptions.registry } : {}),
    ...(runtimeOptions.installationRepositories ? { repositories: runtimeOptions.installationRepositories } : {}),
    root: runtimeOptions.root || CONTROL_ROOT,
    warn: runtimeOptions.warn || (() => {})
  });
  const dataRepositories = new Set((runtimeOptions.dataRepositories || loadDataRepositoryNames()).map((value) => value.toLowerCase()));
  const controlMetadata = runtimeOptions.controlMetadata || await gh.request("GET", `/repos/${repoName(controlRepo)}`);
  if (controlMetadata?.archived === true || controlMetadata?.disabled === true) {
    throw new Error("DarkFactory control repository is archived or disabled");
  }
  const allowed = new Map();
  for (const candidate of [controlRepo, ...active]) {
    const identity = repoName(candidate);
    if (!dataRepositories.has(identity.toLowerCase())) allowed.set(identity.toLowerCase(), candidate);
  }
  if (repository) {
    if (!REPOSITORY.test(repository)) throw new Error("Recovery target must be an exact owner/repository identity");
    const exact = allowed.get(repository.toLowerCase());
    if (!exact) throw new Error(`Recovery target ${repository} is not an active code repository`);
    return [{ repository: repoName(exact), name: exact.repo }];
  }
  return [...allowed.values()]
    .sort((left, right) => repoName(left).localeCompare(repoName(right)))
    .map((candidate) => ({ repository: repoName(candidate), name: candidate.repo }));
}

export async function recoverAutoreviews({ kind = "all", repository = "", maxDispatches = 4, trigger = "manual" } = {}) {
  assertRuntime();
  if (!["all", "pull_request", "issue"].includes(kind)) throw new Error(`Unknown Autoreview recovery kind: ${kind}`);
  if (!Number.isSafeInteger(maxDispatches) || maxDispatches < 1 || maxDispatches > 20) throw new Error("Autoreview recovery dispatch bound is invalid");
  const controlRevision = String(runtimeOptions.controlRevision || "");
  if (!SHA.test(controlRevision)) throw new Error("Autoreview recovery requires an exact trusted control revision");
  const repositories = await listRecoveryRepositories({ repository });
  const candidates = [];
  for (const target of repositories) {
    const parsed = parseRepo(target.repository);
    if (kind === "all" || kind === "pull_request") candidates.push(...await collectPullCandidates(parsed));
    if (kind === "all" || kind === "issue") candidates.push(...await collectIssueCandidates(parsed));
  }
  candidates.sort((left, right) => left.repository.localeCompare(right.repository)
    || left.kind.localeCompare(right.kind)
    || left.number - right.number);
  const selected = candidates.slice(0, maxDispatches);
  await recordLedger("autoreview-recovery-scan", repoName(runtimeOptions.controlRepo || CONTROL_REPOSITORY), {
    status: "admitted",
    trigger,
    kind,
    control_revision: controlRevision,
    repositories: repositories.map((entry) => entry.repository),
    candidates: candidates.map(publicCandidate),
    selected: selected.map(publicCandidate),
    deferred: Math.max(0, candidates.length - selected.length)
  });
  const dispatched = [];
  for (const candidate of selected) dispatched.push(await dispatchCandidate(candidate, { trigger, controlRevision }));
  const result = {
    schemaVersion: 1,
    status: "complete",
    kind,
    controlRevision,
    repositories: repositories.map((entry) => entry.repository),
    candidates: candidates.length,
    dispatched,
    deferred: Math.max(0, candidates.length - selected.length)
  };
  await recordLedger("autoreview-recovery-completion", repoName(runtimeOptions.controlRepo || CONTROL_REPOSITORY), result);
  return result;
}

async function collectPullCandidates(repository) {
  const candidates = [];
  for (const pull of await listAll(`/repos/${repoName(repository)}/pulls?state=open&per_page=100`)) {
    if (!isRecord(pull) || pull.draft === true || !isRecord(pull.base) || !isRecord(pull.head) || !isRecord(pull.head.repo)) continue;
    if (String(pull.head.repo.full_name || "").toLowerCase() !== repoName(repository).toLowerCase()) continue;
    const baseSha = String(pull.base.sha || "");
    const headSha = String(pull.head.sha || "");
    if (!SHA.test(baseSha) || !SHA.test(headSha) || !Number.isSafeInteger(pull.number) || pull.number < 1) continue;
    const version = `${baseSha}:${headSha}`;
    const comments = await listAll(`/repos/${repoName(repository)}/issues/${pull.number}/comments?per_page=100`);
    if (hasExactCompletion(comments, version) || hasFreshPending(comments, "pull-request", pull.number, version)) continue;
    const checks = await gh.request("GET", `/repos/${repoName(repository)}/commits/${headSha}/check-runs?per_page=100`);
    if (Array.isArray(checks?.check_runs) && checks.check_runs.some((check) => check?.name === "DarkFactory Autoreview"
      && check?.app?.id === TRUSTED_GATE_APP_ID && check?.head_sha === headSha && check?.status !== "completed")) continue;
    candidates.push({ kind: "pull_request", repository: repoName(repository), number: pull.number, version });
  }
  return candidates;
}

async function collectIssueCandidates(repository) {
  const candidates = [];
  for (const issue of await listAll(`/repos/${repoName(repository)}/issues?state=open&per_page=100`)) {
    if (!isRecord(issue) || issue.pull_request || !Number.isSafeInteger(issue.number) || issue.number < 1) continue;
    const labels = new Set((Array.isArray(issue.labels) ? issue.labels : []).map((label) => typeof label === "string" ? label : label?.name).filter(Boolean));
    if (labels.has("df:no-dispatch") || labels.has("df:running")) continue;
    const comments = await listAll(`/repos/${repoName(repository)}/issues/${issue.number}/comments?per_page=100`);
    const version = resolveEffectiveIssueContent(issue, comments).version;
    if (!/^[0-9a-f]{64}$/.test(version) || hasExactCompletion(comments, version) || hasFreshPending(comments, "issue", issue.number, version)) continue;
    candidates.push({ kind: "issue", repository: repoName(repository), number: issue.number, version });
  }
  return candidates;
}

async function dispatchCandidate(candidate, context) {
  await assertCandidateCurrent(candidate);
  await recordLedger("autoreview-recovery-admission", candidate.repository, {
    status: "admitted",
    ...publicCandidate(candidate),
    trigger: context.trigger,
    control_revision: context.controlRevision,
    workflow: AUTOREVIEW_WORKFLOW
  });
  const marker = renderPendingMarker(candidate, "pending", "Exact-version scheduled recovery admitted from trusted DarkFactory main.");
  const comment = await gh.request("POST", `/repos/${candidate.repository}/issues/${candidate.number}/comments`, { body: marker });
  try {
    if (!Number.isSafeInteger(comment?.id)) throw new Error(`Recovery pending marker for ${candidate.repository}#${candidate.number} has no exact comment identity`);
    await assertCandidateCurrent(candidate, comment.id);
    await gh.request("POST", `/repos/${candidate.repository}/actions/workflows/${AUTOREVIEW_WORKFLOW}/dispatches`, {
      ref: "main",
      inputs: { target_kind: candidate.kind, target_number: String(candidate.number), target_version: candidate.version }
    });
    const result = { ...publicCandidate(candidate), status: "dispatched", workflow: AUTOREVIEW_WORKFLOW, pendingComment: comment?.html_url || null };
    await recordLedger("autoreview-recovery-dispatch", candidate.repository, result);
    return result;
  } catch (error) {
    if (Number.isSafeInteger(comment?.id)) {
      await gh.request("PATCH", `/repos/${candidate.repository}/issues/comments/${comment.id}`, {
        body: renderPendingMarker(candidate, "failed", "Recovery dispatch failed closed; no target content was executed or mutated.")
      }).catch(() => {});
    }
    await recordLedger("autoreview-recovery-dispatch", candidate.repository, {
      ...publicCandidate(candidate), status: "failed", workflow: AUTOREVIEW_WORKFLOW, reason: String(error?.message || error)
    });
    throw error;
  }
}

async function assertCandidateCurrent(candidate, admittedCommentId = null) {
  const repository = parseRepo(candidate.repository);
  if (candidate.kind === "pull_request") {
    const pull = await gh.request("GET", `/repos/${candidate.repository}/pulls/${candidate.number}`);
    if (pull?.state !== "open" || pull?.draft === true || pull?.head?.repo?.full_name?.toLowerCase() !== candidate.repository.toLowerCase()
      || `${pull?.base?.sha || ""}:${pull?.head?.sha || ""}` !== candidate.version) {
      throw new Error(`Pull request ${candidate.repository}#${candidate.number} changed before recovery dispatch`);
    }
    const comments = await listAll(`/repos/${repoName(repository)}/issues/${candidate.number}/comments?per_page=100`);
    assertNoConcurrentResult(candidate, comments, admittedCommentId);
    const checks = await gh.request("GET", `/repos/${repoName(repository)}/commits/${pull.head.sha}/check-runs?per_page=100`);
    if (Array.isArray(checks?.check_runs) && checks.check_runs.some((check) => check?.name === "DarkFactory Autoreview"
      && check?.app?.id === TRUSTED_GATE_APP_ID && check?.head_sha === pull.head.sha && check?.status !== "completed")) {
      throw new Error(`Pull request ${candidate.repository}#${candidate.number} acquired a trusted pending Autoreview check before recovery dispatch`);
    }
    return;
  }
  const issue = await gh.request("GET", `/repos/${candidate.repository}/issues/${candidate.number}`);
  const comments = await listAll(`/repos/${repoName(repository)}/issues/${candidate.number}/comments?per_page=100`);
  if (issue?.state !== "open" || issue?.pull_request) {
    throw new Error(`Issue ${candidate.repository}#${candidate.number} is no longer an open issue`);
  }
  if (resolveEffectiveIssueContent(issue, comments).version !== candidate.version) {
    throw new Error(`Issue ${candidate.repository}#${candidate.number} changed before recovery dispatch`);
  }
  assertNoConcurrentResult(candidate, comments, admittedCommentId);
}

function assertNoConcurrentResult(candidate, comments, admittedCommentId) {
  if (hasExactCompletion(comments, candidate.version)) {
    throw new Error(`${candidate.repository}#${candidate.number} acquired an exact Autoreview result before recovery dispatch`);
  }
  const kind = candidate.kind === "pull_request" ? "pull-request" : "issue";
  const otherComments = admittedCommentId === null
    ? comments
    : comments.filter((comment) => comment?.id !== admittedCommentId);
  if (hasFreshPending(otherComments, kind, candidate.number, candidate.version)) {
    throw new Error(`${candidate.repository}#${candidate.number} acquired another fresh recovery admission before dispatch`);
  }
}

function hasExactCompletion(comments, version) {
  const marker = `${AUTOREVIEW_TARGET_VERSION_MARKER_PREFIX}${version} -->`;
  return comments.some((comment) => normalizeWorkerPullRequestActor(comment?.user) !== null
    && typeof comment?.body === "string"
    && comment.body.startsWith(AUTOREVIEW_RESULT_MARKER)
    && comment.body.includes(marker));
}

function hasFreshPending(comments, kind, number, version) {
  const expected = `<!-- darkfactory:clean-autoreview schema=1 kind=${kind} number=${number} version=${version} status=pending -->`;
  const now = Number(runtimeOptions.now || Date.now());
  return comments.some((comment) => normalizeWorkerPullRequestActor(comment?.user) !== null
    && typeof comment?.body === "string"
    && comment.body.startsWith(expected)
    && Number.isFinite(Date.parse(comment.created_at || ""))
    && now - Date.parse(comment.created_at) >= -300_000
    && now - Date.parse(comment.created_at) <= PENDING_MAX_AGE_MS);
}

function renderPendingMarker(candidate, status, detail) {
  return [
    `${PENDING_MARKER} schema=1 kind=${candidate.kind === "pull_request" ? "pull-request" : "issue"} number=${candidate.number} version=${candidate.version} status=${status} -->`,
    detail
  ].join("\n");
}

function publicCandidate(candidate) {
  return { kind: candidate.kind, repository: candidate.repository, number: candidate.number, version: candidate.version };
}

async function recordLedger(kind, target, payload) {
  if (typeof runtimeOptions.writeLedger === "function") return await runtimeOptions.writeLedger(kind, target, payload);
  return await writeRunLedger(ledgerGh, DARK_FACTORY_DATA_REPO, kind, target, payload);
}

async function listAll(requestPath) {
  const output = [];
  for (let page = 1; page <= 100; page += 1) {
    const separator = requestPath.includes("?") ? "&" : "?";
    const pagePath = /(?:^|[?&])page=/.test(requestPath) ? requestPath : `${requestPath}${separator}page=${page}`;
    const items = await gh.request("GET", pagePath);
    if (!Array.isArray(items)) throw new Error(`GitHub returned a malformed paginated response for ${requestPath}`);
    output.push(...items);
    if (items.length < 100) return output;
  }
  throw new Error(`GitHub pagination exceeded its bounded inventory for ${requestPath}`);
}

function loadDataRepositoryNames() {
  const value = JSON.parse(readFileSync(DATA_POLICY_PATH, "utf8"));
  if (!isRecord(value) || !Array.isArray(value.repositories)) throw new Error("Data repository policy is malformed");
  return value.repositories.map((entry) => {
    if (!isRecord(entry) || typeof entry.repository !== "string" || !REPOSITORY.test(entry.repository)) throw new Error("Data repository policy contains a malformed repository");
    return entry.repository;
  });
}

function requiredEnv(name) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`Missing required environment variable ${name}`);
  return value;
}

function requiredExactSha(name) {
  const value = requiredEnv(name);
  if (!SHA.test(value)) throw new Error(`${name} must be an exact lowercase commit SHA`);
  return value;
}

function boundedInteger(value, fallback, minimum, maximum) {
  if (!value?.trim()) return fallback;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < minimum || parsed > maximum) throw new Error(`Bounded integer must be between ${minimum} and ${maximum}`);
  return parsed;
}

function assertRuntime() {
  if (!gh || !ledgerGh) throw new Error("Autoreview recovery runtime is not configured");
}

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
