import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

import {
  RUNNER_LABEL,
  RunnerManager,
  commandForLog,
  mapRunnerListResponse,
  parseRunnerCommand,
  readRunnerState,
  runnerDirectory,
  runnerNameFor,
  runnerStatePath,
  selectWindowsX64Asset,
  type CommandRunner,
  type Downloader,
  type GitHubRunnerClient,
  type RepositoryRef
} from "../src/runners.js";

test("parseRunnerCommand accepts runner actions and root option", () => {
  assert.deepEqual(parseRunnerCommand(["setup", "marius-patrik/dream", "--root", "C:/df-runners"]), {
    action: "setup",
    repository: { owner: "marius-patrik", repo: "dream" },
    root: "C:/df-runners"
  });

  assert.deepEqual(parseRunnerCommand(["status"]), {
    action: "status",
    repository: undefined,
    root: undefined
  });

  assert.throws(() => parseRunnerCommand(["remove"]), /requires <owner\/repo>/);
  assert.throws(() => parseRunnerCommand(["setup", "not-a-ref"]), /owner\/repo/);
});

test("mapRunnerListResponse normalizes GitHub runner API shape", () => {
  const runners = mapRunnerListResponse({
    total_count: 1,
    runners: [
      {
        id: 10,
        name: "df-dream",
        os: "Windows",
        status: "online",
        busy: false,
        labels: [{ name: "self-hosted" }, { name: RUNNER_LABEL }]
      }
    ]
  });

  assert.deepEqual(runners, [
    {
      id: 10,
      name: "df-dream",
      os: "Windows",
      status: "online",
      busy: false,
      labels: ["self-hosted", RUNNER_LABEL]
    }
  ]);
});

test("selectWindowsX64Asset picks the latest runner Windows x64 zip", () => {
  assert.deepEqual(
    selectWindowsX64Asset({
      tag_name: "v2.999.0",
      assets: [
        {
          name: "actions-runner-linux-x64-2.999.0.tar.gz",
          browser_download_url: "https://example.invalid/linux"
        },
        {
          name: "actions-runner-win-x64-2.999.0.zip",
          browser_download_url: "https://example.invalid/windows"
        }
      ]
    }),
    {
      version: "v2.999.0",
      assetName: "actions-runner-win-x64-2.999.0.zip",
      downloadUrl: "https://example.invalid/windows"
    }
  );
});

