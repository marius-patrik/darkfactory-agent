import assert from "node:assert/strict";
import test from "node:test";

test("orchestrator dispatches open df:ready issues in active managed repos", async () => {
  // @ts-ignore Script helpers are native ESM workflow files, not built TypeScript modules.
  const { orchestrate } = await import("../.github/scripts/df-orchestrate.mjs?unit=df-orchestrate-test");
  const calls: Array<{ method: string; path: string; body?: unknown }> = [];
  const notFound = Object.assign(new Error("not found"), { status: 404 });

  const gh = {
    async graphql() {
      return {
        repository: {
          pullRequests: {
            pageInfo: { hasNextPage: false, endCursor: null },
            nodes: []
          }
        }
      };
    },
    async request(method: string, path: string, body?: unknown) {
      calls.push({ method, path, body });

      if (method === "GET" && path === "/repos/marius-patrik/example/issues?state=open&per_page=100&page=1") {
        return [
          {
            number: 42,
            title: "Ready work",
            body: "Directly queued issue without a PRD marker.",
            labels: [{ name: "df:ready" }]
          }
        ];
      }
      if (method === "GET" && path === "/repos/marius-patrik/example/issues?state=open&per_page=100&page=2") {
        return [];
      }
      if (method === "GET" && path === "/repos/marius-patrik/example/contents/PRD.md") {
        return {
          type: "file",
          encoding: "base64",
          content: Buffer.from("# PRD\n", "utf8").toString("base64")
        };
      }
      if (method === "GET" && path === "/repos/marius-patrik/example") {
        return { default_branch: "main", allow_auto_merge: true };
      }
      if (method === "GET" && path === "/repos/marius-patrik/example/git/ref/heads/dev") {
        throw notFound;
      }
      if (method === "GET" && path === "/repos/marius-patrik/example/branches/main/protection") {
        throw notFound;
      }
      if (method === "POST" && path === "/repos/marius-patrik/example/issues/42/labels") {
        return {};
      }
      if (method === "DELETE" && path === "/repos/marius-patrik/example/issues/42/labels/df%3Aready") {
        return null;
      }
      if (method === "POST" && path === "/repos/marius-patrik/agent-darkfactory/actions/workflows/df-work.yml/dispatches") {
        return null;
      }

      throw new Error(`Unexpected GitHub request: ${method} ${path}`);
    }
  };

  const result = await orchestrate({
    gh,
    controlRepo: { owner: "marius-patrik", repo: "agent-darkfactory" },
    registry: { repositories: { "marius-patrik/example": { state: "active" } } },
    repositories: [{ full_name: "marius-patrik/example", archived: false, disabled: false }],
    readCi: false,
    readLedgers: false,
    writeLedger: false,
    writeDashboard: false,
    warn: () => {},
    log: () => {}
  });

  assert.deepEqual(result.dispatched, [{ repo: "marius-patrik/example", issue: 42, priority: "P3", stream: "default" }]);
  assert.deepEqual(
    calls.find((call) => call.method === "POST" && call.path === "/repos/marius-patrik/example/issues/42/labels")?.body,
    { labels: ["df:running"] }
  );
  assert.ok(calls.some((call) => call.method === "DELETE" && call.path === "/repos/marius-patrik/example/issues/42/labels/df%3Aready"));
  assert.deepEqual(
    calls.find((call) => call.method === "POST" && call.path.endsWith("/actions/workflows/df-work.yml/dispatches"))?.body,
    { ref: "main", inputs: { repo: "marius-patrik/example", issue_number: "42" } }
  );
});

