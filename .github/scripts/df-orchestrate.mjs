import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  DEFAULT_DATA_REPO,
  WORK_LABELS,
  assertAllowedRepo,
  createGithubClient,
  ensureLabels,
  findOpenWorkerPullRequestForIssue,
  getOptionalFileContent,
  getRepository,
  listActiveManagedRepos,
  parseRepo,
  preflightMergePolicy,
  repoName,
  requiredEnv,
  warnReadOnlyRepository,
  writeRunLedger
} from "./df-lib.mjs";

const CONTROL_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const DASHBOARD_MARKER = "<!-- dark-factory:l0-dashboard -->";
const OWNER_ESCALATION_MARKER = "<!-- dark-factory:l0-ask-owner -->";
const DEFAULT_GLOBAL_WORKER_CAP = 6;
const DEFAULT_REPO_WORKER_CAP = 2;
const DEFAULT_STREAM_WORKER_CAP = 1;

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error.stack || error.message || String(error));
    process.exitCode = 1;
  });
}

async function main() {
  const token = requiredEnv("DARK_FACTORY_TOKEN");
  const controlRepo = parseRepo(requiredEnv("DF_CONTROL_REPO"));
  const dataRepo = process.env.DF_DATA_REPO ?? DEFAULT_DATA_REPO;
  const trigger = process.env.DF_TRIGGER ?? "unknown";
  const caps = {
    global: envInt("DF_L0_GLOBAL_WORKER_CAP", DEFAULT_GLOBAL_WORKER_CAP),
    perRepo: envInt("DF_L0_REPO_WORKER_CAP", DEFAULT_REPO_WORKER_CAP),
    perStream: envInt("DF_L0_STREAM_WORKER_CAP", DEFAULT_STREAM_WORKER_CAP)
  };
  const gh = createGithubClient(token, "darkfactory-orchestrate");

  await orchestrate({ gh, controlRepo, dataRepo, trigger, root: CONTROL_ROOT, caps });
}

