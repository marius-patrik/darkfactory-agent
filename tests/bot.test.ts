import test from "node:test";
import assert from "node:assert/strict";

import { closeDevMergeIssues } from "../src/bot.js";

test("closeDevMergeIssues closes referenced issues for merged dev worker PRs", async () => {
  const calls: Array<{ route: string; parameters: Record<string, unknown> }> = [];

  const closed = await closeDevMergeIssues(
    {
      async request(route, parameters) {
        calls.push({ route, parameters });
        if (route === "GET /repos/{owner}/{repo}/issues/{issue_number}/comments") {
          return { data: [] };
        }
        return { data: {} };
      }
    },
    workerClosedPayload()
  );

  assert.deepEqual(closed, [23]);
  assert.ok(calls.some((call) => {
    return call.route === "POST /repos/{owner}/{repo}/issues/{issue_number}/comments" &&
      call.parameters.issue_number === 23 &&
      call.parameters.body === "merged to dev in https://github.com/marius-patrik/example/pull/7; releases with the next dev→main PR";
  }));
  assert.ok(calls.some((call) => {
    return call.route === "PATCH /repos/{owner}/{repo}/issues/{issue_number}" &&
      call.parameters.issue_number === 23 &&
      call.parameters.state === "closed";
  }));
});

test("closeDevMergeIssues skips non-worker and already-commented dev merges", async () => {
  const nonWorkerClosed = await closeDevMergeIssues(
    {
      async request() {
        throw new Error("non-worker PR should not call GitHub");
      }
    },
    workerClosedPayload({
      body: "Closes #23",
      user: { login: "marius-patrik" }
    })
  );

  assert.deepEqual(nonWorkerClosed, []);

  const calls: Array<{ route: string; parameters: Record<string, unknown> }> = [];
  const alreadyCommentedClosed = await closeDevMergeIssues(
    {
      async request(route, parameters) {
        calls.push({ route, parameters });
        return {
          data: [
            {
              body: "merged to dev in https://github.com/marius-patrik/example/pull/7; releases with the next dev→main PR"
            }
          ]
        };
      }
    },
    workerClosedPayload()
  );

  assert.deepEqual(alreadyCommentedClosed, []);
  assert.equal(calls.length, 1);
  assert.equal(calls[0]?.route, "GET /repos/{owner}/{repo}/issues/{issue_number}/comments");
});

function workerClosedPayload(overrides: Record<string, unknown> = {}) {
  return {
    repository: {
      name: "example",
      full_name: "marius-patrik/example",
      owner: { login: "marius-patrik" }
    },
    pull_request: {
      number: 7,
      title: "Implement issue #23",
      body: "<!-- dark-factory:worker-pr issue=23 -->\n\nCloses #23",
      html_url: "https://github.com/marius-patrik/example/pull/7",
      merged: true,
      user: { login: "app/darkfactory-agent" },
      base: { ref: "dev" },
      head: {
        ref: "df/23-implement",
        sha: "abc123",
        repo: {
          name: "example",
          owner: { login: "marius-patrik" }
        }
      },
      ...overrides
    }
  };
}
