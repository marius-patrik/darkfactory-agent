#!/usr/bin/env node
import "dotenv/config";

import { App } from "@octokit/app";
import { Octokit } from "@octokit/core";
import { fileURLToPath, pathToFileURL } from "node:url";
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

  if (command === "doctor") {
    await runDoctor(args.slice(1));
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

export type DoctorCliOptions = {
  all: boolean;
  target: string;
  json: boolean;
  writeIssues: boolean;
  localPath: string;
  agentsHome: string;
};

export function parseDoctorCliArgs(args: string[]): DoctorCliOptions {
  const options: DoctorCliOptions = {
    all: false,
    target: `${CONTROL_OWNER}/${CONTROL_REPO}`,
    json: false,
    writeIssues: false,
    localPath: "",
    agentsHome: process.env.AGENTS_HOME?.trim() || ""
  };
  let explicitTarget = false;

  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === "--all") {
      options.all = true;
      continue;
    }
    if (argument === "--json") {
      options.json = true;
      continue;
    }
    if (argument === "--write-issues") {
      options.writeIssues = true;
      continue;
    }
    if (argument === "--repair") {
      throw new Error("doctor repair is intentionally unavailable; diagnose first and use a separately reviewed repair lane");
    }
    if (argument === "--local" || argument === "--agents-home") {
      const value = args[index + 1]?.trim();
      if (!value || value.startsWith("--")) throw new Error(`${argument} requires a path`);
      if (argument === "--local") options.localPath = value;
      else options.agentsHome = value;
      index += 1;
      continue;
    }
    if (argument.startsWith("-")) throw new Error(`unknown doctor option: ${argument}`);
    if (explicitTarget) throw new Error("doctor accepts at most one owner/repo target");
    if (!/^[^/\s]+\/[^/\s]+$/.test(argument)) throw new Error(`invalid doctor repository: ${argument}`);
    options.target = argument;
    explicitTarget = true;
  }

  if (options.all && explicitTarget) throw new Error("doctor --all cannot be combined with an owner/repo target");
  if (options.all && options.localPath) throw new Error("doctor --all cannot inspect one ambiguous --local checkout");
  return options;
}

async function runDoctor(args: string[]): Promise<void> {
  const options = parseDoctorCliArgs(args);
  const credentials = loadAppCredentials();
  const app = new App({ appId: credentials.appId, privateKey: credentials.privateKey });
  const owner = options.all ? CONTROL_OWNER : options.target.split("/", 1)[0];
  const octokit = await getScopedInstallationOctokit(app, owner, {
    administration: "read",
    actions: "read",
    checks: "read",
    contents: "read",
    issues: options.writeIssues ? "write" : "read",
    pull_requests: "read",
    secrets: "read",
    statuses: "read"
  });
  const github = createDoctorRequester(octokit);
  const ledgerGithub = options.writeIssues
    ? createDoctorRequester(await getScopedInstallationOctokit(app, CONTROL_OWNER, { contents: "write" }, ["darkfactory-data"]))
    : undefined;
  const moduleUrl = new URL("../.github/scripts/df-audit.mjs", import.meta.url);
  const doctor = await import(moduleUrl.href) as {
    runRepositoryDoctor: (github: unknown, options: Record<string, unknown>) => Promise<unknown[]>;
    formatDoctorReports: (reports: unknown[]) => string;
  };
  const packageRoot = fileURLToPath(new URL("..", import.meta.url));
  const reports = await doctor.runRepositoryDoctor(github, {
    root: packageRoot,
    controlRepo: { owner: CONTROL_OWNER, repo: CONTROL_REPO },
    target: options.target,
    all: options.all,
    mode: options.writeIssues ? "report" : "diagnose",
    ledgerGithub,
    trigger: "cli",
    localPath: options.localPath,
    agentsHome: options.agentsHome
  });

  if (options.json) console.log(JSON.stringify(reports, null, 2));
  else console.log(doctor.formatDoctorReports(reports));
}

async function getInstallationOctokit(app: App, owner: string): Promise<Octokit> {
  return app.getInstallationOctokit(await getInstallationId(app, owner));
}

async function getScopedInstallationOctokit(
  app: App,
  owner: string,
  permissions: Record<string, "read" | "write">,
  repositoryNames?: string[]
): Promise<Octokit> {
  const installationId = await getInstallationId(app, owner);
  const authentication = await app.octokit.auth({
    type: "installation",
    installationId,
    permissions,
    ...(repositoryNames ? { repositoryNames } : {})
  }) as unknown;
  if (!isRecord(authentication) || typeof authentication.token !== "string" || !authentication.token) {
    throw new Error("GitHub returned an invalid scoped installation authentication response");
  }
  return new Octokit({ auth: authentication.token });
}

async function getInstallationId(app: App, owner: string): Promise<number> {
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

  return installation.id;
}

function createOctokitRequester(octokit: Octokit): GitHubRequester {
  return {
    async request(route, parameters) {
      const response = await octokit.request(route, parameters);
      return { data: response.data };
    }
  };
}

function createDoctorRequester(octokit: Octokit): {
  request(method: string, path: string, body?: unknown): Promise<unknown>;
  graphql(query: string, variables?: Record<string, unknown>): Promise<unknown>;
} {
  return {
    async request(method, path, body) {
      const response = await octokit.request(`${method} ${path}`, body as Record<string, unknown> | undefined);
      return response.data;
    },
    async graphql(query, variables) {
      return await octokit.graphql(query, variables);
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
  darkfactory doctor [owner/repo | --all] [--json] [--local PATH] [--agents-home PATH]
  darkfactory doctor [owner/repo | --all] --write-issues [--json]

Command information:
  serve         Run the GitHub App webhook server.
  install-url   Print the GitHub App installation URL.
  sync-managed  Reconcile managed setup PRs for installed repositories.
  status        Read DarkFactory orchestration and backlog status.
  doctor        Diagnose deterministic repository, workflow, branch, issue, and local-state drift.

Doctor safety:
  Diagnose mode is the default and performs no writes or repairs.
  --write-issues explicitly enables stable per-finding issue reconciliation and the doctor ledger.
  Repair is intentionally a separate reviewed work lane; --repair is rejected.
Secrets are read from environment variables first, then AGENTS_SECRETS/*.secret.`);
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  runCli().catch((error) => {
    console.error(`darkfactory: ${error.message}`);
    process.exitCode = 1;
  });
}
