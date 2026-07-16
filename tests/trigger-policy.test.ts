import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

// @ts-ignore Native ESM workflow policy is exercised directly.
const policyModule: any = await import("../.github/scripts/df-trigger-policy.mjs");
// @ts-ignore Trusted recovery controller is native ESM and exercised directly.
const recoveryModule: any = await import("../.github/scripts/df-autoreview-recovery.mjs?unit=trigger-policy-recovery-test");
const {
  REQUIRED_LOOP_IDS,
  admitLoopInvocation,
  collectLoopWorkflowEvidence,
  loopStatusMarkdownRows,
  projectLoopStatus,
  readTriggerPolicy,
  renderLoopIdempotencyKey,
  validateTriggerPolicy
} = policyModule;
const root = path.resolve(import.meta.dirname, "..");

test("trigger policy covers every development loop with explicit cadence, admission, and authorization", async () => {
  const policy = await readTriggerPolicy(root);
  assert.equal(policy.policyVersion, "1.1.0");
  assert.deepEqual(policy.loops.map((loop: any) => loop.id).sort(), [...REQUIRED_LOOP_IDS].sort());
  for (const loop of policy.loops) {
    assert.match(loop.idempotencyKey, /\{repository\}.*\{target\}.*\{sourceVersion\}/);
    if (loop.recovery) assert.ok(loop.recovery.maxDetectionMinutes >= loop.recovery.intervalMinutes);
    assert.equal(loop.authorization.receiptRequired, true);
    assert.equal(loop.retry.escalationLabel, "df:ask-owner");
    if (loop.status === "active") {
      const workflow = await readFile(path.join(root, loop.recoveryWorkflow), "utf8");
      assert.ok(workflow.includes(`cron: "${loop.recovery.cron}"`), `${loop.id} schedule matches its active recovery workflow`);
      const eventSources = (await Promise.all(
        loop.eventWorkflows.map((workflowPath: string) => readFile(path.join(root, workflowPath), "utf8"))
      )).join("\n");
      for (const trigger of loop.eventTriggers) {
        assert.match(eventSources, new RegExp(`^\\s{2}${trigger.event}:`, "m"), `${loop.id} declares only live event triggers`);
      }
    }
  }
  const drafting = policy.loops.find((loop: any) => loop.id === "interactive-issue-drafting");
  assert.equal(drafting.status, "planned");
  assert.deepEqual(drafting.eventWorkflows, []);
  assert.equal(drafting.recoveryWorkflow, null);
  assert.deepEqual(drafting.eventTriggers, []);
  assert.equal(drafting.recovery, null);
});

test("policy validation rejects unknown loops, stale latency, missing receipt admission, and non-independent model policy", async () => {
  const policy = await readTriggerPolicy(root);
  const extra = structuredClone(policy);
  extra.loops.push({ ...structuredClone(extra.loops[0]), id: "shadow-loop" });
  extra.loops.at(-1).idempotencyKey = "shadow-loop:{repository}:{target}:{sourceVersion}";
  assert.throws(() => validateTriggerPolicy(extra), /coverage mismatch/);

  const latency = structuredClone(policy);
  latency.loops[0].recovery.maxDetectionMinutes = latency.loops[0].recovery.intervalMinutes - 1;
  assert.throws(() => validateTriggerPolicy(latency), /must cover its schedule interval/);

  const receipt = structuredClone(policy);
  receipt.loops[0].authorization.receiptRequired = false;
  assert.throws(() => validateTriggerPolicy(receipt), /receipt admission/);

  const providerNamed = structuredClone(policy);
  providerNamed.loops[0].modelPolicy = "specific-provider";
  assert.throws(() => validateTriggerPolicy(providerNamed), /unknown model policy/);

  const fakeDraftWorkflow = structuredClone(policy);
  const drafting = fakeDraftWorkflow.loops.find((loop: any) => loop.id === "interactive-issue-drafting");
  drafting.eventWorkflows = [".github/workflows/df-issue-draft.yml"];
  drafting.recoveryWorkflow = ".github/workflows/df-issue-draft.yml";
  drafting.eventTriggers = [{ event: "schedule", types: [] }];
  drafting.recovery = { cron: "19 */6 * * *", intervalMinutes: 360, maxDetectionMinutes: 390 };
  assert.throws(() => validateTriggerPolicy(fakeDraftWorkflow), /explicitly local, human-driven/);
});

