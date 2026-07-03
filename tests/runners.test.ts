import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

import {
  RUNNER_LABEL,
  RunnerManager,
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
  const calls: Array<{ file: string; args: string[]; cwd?: string }> = [];
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
      calls.push({ file, args, cwd: options?.cwd });
      return { stdout: "{}", stderr: "" };
    },
    spawnDetached(file, args, options) {
      calls.push({ file, args, cwd: options?.cwd });
      return { pid: 4242 };
    }
  };
  const downloader: Downloader = {
    async download() {
      return;
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
    assert.ok(calls.some((call) => call.file === "cmd.exe" && call.args.some((arg) => arg.endsWith("run.cmd"))));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