test("orchestrator does not dispatch issues that already have an open worker PR", async () => {
  // @ts-ignore Script helpers are native ESM workflow files, not built TypeScript modules.
  const { orchestrate } = await import("../.github/scripts/df-orchestrate.mjs?unit=df-orchestrate-existing-pr-test");
  const calls: Array<{ method: string; path: string; body?: unknown }> = [];

  const gh = {
    async graphql() {
      return {
        repository: {
          pullRequests: {
            pageInfo: { hasNextPage: false, endCursor: null },
            nodes: [
              {
                id: "PR_21",
                number: 21,
                title: "Worker PR",
                body: "<!-- dark-factory:worker-pr issue=8 -->\n\nCloses #8",
                url: "https://github.com/marius-patrik/example/pull/21",
                headRefName: "df/8-worker",
                baseRefName: "main",
                headRepository: { owner: { login: "marius-patrik" }, name: "example" },
                author: { login: "mp-agents[bot]" }
              }
            ]
          }
        }
      };
    },
    async request(method: string, path: string, body?: unknown) {
      calls.push({ method, path, body });

      if (method === "GET" && path === "/repos/marius-patrik/example/issues?state=open&per_page=100&page=1") {
        return [{ number: 8, title: "Existing PR", body: "", labels: [{ name: "df:ready" }] }];
      }
      if (method === "GET" && path === "/repos/marius-patrik/example/issues?state=open&per_page=100&page=2") {
        return [];
      }
      if (method === "GET" && path === "/repos/marius-patrik/example/contents/PRD.md") {
        return {
          type: "file",
          encoding: "base64",
          content: Buffer.from("# PRD\n", "utf8").toString("base64")
        };
      }
      if (method === "GET" && path === "/repos/marius-patrik/example") {
        return { default_branch: "main", allow_auto_merge: true };
      }
      if (method === "POST" && path === "/repos/marius-patrik/example/issues/8/labels") return {};
      if (method === "DELETE" && path === "/repos/marius-patrik/example/issues/8/labels/df%3Aready") return {};

      throw new Error(`Unexpected GitHub request: ${method} ${path}`);
    }
  };

  const result = await orchestrate({
    gh,
    controlRepo: { owner: "marius-patrik", repo: "agent-darkfactory" },
    registry: { repositories: { "marius-patrik/example": { state: "active" } } },
    repositories: [{ full_name: "marius-patrik/example", archived: false, disabled: false }],
    readCi: false,
    readLedgers: false,
    writeLedger: false,
    writeDashboard: false,
    warn: () => {},
    log: () => {}
  });

  assert.deepEqual(result.dispatched, []);
  assert.equal(calls.some((call) => call.path.endsWith("/actions/workflows/df-work.yml/dispatches")), false);
  assert.deepEqual(
    calls.find((call) => call.method === "POST" && call.path === "/repos/marius-patrik/example/issues/8/labels")?.body,
    { labels: ["df:running"] }
  );
  assert.ok(calls.some((call) => call.method === "DELETE" && call.path === "/repos/marius-patrik/example/issues/8/labels/df%3Aready"));
});

test("orchestrator plan respects sequencing and concurrency caps", async () => {
  // @ts-ignore Script helpers are native ESM workflow files, not built TypeScript modules.
  const { planOrchestratorWave } = await import("../.github/scripts/df-orchestrate.mjs?unit=df-orchestrate-plan-test");
  const repository = { owner: "marius-patrik", repo: "example" };
  const state = {
    repository,
    repo: "marius-patrik/example",
    defaultBranch: "main",
    prdState: { present: true, path: "PRD.md" },
    openIssueNumbers: new Set([1, 2, 3, 4]),
    counts: { ready: 3, running: 1, blocked: 0, askOwner: 0, done: 0 },
    issues: [
      issue(repository, 1, ["df:running", "stream:maintenance"]),
      issue(repository, 2, ["df:ready", "P1", "stream:core"]),
      issue(repository, 3, ["df:ready", "P0", "stream:ops"], "## Sequencing\n\nBlocked-by: #2"),
      issue(repository, 4, ["roadmap", "P2", "df:class:standard"], "## Sequencing\n\nBlocked-by: #99")
    ]
  };

  const plan = planOrchestratorWave([state], { global: 3, perRepo: 2, perStream: 1 });

  assert.deepEqual(
    plan.dispatches.map((dispatch: { issue: number; stream: string }) => ({ issue: dispatch.issue, stream: dispatch.stream })),
    [{ issue: 2, stream: "core" }]
  );
  assert.ok(plan.labelActions.some((action: { action: string; issue: number; remove: string[] }) => {
    return action.action === "hold-sequenced-issue" && action.issue === 3 && action.remove.includes("df:ready");
  }));
  assert.ok(plan.labelActions.some((action: { action: string; issue: number; add: string[] }) => {
    return action.action === "ready-unblocked-issue" && action.issue === 4 && action.add.includes("df:ready");
  }));
});