test("event and schedule recovery share one idempotency key while stale or untrusted events fail closed", async () => {
  const policy = await readTriggerPolicy(root);
  const context = {
    repository: "marius-patrik/DarkFactory",
    target: "issue-48",
    sourceVersion: "abc123",
    sourceRef: "refs/heads/main",
    deliveredAt: "2026-07-15T10:00:00.000Z"
  };
  const event = admitLoopInvocation(policy, "worker-dispatch", context, new Date("2026-07-15T10:05:00.000Z"));
  const recovered = renderLoopIdempotencyKey(policy, "worker-dispatch", context);
  assert.equal(event.idempotencyKey, recovered);

  assert.throws(
    () => admitLoopInvocation(policy, "worker-dispatch", { ...context, sourceRef: "refs/heads/dev" }, new Date("2026-07-15T10:05:00.000Z")),
    /trusted source ref/
  );
  assert.throws(
    () => admitLoopInvocation(policy, "worker-dispatch", context, new Date("2026-07-15T10:30:01.000Z")),
    /stale and must be replanned/
  );
  const submodule = admitLoopInvocation(
    policy,
    "submodule-autoupdate",
    { ...context, target: "plugins/DarkFactory" },
    new Date("2026-07-15T10:05:00.000Z")
  );
  assert.equal(submodule.loopId, "submodule-autoupdate");
  assert.match(submodule.idempotencyKey, /^submodule-autoupdate:/);
});

test("loop status projection exposes success, next run, source, retry, stale, and planned blockers", async () => {
  const policy = await readTriggerPolicy(root);
  const evidence: Record<string, any[]> = {};
  for (const loop of policy.loops.filter((entry: any) => entry.status === "active")) {
    evidence[loop.eventWorkflows[0]] = [{
      head_branch: "main",
      head_sha: "1234567890abcdef",
      created_at: "2026-07-15T09:50:00.000Z",
      updated_at: "2026-07-15T09:51:00.000Z",
      status: "completed",
      conclusion: "success",
      run_attempt: 1
    }];
  }
  evidence[".github/workflows/df-audit.yml"] = [{
    head_branch: "main",
    head_sha: "badbadbadbadbadb",
    created_at: "2026-07-13T09:00:00.000Z",
    updated_at: "2026-07-13T09:01:00.000Z",
    status: "completed",
    conclusion: "failure",
    run_attempt: 3
  }];
  const statuses = projectLoopStatus(policy, evidence, new Date("2026-07-15T10:00:00.000Z"));
  const doctor = statuses.find((entry: any) => entry.id === "repository-doctor");
  assert.equal(doctor.state, "stale");
  assert.equal(doctor.retry, "escalate:df:ask-owner");
  const worker = statuses.find((entry: any) => entry.id === "worker-dispatch");
  assert.equal(worker.state, "success");
  assert.equal(worker.source, "refs/heads/main@1234567890ab");
  assert.match(worker.nextExpected, /^2026-07-15T10:01:00/);
  const submodules = statuses.find((entry: any) => entry.id === "submodule-autoupdate");
  assert.equal(submodules.state, "success");
  assert.equal(submodules.retry, "idle");
  const markdown = loopStatusMarkdownRows(statuses);
  assert.match(markdown, /worker-dispatch/);
  assert.match(markdown, /submodule-autoupdate/);
});

test("workflow evidence is fetched only for active trusted workflow names", async () => {
  const policy = await readTriggerPolicy(root);
  const calls: string[] = [];
  const evidence = await collectLoopWorkflowEvidence({
    async request(method: string, requestPath: string) {
      calls.push(`${method} ${requestPath}`);
      return { workflow_runs: [] };
    }
  }, { owner: "marius-patrik", repo: "DarkFactory" }, policy);
  assert.ok(calls.every((call) => call.includes("/actions/workflows/") && call.includes("branch=main")));
  assert.equal(calls.some((call) => call.includes("df-release.yml")), true);
  assert.equal(calls.some((call) => call.includes("df-submodule-autoupdate.yml")), true);
  assert.ok(Object.prototype.hasOwnProperty.call(evidence, ".github/workflows/df-orchestrate.yml"));
  assert.ok(Object.prototype.hasOwnProperty.call(evidence, ".github/workflows/df-submodule-autoupdate.yml"));
  assert.ok(Object.prototype.hasOwnProperty.call(evidence, ".github/workflows/df-autoreview-recovery.yml"));
  assert.ok(Object.prototype.hasOwnProperty.call(evidence, ".github/workflows/df-clean.yml"));
});

