#!/usr/bin/env node
import "dotenv/config";

import { App } from "@octokit/app";
import { Octokit } from "@octokit/core";
import { createBot } from "./bot.js";
import { loadAppCredentials, loadConfig } from "./config.js";
import { ensureManagedRepositorySetup, orderManagedRepositoriesForSync } from "./managed-sync.js";
import {
  CONTROL_OWNER,
  CONTROL_REPO,
  buildStatusReport,
  formatStatusReport,
  type GitHubRequester
} from "./status.js";
import { createWebhookServer } from "./server.js";

export async function runCli(args = process.argv.slice(2)): Promise<void> {
  const [command = "help"] = args;

  if (command === "help" || command === "--help" || command === "-h") {
    printHelp();
    return;
  }

  if (command === "serve") {
    serve();
    return;
  }

  if (command === "install-url") {
    await printInstallationUrl();
    return;
  }

  if (command === "sync-managed") {
    await syncManagedRepositories();
    return;
  }

  if (command === "status") {
    await runStatus(args.slice(1));
    return;
  }

  throw new Error(`unknown command: ${command}`);
}

function serve(): void {
  const config = loadConfig();
  const app = createBot({
    appId: config.appId,
    privateKey: config.privateKey,
    webhookSecret: config.webhookSecret,
    controlRepo: config.controlRepo
  });
  const server = createWebhookServer(app.webhooks);

  server.listen(config.port, () => {
    console.log(`DarkFactory listening on http://localhost:${config.port}/webhook`);
  });
}

async function printInstallationUrl(): Promise<void> {
  const credentials = loadAppCredentials();
  const app = new App({
    appId: credentials.appId,
    privateKey: credentials.privateKey
  });

  console.log(await app.getInstallationUrl());
}

async function syncManagedRepositories(): Promise<void> {
  const credentials = loadAppCredentials();
  const app = new App({
    appId: credentials.appId,
    privateKey: credentials.privateKey
  });
  const repositories: Array<{
    octokit: Parameters<typeof ensureManagedRepositorySetup>[0];
    repository: {
      owner: { login: string };
      name: string;
      default_branch?: string;
      archived?: boolean;
    };
  }> = [];
  let count = 0;

  for await (const { octokit, repository } of app.eachRepository.iterator()) {
    repositories.push({ octokit, repository });
  }

  for (const { octokit, repository } of orderManagedRepositoriesForSync(repositories, ({ repository }) => ({
    owner: repository.owner.login,
    repo: repository.name
  }))) {
    const result = await ensureManagedRepositorySetup(octokit, {
      owner: repository.owner.login,
      repo: repository.name,
      defaultBranch: repository.default_branch,
      archived: repository.archived
    });

    count += 1;
    console.log(
      `${result.owner}/${result.repo}: ${result.status}${
        result.pullRequestUrl ? ` ${result.pullRequestUrl}` : ""
      }`
    );
  }

  console.log(`Processed ${count} installed repositories.`);
}

async function runStatus(args: string[]): Promise<void> {
  const json = args.includes("--json");
  const credentials = loadAppCredentials();
  const app = new App({
    appId: credentials.appId,
    privateKey: credentials.privateKey
  });
  const octokit = await getInstallationOctokit(app, CONTROL_OWNER);
  const requester = createOctokitRequester(octokit);
  const report = await buildStatusReport(requester);

  if (json) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    console.log(formatStatusReport(report));
  }
}

async function getInstallationOctokit(app: App, owner: string): Promise<Octokit> {
  const { data } = await app.octokit.request("GET /app/installations");

  if (!Array.isArray(data)) {
    throw new Error("GitHub returned an invalid app installations response");
  }

  const installation = data.find(
    (item) =>
      isRecord(item) &&
      isRecord(item.account) &&
      typeof item.account.login === "string" &&
      item.account.login.toLowerCase() === owner.toLowerCase()
  );

  if (!installation || !isRecord(installation) || typeof installation.id !== "number") {
    throw new Error(`GitHub App is not installed for owner ${owner}`);
  }

  return app.getInstallationOctokit(installation.id);
}

function createOctokitRequester(octokit: Octokit): GitHubRequester {
  return {
    async request(route, parameters) {
      const response = await octokit.request(route, parameters);
      return { data: response.data };
    }
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function printHelp(): void {
  console.log(`darkfactory - DarkFactory GitHub agent

Usage:
  darkfactory serve
  darkfactory install-url
  darkfactory sync-managed
  darkfactory status [--json]
Secrets are read from environment variables first, then AGENTS_SECRETS/*.secret.`);
}

runCli().catch((error) => {
  console.error(`darkfactory: ${error.message}`);
  process.exitCode = 1;
});
