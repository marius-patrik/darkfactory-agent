import { App } from "@octokit/app";

import {
  checkRepositorySetup,
  formatRepositorySetupComment,
  type GitHubRequester
} from "./repository-setup.js";
import { ensureManagedRepositorySetup } from "./managed-sync.js";

export interface BotOptions {
  appId: string;
  privateKey: string;
  webhookSecret: string;
}

interface PullRequestPayload {
  repository: {
    name: string;
    full_name?: string;
    owner: {
      login: string;
    };
  };
  pull_request: {
    number: number;
    title?: string;
    body?: string | null;
    html_url?: string;
    merged?: boolean | null;
    user?: {
      login?: string;
    } | null;
    base?: {
      ref?: string;
    };
    head: {
      ref?: string;
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

interface InstallationRepository {
  full_name?: string;
  name?: string;
  owner?: {
    login?: string;
  } | null;
  default_branch?: string;
  archived?: boolean;
}

interface InstallationPayload {
  repositories?: InstallationRepository[];
}

interface InstallationRepositoriesPayload {
  repositories_added?: InstallationRepository[];
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

  app.webhooks.on("pull_request.closed", async ({ octokit, payload }) => {
    await closeDevMergeIssues(octokit, payload);
  });

  app.webhooks.on("installation.created", async ({ octokit, payload }) => {
    await syncInstalledRepositories(octokit, payload);
  });

  app.webhooks.on("installation_repositories.added", async ({ octokit, payload }) => {
    await syncAddedRepositories(octokit, payload);
  });

  return app;
}

export async function closeDevMergeIssues(
  octokit: GitHubRequester,
  payload: PullRequestPayload
): Promise<number[]> {
  const pull = payload.pull_request;

  if (pull.merged !== true || pull.base?.ref !== "dev" || !isWorkerPullRequest(payload)) {
    return [];
  }

  const repositoryName = payload.repository.full_name ?? `${payload.repository.owner.login}/${payload.repository.name}`;
  const issueNumbers = extractClosingIssueNumbers(pull.body ?? "", repositoryName);
  const closed: number[] = [];

  for (const issueNumber of issueNumbers) {
    const pullUrl = pull.html_url ?? `#${pull.number}`;
    if (await hasDevMergeComment(octokit, payload, issueNumber, pullUrl)) {
      continue;
    }

    await octokit.request("POST /repos/{owner}/{repo}/issues/{issue_number}/comments", {
      owner: payload.repository.owner.login,
      repo: payload.repository.name,
      issue_number: issueNumber,
      body: `merged to dev in ${pullUrl}; releases with the next dev→main PR`
    });
    await octokit.request("PATCH /repos/{owner}/{repo}/issues/{issue_number}", {
      owner: payload.repository.owner.login,
      repo: payload.repository.name,
      issue_number: issueNumber,
      state: "closed"
    });
    closed.push(issueNumber);
  }

  return closed;
}

async function hasDevMergeComment(
  octokit: GitHubRequester,
  payload: PullRequestPayload,
  issueNumber: number,
  pullUrl: string
): Promise<boolean> {
  const response = await octokit.request("GET /repos/{owner}/{repo}/issues/{issue_number}/comments", {
    owner: payload.repository.owner.login,
    repo: payload.repository.name,
    issue_number: issueNumber,
    per_page: 100
  });
  const comments = Array.isArray(response.data) ? response.data : [];

  return comments.some((comment) => {
    return typeof comment === "object" &&
      comment !== null &&
      "body" in comment &&
      typeof comment.body === "string" &&
      comment.body.includes(`merged to dev in ${pullUrl}`);
  });
}

function isWorkerPullRequest(payload: PullRequestPayload): boolean {
  const pull = payload.pull_request;
  const author = pull.user?.login ?? "";
  const headRepo = pull.head.repo;
  const expectedHeadOwner = payload.repository.owner.login;
  const expectedHeadRepo = payload.repository.name;

  return String(pull.head.ref ?? "").startsWith("df/") &&
    headRepo?.name === expectedHeadRepo &&
    headRepo?.owner?.login === expectedHeadOwner &&
    /\b(?:github-actions\[bot\]|mp-agents\[bot\]|app\/darkfactory-agent|darkfactory-agent)\b/.test(author) &&
    /<!--\s*dark-factory:worker-pr\s+issue=\d+\s*-->/.test(pull.body ?? "") &&
    extractClosingIssueNumbers(pull.body ?? "", payload.repository.full_name ?? `${expectedHeadOwner}/${expectedHeadRepo}`).length > 0;
}

function extractClosingIssueNumbers(body: string, repositoryName: string): number[] {
  const refs = new Set<number>();
  const expectedRepo = repositoryName.toLowerCase();
  const matches = body.matchAll(/\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+(?:([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+)#|#)(\d+)\b/gi);

  for (const match of matches) {
    const qualifiedRepo = match[1]?.toLowerCase() ?? "";
    if (qualifiedRepo && qualifiedRepo !== expectedRepo) continue;
    refs.add(Number(match[2]));
  }

  return [...refs].filter((number) => Number.isInteger(number) && number > 0);
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

async function syncInstalledRepositories(
  octokit: GitHubRequester,
  payload: InstallationPayload
): Promise<void> {
  await syncRepositories(octokit, payload.repositories ?? []);
}

async function syncAddedRepositories(
  octokit: GitHubRequester,
  payload: InstallationRepositoriesPayload
): Promise<void> {
  await syncRepositories(octokit, payload.repositories_added ?? []);
}

async function syncRepositories(
  octokit: GitHubRequester,
  repositories: InstallationRepository[]
): Promise<void> {
  for (const repository of repositories) {
    const parsed = parseRepository(repository);

    if (!parsed) {
      continue;
    }

    try {
      const result = await ensureManagedRepositorySetup(octokit, parsed);
      console.log(
        `Managed setup ${result.status} for ${result.owner}/${result.repo}${
          result.pullRequestUrl ? `: ${result.pullRequestUrl}` : ""
        }`
      );
    } catch (error) {
      console.error(`Failed to sync managed setup for ${parsed.owner}/${parsed.repo}`, error);
    }
  }
}

function parseRepository(repository: InstallationRepository) {
  if (repository.owner?.login && repository.name) {
    return {
      owner: repository.owner.login,
      repo: repository.name,
      defaultBranch: repository.default_branch,
      archived: repository.archived
    };
  }

  if (repository.full_name) {
    const [owner, repo] = repository.full_name.split("/");

    if (owner && repo) {
      return {
        owner,
        repo,
        defaultBranch: repository.default_branch,
        archived: repository.archived
      };
    }
  }

  return null;
}