test("Autoreview recovery lifecycle-filters code repositories and dispatches exact PR and issue versions", async () => {
  const SHA_A = "a".repeat(40);
  const SHA_B = "b".repeat(40);
  const issue = { number: 9, title: "Exact issue", body: "Review this contract", state: "open", labels: [] };
  const pull = {
    number: 7,
    state: "open",
    draft: false,
    base: { sha: SHA_A },
    head: { sha: SHA_B, repo: { full_name: "marius-patrik/DarkFactory" } }
  };
  const calls: Array<{ method: string; path: string; body?: any }> = [];
  const ledgers: Array<{ kind: string; target: string; payload: any }> = [];
  let commentId = 100;
  const gh = {
    async request(method: string, requestPath: string, body?: any) {
      calls.push({ method, path: requestPath, body });
      if (method === "GET" && requestPath === "/repos/marius-patrik/DarkFactory") return { archived: false, disabled: false };
      if (method === "GET" && requestPath.includes("/repos/marius-patrik/DarkFactory/pulls?")) return [pull];
      if (method === "GET" && requestPath.includes("/repos/marius-patrik/Andromeda/pulls?")) return [];
      if (method === "GET" && requestPath.includes("/repos/marius-patrik/DarkFactory/issues?")) return [];
      if (method === "GET" && requestPath.includes("/repos/marius-patrik/Andromeda/issues?")) return [issue];
      if (method === "GET" && /\/issues\/(?:7|9)\/comments\?/.test(requestPath)) return [];
      if (method === "GET" && requestPath.endsWith(`/commits/${SHA_B}/check-runs?per_page=100`)) return { check_runs: [] };
      if (method === "GET" && requestPath === "/repos/marius-patrik/DarkFactory/pulls/7") return pull;
      if (method === "GET" && requestPath === "/repos/marius-patrik/Andromeda/issues/9") return issue;
      if (method === "POST" && /\/issues\/(?:7|9)\/comments$/.test(requestPath)) return { id: commentId++, html_url: `https://github.com/comment/${commentId}` };
      if (method === "POST" && requestPath.endsWith("/actions/workflows/darkfactory-autoreview.yml/dispatches")) return {};
      throw new Error(`unexpected ${method} ${requestPath}`);
    }
  };
  recoveryModule.configureAutoreviewRecoveryRuntime({
    gh,
    ledgerGh: gh,
    controlRepo: { owner: "marius-patrik", repo: "DarkFactory" },
    controlRevision: "c".repeat(40),
    controlMetadata: { archived: false, disabled: false },
    activeRepositories: [
      { owner: "marius-patrik", repo: "Andromeda" },
      { owner: "marius-patrik", repo: "Andromeda-data" }
    ],
    dataRepositories: ["marius-patrik/Andromeda-data", "marius-patrik/darkfactory-data"],
    now: Date.parse("2026-07-16T12:00:00Z"),
    async writeLedger(kind: string, target: string, payload: any) { ledgers.push({ kind, target, payload }); }
  });
  const repositories = await recoveryModule.listRecoveryRepositories();
  assert.deepEqual(repositories.map((entry: any) => entry.repository), ["marius-patrik/Andromeda", "marius-patrik/DarkFactory"]);
  await assert.rejects(
    recoveryModule.listRecoveryRepositories({ repository: "marius-patrik/Andromeda-data" }),
    /not an active code repository/
  );
  const result = await recoveryModule.recoverAutoreviews({ kind: "all", maxDispatches: 4, trigger: "test" });
  assert.equal(result.dispatched.length, 2);
  const dispatches = calls.filter((call) => call.method === "POST" && call.path.endsWith("/dispatches"));
  assert.deepEqual(dispatches.map((call) => call.body.inputs.target_kind).sort(), ["issue", "pull_request"]);
  assert.ok(dispatches.some((call) => call.body.inputs.target_version === `${SHA_A}:${SHA_B}`));
  assert.ok(dispatches.some((call) => /^[0-9a-f]{64}$/.test(call.body.inputs.target_version)));
  assert.ok(ledgers.some((entry) => entry.kind === "autoreview-recovery-admission"));
  assert.ok(ledgers.some((entry) => entry.kind === "autoreview-recovery-completion"));
  assert.equal(calls.some((call) => /contents|git\/trees|tarball|zipball/.test(call.path)), false);
});