test("RunnerManager setup writes state without tokens and start records a detached PID", async () => {
  const root = await mkdtemp(join(tmpdir(), "df-runners-"));
  const calls: Array<{ file: string; args: string[]; cwd?: string; redactions?: string[] }> = [];
  const repository: RepositoryRef = { owner: "marius-patrik", repo: "dream" };
  const github: GitHubRunnerClient = {
    async createRegistrationToken() {
      return { token: "registration-secret" };
    },
    async createRemovalToken() {
      return { token: "removal-secret" };
    },
    async listRunners() {
      return [
        {
          id: 1,
          name: runnerNameFor(repository),
          os: "Windows",
          status: "online",
          busy: false,
          labels: [RUNNER_LABEL]
        }
      ];
    },
    async getLatestWindowsX64RunnerRelease() {
      return {
        version: "v2.999.0",
        assetName: "actions-runner-win-x64-2.999.0.zip",
        downloadUrl: "https://example.invalid/actions-runner.zip"
      };
    }
  };
  const commands: CommandRunner = {
    async exec(file, args, options) {
      await materializeRunnerOnExpand(file, args, options?.cwd);
      calls.push({ file, args, cwd: options?.cwd, redactions: options?.redactions });
      return { stdout: "{}", stderr: "" };
    },
    spawnDetached(file, args, options) {
      calls.push({ file, args, cwd: options?.cwd });
      return { pid: 4242 };
    }
  };
  const downloader: Downloader = {
    async download(_url, destination) {
      await writeFile(destination, "zip");
    }
  };
  const manager = new RunnerManager({
    github,
    commands,
    downloader,
    now: () => new Date("2026-07-03T12:00:00.000Z")
  });

  try {
    const configured = await manager.setup(repository, { root });
    const started = await manager.start(repository, { root });
    const statuses = await manager.status(repository, { root });
    const stateText = await readFile(runnerStatePath(root), "utf8");
    const state = await readRunnerState(root);

    assert.equal(configured.directory, runnerDirectory(root, repository));
    assert.equal(started.pid, 4242);
    assert.equal(state.runners["marius-patrik/dream"].runnerName, "df-dream");
    assert.equal(state.runners["marius-patrik/dream"].labels[0], RUNNER_LABEL);
    assert.equal(stateText.includes("registration-secret"), false);
    assert.equal(stateText.includes("removal-secret"), false);
    assert.equal(statuses[0].github, "online");
    assert.equal(calls.some((call) => call.args.includes("--runasservice")), false);
    assert.ok(calls.some((call) => call.file === "cmd.exe" && call.args.some((arg) => arg.endsWith("config.cmd"))));
    assert.ok(calls.some((call) => call.redactions?.includes("registration-secret")));
    assert.ok(calls.some((call) => call.file === "cmd.exe" && call.args.some((arg) => arg.endsWith("run.cmd"))));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("RunnerManager setup redacts registration token from command failures", async () => {
  const root = await mkdtemp(join(tmpdir(), "df-runners-"));
  const repository: RepositoryRef = { owner: "marius-patrik", repo: "dream" };
  const token = "registration-secret";
  const manager = new RunnerManager({
    github: createRunnerClient(repository, token, "removal-secret"),
    downloader: createNoopDownloader(),
    commands: {
      async exec(file, args) {
        await materializeRunnerOnExpand(file, args, runnerDirectory(root, repository));
        if (args.some((arg) => arg.endsWith("config.cmd"))) {
          throw new Error(`config failed after echoing ${token}`);
        }

        return { stdout: "", stderr: "" };
      },
      spawnDetached() {
        return { pid: 4242 };
      }
    }
  });

  try {
    await assert.rejects(
      () => manager.setup(repository, { root }),
      (error: unknown) => {
        assert.ok(error instanceof Error);
        assert.equal(error.message.includes(token), false);
        assert.ok(error.message.includes("***"));
        return true;
      }
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("RunnerManager remove redacts removal token from command failures", async () => {
  const root = await mkdtemp(join(tmpdir(), "df-runners-"));
  const repository: RepositoryRef = { owner: "marius-patrik", repo: "dream" };
  const token = "removal-secret";
  const manager = new RunnerManager({
    github: createRunnerClient(repository, "registration-secret", token),
    downloader: createNoopDownloader(),
    commands: {
      async exec(file, args) {
        await materializeRunnerOnExpand(file, args, runnerDirectory(root, repository));
        if (args.includes("remove")) {
          throw new Error(`remove failed after echoing ${token}`);
        }

        return { stdout: "", stderr: "" };
      },
      spawnDetached() {
        return { pid: 4242 };
      }
    }
  });

  try {
    const configured = await manager.setup(repository, { root });
    await writeFile(join(configured.directory, "config.cmd"), "");

    await assert.rejects(
      () => manager.remove(repository, { root }),
      (error: unknown) => {
        assert.ok(error instanceof Error);
        assert.equal(error.message.includes(token), false);
        assert.ok(error.message.includes("***"));
        return true;
      }
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("runner command log formatting redacts token argv values", async () => {
  const root = await mkdtemp(join(tmpdir(), "df-runners-"));
  const repository: RepositoryRef = { owner: "marius-patrik", repo: "dream" };
  const token = "registration-secret";
  const logs: string[] = [];
  const manager = new RunnerManager({
    github: createRunnerClient(repository, token, "removal-secret"),
    downloader: createNoopDownloader(),
    commands: {
      async exec(file, args, options) {
        await materializeRunnerOnExpand(file, args, options?.cwd);
        logs.push(commandForLog(file, args, options?.redactions));
        return { stdout: "", stderr: "" };
      },
      spawnDetached() {
        return { pid: 4242 };
      }
    }
  });

  try {
    await manager.setup(repository, { root });

    assert.equal(logs.some((line) => line.includes(token)), false);
    assert.ok(logs.some((line) => line.includes("--token ***")));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

function createRunnerClient(repository: RepositoryRef, registrationToken: string, removalToken: string): GitHubRunnerClient {
  return {
    async createRegistrationToken() {
      return { token: registrationToken };
    },
    async createRemovalToken() {
      return { token: removalToken };
    },
    async listRunners() {
      return [
        {
          id: 1,
          name: runnerNameFor(repository),
          os: "Windows",
          status: "online",
          busy: false,
          labels: [RUNNER_LABEL]
        }
      ];
    },
    async getLatestWindowsX64RunnerRelease() {
      return {
        version: "v2.999.0",
        assetName: "actions-runner-win-x64-2.999.0.zip",
        downloadUrl: "https://example.invalid/actions-runner.zip"
      };
    }
  };
}

function createNoopDownloader(): Downloader {
  return {
    async download(_url, destination) {
      await writeFile(destination, "zip");
    }
  };
}

async function materializeRunnerOnExpand(file: string, args: string[], cwd: string | undefined): Promise<void> {
  if (file.toLowerCase() !== "powershell.exe") return;
  const destination = cwd ?? args.at(-1);
  if (!destination) return;
  await writeFile(join(destination, "config.cmd"), "");
  await writeFile(join(destination, "run.cmd"), "");
}