export async function orchestrate(options) {
  const {
    gh,
    controlRepo,
    dataRepo = DEFAULT_DATA_REPO,
    trigger = "unknown",
    root = CONTROL_ROOT,
    registry,
    repositories,
    caps = {},
    readCi = true,
    readLedgers = true,
    writeLedger: shouldWriteLedger = true,
    writeDashboard = true,
    warn = console.warn,
    log = console.log
  } = options;

  assertAllowedRepo(controlRepo);
  const workerCaps = normalizeCaps(caps);
  const targets = await targetRepositories(gh, controlRepo, { root, registry, repositories, warn });
  const states = [];
  const actions = [];
  const dispatched = [];

  for (const target of targets) {
    try {
      states.push(await reconstructRepositoryState(gh, target, { dataRepo, readCi, readLedgers }));
    } catch (error) {
      if (warnReadOnlyRepository(target, error, "orchestration")) continue;
      const message = error.message || String(error);
      states.push({
        repository: target,
        repo: repoName(target),
        error: message,
        defaultBranch: "",
        prdState: { present: false, path: "PRD.md" },
        issues: [],
        openIssueNumbers: new Set(),
        counts: emptyCounts()
      });
      actions.push({ repo: repoName(target), action: "state-error", error: message });
      warn(`Failed to reconstruct L0 state for ${repoName(target)}: ${message}`);
    }
  }

  const plan = planOrchestratorWave(states, workerCaps);

  for (const action of plan.labelActions) {
    try {
      await applyLabelAction(gh, action);
      actions.push(action);
    } catch (error) {
      if (warnReadOnlyRepository(action.repository, error, action.action)) continue;
      const message = error.message || String(error);
      actions.push({ ...publicAction(action), action: `${action.action}-failed`, error: message });
      warn(`Failed to apply ${action.action} for ${repoName(action.repository)}#${action.issue}: ${message}`);
    }
  }

  for (const action of plan.ownerEscalations) {
    try {
      await escalateOwnerQuestion(gh, action);
      actions.push(action);
    } catch (error) {
      if (warnReadOnlyRepository(action.repository, error, "owner escalation")) continue;
      const message = error.message || String(error);
      actions.push({ ...publicAction(action), action: "owner-escalation-failed", error: message });
      warn(`Failed to escalate owner question for ${repoName(action.repository)}#${action.issue}: ${message}`);
    }
  }

  for (const dispatch of plan.dispatches) {
    try {
      const wasDispatched = await dispatchWorker(gh, controlRepo, dispatch.repository, dispatch.issue);
      if (wasDispatched) {
        const record = {
          repo: repoName(dispatch.repository),
          issue: dispatch.issue,
          priority: dispatch.priority,
          stream: dispatch.stream
        };
        dispatched.push(record);
        actions.push({ action: "dispatch-worker", ...record });
      } else {
        actions.push({
          action: "claim-existing-worker-pr",
          repo: repoName(dispatch.repository),
          issue: dispatch.issue
        });
      }
    } catch (error) {
      if (warnReadOnlyRepository(dispatch.repository, error, "worker dispatch")) continue;
      const message = error.message || String(error);
      actions.push({
        action: "dispatch-worker-failed",
        repo: repoName(dispatch.repository),
        issue: dispatch.issue,
        error: message
      });
      warn(`Failed to dispatch worker for ${repoName(dispatch.repository)}#${dispatch.issue}: ${message}`);
    }
  }

  const globalStateBrief = synthesizeGlobalStateBrief(states, plan, dispatched, actions, workerCaps);
  const ledger = {
    trigger,
    control_repo: repoName(controlRepo),
    caps: workerCaps,
    state: states.map(summarizeRepositoryState),
    plan: summarizePlan(plan),
    actions: actions.map(publicAction),
    dispatched,
    global_state_brief: globalStateBrief,
    token_usage: {
      codex_calls: 0,
      input_tokens: 0,
      output_tokens: 0,
      note: "L0 orchestrator state reconstruction, sequencing, caps, dashboard, and owner escalation are deterministic; no AI judgment run was invoked"
    }
  };

  if (writeDashboard) {
    try {
      const dashboard = await upsertDashboardIssue(gh, controlRepo, globalStateBrief);
      ledger.dashboard = dashboard;
      log(`DarkFactory dashboard updated at ${dashboard.url || `${repoName(controlRepo)}#${dashboard.number}`}`);
    } catch (error) {
      if (!warnReadOnlyRepository(controlRepo, error, "dashboard update", warn)) {
        warn(`DarkFactory dashboard warning: ${error.message || String(error)}`);
      }
    }
  }

  if (shouldWriteLedger) {
    await writeLedger(gh, dataRepo, controlRepo, ledger, warn, log);
  }
  log(`DarkFactory orchestrator dispatched ${dispatched.length} worker runs.`);
  return { dispatched, ledger };
}

export async function targetRepositories(gh, controlRepo, options = {}) {
  return await listActiveManagedRepos(gh, controlRepo, options);
}

export async function listReadyIssues(gh, repository) {
  const issues = [];
  for (let page = 1; page <= 20; page += 1) {
    const labels = encodeURIComponent("df:ready");
    const batch = await gh.request(
      "GET",
      `/repos/${repoName(repository)}/issues?state=open&labels=${labels}&per_page=100&page=${page}`
    );
    if (!Array.isArray(batch) || batch.length === 0) break;
    issues.push(...batch.filter((issue) => !issue.pull_request));
    if (batch.length < 100) break;
  }

  return issues.filter((issue) => {
    const names = new Set(
      (issue.labels || []).map((label) => (typeof label === "string" ? label : label?.name)).filter(Boolean)
    );
    if (!names.has("df:ready")) return false;
    if (names.has("df:running") || names.has("df:blocked") || names.has("df:done")) return false;
    return true;
  });
}

export async function reconstructRepositoryState(gh, repository, options = {}) {
  const [repo, issues, prdContent] = await Promise.all([
    getRepository(gh, repository),
    listOpenIssues(gh, repository),
    getOptionalFileContent(gh, repository, "PRD.md").catch((error) => {
      if (error.status === 404) return null;
      throw error;
    })
  ]);

  const normalizedIssues = issues.map((issue) => normalizeIssue(repository, issue));
  await attachBlockedIssueHistory(gh, repository, normalizedIssues);
  const [ciState, ledgerState] = await Promise.all([
    options.readCi === false ? null : getLatestCiState(gh, repository, repo.default_branch || ""),
    options.readLedgers === false ? null : getLatestLedgerIndex(gh, options.dataRepo || DEFAULT_DATA_REPO, repository)
  ]);
  return {
    repository,
    repo: repoName(repository),
    defaultBranch: repo.default_branch || "",
    gitState: {
      default_branch: repo.default_branch || "",
      pushed_at: repo.pushed_at || null,
      archived: repo.archived === true,
      disabled: repo.disabled === true,
      open_issues_count: Number.isInteger(repo.open_issues_count) ? repo.open_issues_count : null
    },
    ciState,
    ledgerState,
    prdState: { present: typeof prdContent === "string" && prdContent.trim().length > 0, path: "PRD.md" },
    issues: normalizedIssues,
    openIssueNumbers: new Set(normalizedIssues.map((issue) => issue.number)),
    counts: countLoopState(normalizedIssues)
  };
}

export function planOrchestratorWave(states, caps = {}) {
  const workerCaps = normalizeCaps(caps);
  const running = {
    global: states.reduce((sum, state) => sum + state.counts.running, 0),
    byRepo: new Map(),
    byStream: new Map()
  };
  const labelActions = [];
  const ownerEscalations = [];
  const candidates = [];
  const skipped = [];

  for (const state of states) {
    if (state.error) continue;
    running.byRepo.set(state.repo, state.counts.running);
    for (const issue of state.issues) {
      if (issue.labels.has("df:running")) increment(running.byStream, streamKey(state.repo, issue.stream), 1);
    }
  }

  for (const state of states) {
    if (state.error) continue;
    for (const issue of state.issues) {
      const openBlockers = issue.blockedBy.filter((number) => state.openIssueNumbers.has(number));

      if (issue.labels.has("df:blocked") && !issue.labels.has("df:ask-owner")) {
        const reason = ownerOnlyBlockerReason(issue);
        if (reason) {
          ownerEscalations.push({
            repository: state.repository,
            repo: state.repo,
            issue: issue.number,
            action: "escalate-owner-question",
            reason
          });
        }
      }

      if (issue.labels.has("df:ready") && openBlockers.length > 0) {
        labelActions.push({
          repository: state.repository,
          repo: state.repo,
          issue: issue.number,
          action: "hold-sequenced-issue",
          add: [],
          remove: ["df:ready"],
          reason: `Blocked-by ${openBlockers.map((number) => `#${number}`).join(", ")} is still open`
        });
        skipped.push({
          repo: state.repo,
          issue: issue.number,
          reason: "blocked-by-open",
          blockers: openBlockers
        });
        continue;
      }

      if (shouldReadyIssue(issue, openBlockers)) {
        labelActions.push({
          repository: state.repository,
          repo: state.repo,
          issue: issue.number,
          action: "ready-unblocked-issue",
          add: ["df:ready"],
          remove: [],
          reason: issue.blockedBy.length ? "all Blocked-by issues are closed" : "planned issue has no open blockers"
        });
        issue.labels.add("df:ready");
      }

      if (isDispatchCandidate(issue, openBlockers)) {
        candidates.push({ state, issue });
      }
    }
  }

  candidates.sort(compareCandidates);
  const dispatches = [];

  for (const candidate of candidates) {
    const repo = candidate.state.repo;
    const stream = streamKey(repo, candidate.issue.stream);
    const repoRunning = running.byRepo.get(repo) || 0;
    const streamRunning = running.byStream.get(stream) || 0;

    if (running.global >= workerCaps.global) {
      skipped.push({ repo, issue: candidate.issue.number, reason: "global-cap", cap: workerCaps.global });
      continue;
    }
    if (repoRunning >= workerCaps.perRepo) {
      skipped.push({ repo, issue: candidate.issue.number, reason: "repo-cap", cap: workerCaps.perRepo });
      continue;
    }
    if (streamRunning >= workerCaps.perStream) {
      skipped.push({ repo, issue: candidate.issue.number, reason: "stream-cap", stream: candidate.issue.stream, cap: workerCaps.perStream });
      continue;
    }

    dispatches.push({
      repository: candidate.state.repository,
      repo,
      issue: candidate.issue.number,
      priority: candidate.issue.priority,
      stream: candidate.issue.stream
    });
    running.global += 1;
    running.byRepo.set(repo, repoRunning + 1);
    running.byStream.set(stream, streamRunning + 1);
  }

  return { caps: workerCaps, dispatches, labelActions, ownerEscalations, skipped };
}

export async function dispatchWorker(gh, controlRepo, repository, issueNumber) {
  const existingPullRequest = await findOpenWorkerPullRequestForIssue(gh, repository, issueNumber);
  if (existingPullRequest) {
    await replaceIssueLabels(gh, repository, issueNumber, ["df:running"], ["df:ready"]);
    return false;
  }

  const repo = await getRepository(gh, repository);
  const workBaseBranch = await resolveWorkBaseBranch(gh, repository, repo.default_branch);
  const mergePolicy = await preflightMergePolicy(gh, repository, workBaseBranch, repo);
  if (mergePolicy.blocked) {
    await blockIssueBeforeDispatch(gh, repository, issueNumber, workBaseBranch, mergePolicy);
    return false;
  }

  // Claim the issue before dispatch so a subsequent orchestrator tick cannot
  // re-dispatch the same ready issue while the worker workflow is starting.
  await replaceIssueLabels(gh, repository, issueNumber, ["df:running"], ["df:ready"]);
  try {
    await gh.request("POST", `/repos/${repoName(controlRepo)}/actions/workflows/df-work.yml/dispatches`, {
      ref: "main",
      inputs: {
        repo: repoName(repository),
        issue_number: String(issueNumber)
      }
    });
  } catch (error) {
    // Restore df:ready so the next orchestrator tick can retry; do not leave
    // the issue stranded in df:running when dispatch failed.
    await replaceIssueLabels(gh, repository, issueNumber, ["df:ready"], ["df:running"]);
    throw error;
  }
  return true;
}

async function listOpenIssues(gh, repository) {
  const issues = [];
  for (let page = 1; page <= 20; page += 1) {
    const batch = await gh.request(
      "GET",
      `/repos/${repoName(repository)}/issues?state=open&per_page=100&page=${page}`
    );
    if (!Array.isArray(batch) || batch.length === 0) break;
    issues.push(...batch.filter((issue) => !issue.pull_request));
    if (batch.length < 100) break;
  }
  return issues;
}

async function applyLabelAction(gh, action) {
  await ensureLabels(gh, action.repository, WORK_LABELS);
  await replaceIssueLabels(gh, action.repository, action.issue, action.add, action.remove);
}

async function escalateOwnerQuestion(gh, action) {
  await ensureLabels(gh, action.repository, WORK_LABELS);
  await replaceIssueLabels(gh, action.repository, action.issue, ["df:ask-owner"], []);
  if (!(await hasIssueComment(gh, action.repository, action.issue, OWNER_ESCALATION_MARKER))) {
    await createIssueComment(
      gh,
      action.repository,
      action.issue,
      [
        OWNER_ESCALATION_MARKER,
        "DarkFactory L0 escalated this blocker for owner input.",
        "",
        `Question: ${action.reason}`,
        "",
        "This stays in GitHub as the durable decision record; the orchestrator will continue other lanes."
      ].join("\n")
    );
  }
}

async function upsertDashboardIssue(gh, controlRepo, digest) {
  await ensureLabels(gh, controlRepo, WORK_LABELS);
  const title = "DarkFactory Orchestrator Dashboard";
  const body = [DASHBOARD_MARKER, digest].join("\n\n");
  const issues = await listIssuesAll(gh, controlRepo);
  const existing = issues.find((issue) => String(issue.body || "").includes(DASHBOARD_MARKER));

  if (existing) {
    const updated = await gh.request("PATCH", `/repos/${repoName(controlRepo)}/issues/${existing.number}`, {
      title,
      body,
      state: "open"
    });
    return { number: updated.number, url: updated.html_url, action: "update-dashboard" };
  }

  const created = await gh.request("POST", `/repos/${repoName(controlRepo)}/issues`, {
    title,
    body,
    labels: []
  });
  return { number: created.number, url: created.html_url, action: "create-dashboard" };
}

async function listIssuesAll(gh, repository) {
  const issues = [];
  for (let page = 1; page <= 20; page += 1) {
    const batch = await gh.request(
      "GET",
      `/repos/${repoName(repository)}/issues?state=all&per_page=100&page=${page}`
    );
    if (!Array.isArray(batch) || batch.length === 0) break;
    issues.push(...batch.filter((issue) => !issue.pull_request));
    if (batch.length < 100) break;
  }
  return issues;
}

async function resolveWorkBaseBranch(gh, repository, defaultBranch) {
  try {
    await gh.request("GET", `/repos/${repoName(repository)}/git/ref/heads/${encodeURIComponent("dev")}`);
    return "dev";
  } catch (error) {
    if (error.status === 404) return defaultBranch;
    throw error;
  }
}

async function blockIssueBeforeDispatch(gh, repository, issueNumber, baseBranch, mergePolicy) {
  await ensureLabels(gh, repository, WORK_LABELS);
  await replaceIssueLabels(gh, repository, issueNumber, ["df:blocked"], ["df:ready", "df:running", "df:done"]);
  await createIssueComment(
    gh,
    repository,
    issueNumber,
    [
      "DarkFactory blocked this issue before worker dispatch.",
      "",
      "Blocker:",
      "",
      "```text",
      mergePolicy.reason,
      "```",
      "",
      `Target branch: \`${baseBranch}\``,
      `Repository auto-merge enabled: \`${mergePolicy.autoMergeSupported ? "yes" : "no"}\``,
      "",
      "This is target repository setup work, not a code implementation failure."
    ].join("\n")
  );
}

async function replaceIssueLabels(gh, repository, issueNumber, add, remove) {
  if (add.length) {
    await gh.request("POST", `/repos/${repoName(repository)}/issues/${issueNumber}/labels`, { labels: add });
  }
  for (const label of remove) {
    try {
      await gh.request("DELETE", `/repos/${repoName(repository)}/issues/${issueNumber}/labels/${encodeURIComponent(label)}`);
    } catch (error) {
      if (error.status !== 404) throw error;
    }
  }
}

async function createIssueComment(gh, repository, issueNumber, body) {
  await gh.request("POST", `/repos/${repoName(repository)}/issues/${issueNumber}/comments`, { body });
}

async function hasIssueComment(gh, repository, issueNumber, marker) {
  const comments = await gh.request(
    "GET",
    `/repos/${repoName(repository)}/issues/${issueNumber}/comments?per_page=100`
  );
  return Array.isArray(comments) && comments.some((comment) => String(comment.body || "").includes(marker));
}

async function attachBlockedIssueHistory(gh, repository, issues) {
  const blocked = issues.filter((issue) => issue.labels.has("df:blocked") && !issue.labels.has("df:ask-owner"));
  await Promise.all(blocked.map(async (issue) => {
    const comments = await gh.request(
      "GET",
      `/repos/${repoName(repository)}/issues/${issue.number}/comments?per_page=100`
    );
    issue.history = Array.isArray(comments)
      ? comments.map((comment) => String(comment.body || "")).join("\n\n")
      : "";
  }));
}

async function getLatestCiState(gh, repository, branch) {
  if (!branch) return null;
  try {
    const data = await gh.request(
      "GET",
      `/repos/${repoName(repository)}/actions/runs?branch=${encodeURIComponent(branch)}&per_page=10`
    );
    const runs = Array.isArray(data?.workflow_runs) ? data.workflow_runs : [];
    const latest = runs[0];
    return {
      branch,
      latest: latest ? {
        id: latest.id,
        name: latest.name || "",
        status: latest.status || "",
        conclusion: latest.conclusion || null,
        html_url: latest.html_url || ""
      } : null,
      recent_failures: runs.filter((run) => run.conclusion === "failure").length
    };
  } catch (error) {
    if (error.status === 403 || error.status === 404) {
      return { branch, unavailable: true, reason: error.message || String(error) };
    }
    throw error;
  }
}

async function getLatestLedgerIndex(gh, dataRepo, repository) {
  const ledgerRepo = parseRepo(dataRepo || DEFAULT_DATA_REPO);
  const ledgerPath = `runs/${repoName(repository)}`;
  try {
    const data = await gh.request(
      "GET",
      `/repos/${repoName(ledgerRepo)}/contents/${encodePath(ledgerPath)}`
    );
    const files = Array.isArray(data) ? data : [];
    return files
      .filter((entry) => entry?.type === "file" && typeof entry.name === "string" && entry.name.endsWith(".json"))
      .map((entry) => entry.name)
      .sort()
      .reverse()
      .slice(0, 5)
      .map((name) => ({
        name,
        kind: name.match(/-(df-[a-z-]+)\.json$/)?.[1] || "unknown"
      }));
  } catch (error) {
    if (error.status === 403 || error.status === 404) return [];
    throw error;
  }
}

async function writeLedger(gh, dataRepo, controlRepo, ledger, warn = console.warn, log = console.log) {
  try {
    const written = await writeRunLedger(gh, dataRepo, "df-orchestrate", repoName(controlRepo), ledger);
    log(`DarkFactory ledger written to ${written.repository}/${written.path}`);
  } catch (error) {
    warn(`DarkFactory ledger warning: ${error.message || String(error)}`);
  }
}

function normalizeIssue(repository, issue) {
  const labels = new Set(labelNames(issue.labels));
  const priority = ["P0", "P1", "P2"].find((label) => labels.has(label)) || "P3";
  const stream = labelNames(issue.labels).find((label) => label.startsWith("stream:"))?.slice("stream:".length) || "default";

  return {
    repository,
    repo: repoName(repository),
    number: issue.number,
    title: issue.title || `#${issue.number}`,
    body: issue.body || "",
    labels,
    priority,
    priorityRank: priorityRank(priority),
    stream,
    blockedBy: extractBlockedBy(issue.body || "")
  };
}

function labelNames(labels) {
  return (Array.isArray(labels) ? labels : [])
    .map((label) => typeof label === "string" ? label : label?.name)
    .filter(Boolean);
}

function extractBlockedBy(body) {
  const numbers = [];
  for (const line of String(body || "").split(/\r?\n/)) {
    const match = line.match(/^Blocked-by:\s*#(\d+)\s*$/i);
    if (match) numbers.push(Number(match[1]));
  }
  return numbers;
}

function countLoopState(issues) {
  const counts = emptyCounts();
  for (const issue of issues) {
    if (issue.labels.has("df:ready")) counts.ready += 1;
    if (issue.labels.has("df:running")) counts.running += 1;
    if (issue.labels.has("df:blocked")) counts.blocked += 1;
    if (issue.labels.has("df:ask-owner")) counts.askOwner += 1;
    if (issue.labels.has("df:done")) counts.done += 1;
  }
  return counts;
}

function emptyCounts() {
  return { ready: 0, running: 0, blocked: 0, askOwner: 0, done: 0 };
}

function shouldReadyIssue(issue, openBlockers) {
  if (openBlockers.length > 0) return false;
  if (!isPlannedWorkIssue(issue)) return false;
  if (issue.labels.has("df:ready")) return false;
  if (issue.labels.has("df:running") || issue.labels.has("df:blocked") || issue.labels.has("df:done") || issue.labels.has("df:ask-owner")) return false;
  return true;
}

function isDispatchCandidate(issue, openBlockers) {
  if (openBlockers.length > 0) return false;
  if (!issue.labels.has("df:ready")) return false;
  if (issue.labels.has("df:running") || issue.labels.has("df:blocked") || issue.labels.has("df:done") || issue.labels.has("df:ask-owner")) return false;
  return true;
}

function isPlannedWorkIssue(issue) {
  if (issue.labels.has("roadmap")) return true;
  return [...issue.labels].some((label) => label.startsWith("df:class:"));
}

function compareCandidates(a, b) {
  return (
    a.issue.priorityRank - b.issue.priorityRank ||
    a.state.repo.localeCompare(b.state.repo) ||
    a.issue.stream.localeCompare(b.issue.stream) ||
    a.issue.number - b.issue.number
  );
}

function priorityRank(priority) {
  if (priority === "P0") return 0;
  if (priority === "P1") return 1;
  if (priority === "P2") return 2;
  return 3;
}

function ownerOnlyBlockerReason(issue) {
  const text = `${issue.title}\n${issue.body}\n${issue.history || ""}`.toLowerCase();
  if (/\b(code[x]?_auth_json|secret|private key|app id)\b/i.test(text)) {
    return "A required secret or credential must be configured by the repository owner.";
  }
  if (/\b(auto-merge|automerge)\b/i.test(text) && /\b(disabled|enable|not configured|requires)\b/i.test(text)) {
    return "Repository auto-merge or branch protection settings require owner configuration.";
  }
  if (/\b(resource not accessible by integration|permission|forbidden|protected branch)\b/i.test(text)) {
    return "GitHub permissions or protected branch settings require owner action.";
  }
  if (/\b(owner decision|owner input|manual decision|ambiguous|choose between|clarify)\b/i.test(text)) {
    return "The blocker asks for an owner decision before autonomous work can continue.";
  }
  return "";
}

function increment(map, key, amount) {
  map.set(key, (map.get(key) || 0) + amount);
}

function streamKey(repo, stream) {
  return `${repo}\u0000${stream || "default"}`;
}

function normalizeCaps(caps = {}) {
  return {
    global: positiveInt(caps.global, DEFAULT_GLOBAL_WORKER_CAP),
    perRepo: positiveInt(caps.perRepo, DEFAULT_REPO_WORKER_CAP),
    perStream: positiveInt(caps.perStream, DEFAULT_STREAM_WORKER_CAP)
  };
}

function positiveInt(value, fallback) {
  const number = Number(value);
  return Number.isInteger(number) && number > 0 ? number : fallback;
}

function envInt(name, fallback) {
  return positiveInt(process.env[name], fallback);
}

function summarizeRepositoryState(state) {
  return {
    repo: state.repo,
    default_branch: state.defaultBranch,
    error: state.error || "",
    git: state.gitState || null,
    ci: state.ciState || null,
    ledgers: state.ledgerState || [],
    prd: state.prdState,
    counts: state.counts,
    ready: state.issues.filter((issue) => issue.labels.has("df:ready")).map(issueSummary),
    running: state.issues.filter((issue) => issue.labels.has("df:running")).map(issueSummary),
    blocked: state.issues.filter((issue) => issue.labels.has("df:blocked") || issue.labels.has("df:ask-owner")).map(issueSummary)
  };
}

function summarizePlan(plan) {
  return {
    caps: plan.caps,
    dispatches: plan.dispatches.map((dispatch) => ({
      repo: dispatch.repo,
      issue: dispatch.issue,
      priority: dispatch.priority,
      stream: dispatch.stream
    })),
    label_actions: plan.labelActions.map(publicAction),
    owner_escalations: plan.ownerEscalations.map(publicAction),
    skipped: plan.skipped
  };
}

function publicAction(action) {
  const { repository, ...rest } = action;
  return rest;
}

function issueSummary(issue) {
  return {
    number: issue.number,
    title: issue.title,
    priority: issue.priority,
    stream: issue.stream,
    blocked_by: issue.blockedBy
  };
}

function synthesizeGlobalStateBrief(states, plan, dispatched, actions, caps) {
  const totals = states.reduce((sum, state) => {
    sum.ready += state.counts.ready;
    sum.running += state.counts.running;
    sum.blocked += state.counts.blocked;
    sum.askOwner += state.counts.askOwner;
    return sum;
  }, emptyCounts());

  const lines = [
    "# DarkFactory L0 Orchestrator Digest",
    "",
    `Generated: ${new Date().toISOString()}`,
    `Worker caps: global ${caps.global}, per repo ${caps.perRepo}, per stream ${caps.perStream}`,
    `Totals: ${states.length} repos, ${totals.ready} ready, ${totals.running} running, ${totals.blocked} blocked, ${totals.askOwner} ask-owner`,
    `Dispatched this tick: ${dispatched.length}`,
    "",
    "## Decisions",
    ""
  ];

  const publicActions = actions.map(publicAction);
  if (publicActions.length === 0 && dispatched.length === 0) {
    lines.push("- No state changes were needed.");
  } else {
    for (const action of publicActions.slice(0, 40)) {
      lines.push(`- ${formatAction(action)}`);
    }
    if (publicActions.length > 40) lines.push(`- ${publicActions.length - 40} additional actions omitted from dashboard digest.`);
  }

  lines.push("", "## Repositories", "");
  for (const state of states) {
    if (state.error) {
      lines.push(`- ${state.repo}: state reconstruction failed (${state.error})`);
      continue;
    }
    const prd = state.prdState.present ? "PRD present" : "PRD missing";
    const ci = state.ciState?.latest
      ? `CI ${state.ciState.latest.status}/${state.ciState.latest.conclusion || "none"}`
      : state.ciState?.unavailable
        ? "CI unavailable"
        : "CI none";
    const ledgerCount = Array.isArray(state.ledgerState) ? state.ledgerState.length : 0;
    lines.push(`- ${state.repo}: ${prd}; ${ci}; ledgers ${ledgerCount}; ready ${state.counts.ready}, running ${state.counts.running}, blocked ${state.counts.blocked}, ask-owner ${state.counts.askOwner}`);
  }

  if (plan.skipped.length) {
    lines.push("", "## Skipped Dispatches", "");
    for (const item of plan.skipped.slice(0, 30)) {
      lines.push(`- ${item.repo}#${item.issue}: ${item.reason}${item.cap ? ` (cap ${item.cap})` : ""}`);
    }
  }

  lines.push(
    "",
    "## Harness Migration",
    "",
    "- This L0 tick is deterministic GitHub-native state reconstruction. The scheduler boundary can move to agents-harness without adding DarkFactory-local memory."
  );

  return lines.join("\n");
}

function formatAction(action) {
  if (action.action === "dispatch-worker") {
    return `Dispatch ${action.repo}#${action.issue} (${action.priority}, stream:${action.stream})`;
  }
  if (action.action === "ready-unblocked-issue") {
    return `Ready ${action.repo}#${action.issue}: ${action.reason}`;
  }
  if (action.action === "hold-sequenced-issue") {
    return `Hold ${action.repo}#${action.issue}: ${action.reason}`;
  }
  if (action.action === "escalate-owner-question") {
    return `Escalate ${action.repo}#${action.issue}: ${action.reason}`;
  }
  return `${action.action}${action.repo ? ` ${action.repo}` : ""}${action.issue ? `#${action.issue}` : ""}`;
}

function encodePath(filePath) {
  return filePath.split("/").map(encodeURIComponent).join("/");
}
