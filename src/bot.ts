import { App } from "@octokit/app";

import {
  checkRepositorySetup,
  formatRepositorySetupComment,
  type GitHubRequester
} from "./repository-setup.js";

export interface BotOptions {
  appId: string;
  privateKey: string;
  webhookSecret: string;
}

interface PullRequestPayload {
  repository: {
    name: string;
    owner: {
      login: string;
    };
  };
  pull_request: {
    number: number;
    head: {
      sha: string;
      repo: {
        name: string;
        owner: {
          login: string;
        } | null;
      } | null;
    };
  };
}

export function createBot(options: BotOptions): App {
  const app = new App({
    appId: options.appId,
    privateKey: options.privateKey,
    webhooks: {
      secret: options.webhookSecret
    }
  });

  app.webhooks.on("ping", ({ payload }) => {
    console.log(`Received ping for ${payload.repository?.full_name ?? "unknown repository"}`);
  });

  app.webhooks.on("issues.opened", async ({ octokit, payload }) => {
    await octokit.request("POST /repos/{owner}/{repo}/issues/{issue_number}/comments", {
      owner: payload.repository.owner.login,
      repo: payload.repository.name,
      issue_number: payload.issue.number,
      body: "Thanks for opening this issue. I am online and ready to help."
    });
  });

  app.webhooks.on("pull_request.opened", async ({ octokit, payload }) => {
    await octokit.request("POST /repos/{owner}/{repo}/issues/{issue_number}/comments", {
      owner: payload.repository.owner.login,
      repo: payload.repository.name,
      issue_number: payload.pull_request.number,
      body: "Thanks for opening this pull request. I will take a look."
    });

    await enforceRepositorySetup(octokit, payload);
  });

  app.webhooks.on("pull_request.reopened", async ({ octokit, payload }) => {
    await enforceRepositorySetup(octokit, payload);
  });

  app.webhooks.on("pull_request.synchronize", async ({ octokit, payload }) => {
    await enforceRepositorySetup(octokit, payload);
  });

  app.webhooks.on("pull_request.ready_for_review", async ({ octokit, payload }) => {
    await enforceRepositorySetup(octokit, payload);
  });

  return app;
}

async function enforceRepositorySetup(
  octokit: GitHubRequester,
  payload: PullRequestPayload
): Promise<void> {
  const headRepository = payload.pull_request.head.repo;
  const report = await checkRepositorySetup(octokit, {
    owner: headRepository?.owner?.login ?? payload.repository.owner.login,
    repo: headRepository?.name ?? payload.repository.name,
    ref: payload.pull_request.head.sha
  });
  const body = formatRepositorySetupComment(report);

  if (!body) {
    return;
  }

  await octokit.request("POST /repos/{owner}/{repo}/issues/{issue_number}/comments", {
    owner: payload.repository.owner.login,
    repo: payload.repository.name,
    issue_number: payload.pull_request.number,
    body
  });
}