test("orchestrator escalates owner-only blocked issues", async () => {
  // @ts-ignore Script helpers are native ESM workflow files, not built TypeScript modules.
  const { planOrchestratorWave } = await import("../.github/scripts/df-orchestrate.mjs?unit=df-orchestrate-owner-test");
  const repository = { owner: "marius-patrik", repo: "example" };
  const state = {
    repository,
    repo: "marius-patrik/example",
    defaultBranch: "main",
    prdState: { present: true, path: "PRD.md" },
    openIssueNumbers: new Set([10]),
    counts: { ready: 0, running: 0, blocked: 1, askOwner: 0, done: 0 },
    issues: [
      {
        ...issue(repository, 10, ["df:blocked", "P1"]),
        history: "DarkFactory blocked this issue before worker dispatch.\nEnable repository auto-merge or open managed setup work."
      }
    ]
  };

  const plan = planOrchestratorWave([state]);

  assert.deepEqual(
    plan.ownerEscalations.map((action: { repo: string; issue: number; action: string }) => ({
      repo: action.repo,
      issue: action.issue,
      action: action.action
    })),
    [{ repo: "marius-patrik/example", issue: 10, action: "escalate-owner-question" }]
  );
  assert.match(plan.ownerEscalations[0].reason, /auto-merge/i);
});

test("orchestrator posts a dashboard digest issue", async () => {
  // @ts-ignore Script helpers are native ESM workflow files, not built TypeScript modules.
  const { orchestrate } = await import("../.github/scripts/df-orchestrate.mjs?unit=df-orchestrate-dashboard-test");
  const calls: Array<{ method: string; path: string; body?: any }> = [];

  const gh = {
    async request(method: string, path: string, body?: any) {
      calls.push({ method, path, body });

      if (method === "POST" && path === "/repos/marius-patrik/agent-darkfactory/labels") return {};
      if (method === "GET" && path === "/repos/marius-patrik/agent-darkfactory/issues?state=all&per_page=100&page=1") return [];
      if (method === "POST" && path === "/repos/marius-patrik/agent-darkfactory/issues") {
        return { number: 99, html_url: "https://github.com/marius-patrik/agent-darkfactory/issues/99" };
      }

      throw new Error(`Unexpected GitHub request: ${method} ${path}`);
    }
  };

  const result = await orchestrate({
    gh,
    controlRepo: { owner: "marius-patrik", repo: "agent-darkfactory" },
    registry: { repositories: {} },
    repositories: [],
    readCi: false,
    readLedgers: false,
    writeLedger: false,
    warn: () => {},
    log: () => {}
  });

  const create = calls.find((call) => call.method === "POST" && call.path === "/repos/marius-patrik/agent-darkfactory/issues");
  assert.equal(create?.body.title, "DarkFactory Orchestrator Dashboard");
  assert.match(create?.body.body, /dark-factory:l0-dashboard/);
  assert.match(create?.body.body, /DarkFactory L0 Orchestrator Digest/);
  assert.equal(result.ledger.dashboard.number, 99);
});

function issue(repository: { owner: string; repo: string }, number: number, labels: string[], body = "") {
  const names = new Set(labels);
  const priority = labels.find((label) => /^P\d$/.test(label)) || "P3";
  const stream = labels.find((label) => label.startsWith("stream:"))?.slice("stream:".length) || "default";
  return {
    repository,
    repo: `${repository.owner}/${repository.repo}`,
    number,
    title: `Issue ${number}`,
    body,
    labels: names,
    priority,
    priorityRank: priority === "P0" ? 0 : priority === "P1" ? 1 : priority === "P2" ? 2 : 3,
    stream,
    blockedBy: [...body.matchAll(/^Blocked-by:\s*#(\d+)/gim)].map((match) => Number(match[1]))
  };
}