test("Autoreview recovery suppresses exact completed and fresh pending targets", async () => {
  const base = "d".repeat(40);
  const head = "e".repeat(40);
  const version = `${base}:${head}`;
  const trusted = { login: "darkfactory-agent[bot]", type: "Bot" };
  const pull = { number: 3, state: "open", draft: false, base: { sha: base }, head: { sha: head, repo: { full_name: "marius-patrik/DarkFactory" } } };
  const pendingPull = { ...pull, number: 4 };
  const gh = {
    async request(method: string, requestPath: string) {
      if (method === "GET" && requestPath.includes("/pulls?")) return [pull, pendingPull];
      if (method === "GET" && requestPath.includes("/issues/3/comments?")) return [{
        user: trusted,
        created_at: "2026-07-16T11:59:00Z",
        body: `<!-- darkfactory-autoreview -->\n<!-- darkfactory-autoreview-target version=${version} -->\nclean`
      }];
      if (method === "GET" && requestPath.includes("/issues/4/comments?")) return [{
        user: trusted,
        created_at: "2026-07-16T11:59:00Z",
        body: `<!-- darkfactory:clean-autoreview schema=1 kind=pull-request number=4 version=${version} status=pending -->\nadmitted`
      }];
      if (method === "GET" && requestPath.includes("/issues?")) return [];
      throw new Error(`unexpected ${method} ${requestPath}`);
    }
  };
  recoveryModule.configureAutoreviewRecoveryRuntime({
    gh,
    ledgerGh: gh,
    controlRepo: { owner: "marius-patrik", repo: "DarkFactory" },
    controlRevision: "f".repeat(40),
    controlMetadata: { archived: false, disabled: false },
    activeRepositories: [],
    dataRepositories: [],
    now: Date.parse("2026-07-16T12:00:00Z"),
    async writeLedger() {}
  });
  const result = await recoveryModule.recoverAutoreviews({ kind: "all", maxDispatches: 2, trigger: "test" });
  assert.equal(result.candidates, 0);
  assert.deepEqual(result.dispatched, []);
});

test("active recovery workflows bind trusted main, Agent OS, scoped tokens, exact plans, and no bypasses", async () => {
  const [recovery, clean, managed] = await Promise.all([
    readFile(path.join(root, ".github/workflows/df-autoreview-recovery.yml"), "utf8"),
    readFile(path.join(root, ".github/workflows/df-clean.yml"), "utf8"),
    readFile(path.join(root, ".darkfactory/managed-repository.json"), "utf8")
  ]);
  assert.match(recovery, /ref: \$\{\{ github\.sha \}\}/);
  assert.match(recovery, /bin\\agents\.ps1/);
  assert.match(recovery, /permission-actions: write/);
  assert.match(recovery, /repositories: darkfactory-data[\s\S]*permission-contents: write/);
  assert.match(recovery, /df-autoreview-recovery\.mjs/);
  assert.doesNotMatch(recovery, /checkout[^\n]*pull_request|github\.event\.pull_request\.head/);

  assert.match(clean, /cron: "53 \*\/4 \* \* \*"/);
  assert.match(clean, /packages run darkfactory -- clean plan/);
  assert.match(clean, /packages run darkfactory -- clean apply \$planId --local \$target --watch --json/);
  assert.match(clean, /packages run darkfactory -- clean verify/);
  assert.match(clean, /DF_CONTROL_REVISION: \$\{\{ needs\.discover\.outputs\.control_revision \}\}/);
  assert.match(clean, /Materialize exact local target without executing it/);
  assert.doesNotMatch(`${recovery}\n${clean}`, /--force|--bypass|reset --hard|checkout --force/);

  const manifest = JSON.parse(managed);
  for (const required of [
    ".github/workflows/df-autoreview-recovery.yml",
    ".github/workflows/df-clean.yml",
    ".github/scripts/df-autoreview-recovery.mjs"
  ]) {
    assert.ok(manifest.packageFiles.includes(required), `${required} is package-owned`);
    assert.ok(manifest.requiredFiles.includes(required), `${required} is required`);
  }
});
