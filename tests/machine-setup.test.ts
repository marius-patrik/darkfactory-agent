import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";

import { convergeMachineRuntime, type MachineProcessRunner } from "../src/machine-setup.js";

test("machine setup builds and registers the exact package before proving its CLI binding", async () => {
  await withFixture(async ({ agentsHome, packageRoot }) => {
    const calls: string[] = [];
    const run = fixtureRunner(calls, { packageRegistered: true });
    const receipts = await convergeMachineRuntime({
      agentsHome,
      packageRoot,
      findingIds: ["darkfactory-package-unregistered", "darkfactory-command-unrunnable"],
      platform: "win32",
      run
    });

    assert.deepEqual(calls, [
      "pwsh -NoProfile -File launcher state doctor --json",
      "npm.cmd run build",
      `pwsh -NoProfile -File launcher packages register ${packageRoot}`,
      "pwsh -NoProfile -File launcher packages list --json",
      "pwsh -NoProfile -File launcher packages run darkfactory -- --help"
    ]);
    assert.equal(receipts[0]?.action, "machine-package-binding");
  });
});

test("machine setup chooses install for an absent runner and re-proves every readiness invariant", async () => {
  await withFixture(async ({ agentsHome, packageRoot }) => {
    const calls: string[] = [];
    const run = fixtureRunner(calls, { runnerInstalled: false });
    const receipts = await convergeMachineRuntime({
      agentsHome,
      packageRoot,
      findingIds: ["df-local-runner-missing"],
      platform: "win32",
      run
    });

    assert.ok(calls.includes("pwsh -NoProfile -File launcher runner install --json"));
    assert.equal(receipts[0]?.action, "machine-runner-lifecycle");
  });
});

test("machine setup fails closed before mutation when canonical state authority is unhealthy", async () => {
  await withFixture(async ({ agentsHome, packageRoot }) => {
    const calls: string[] = [];
    const run = fixtureRunner(calls, { stateHealthy: false });
    await assert.rejects(convergeMachineRuntime({
      agentsHome,
      packageRoot,
      findingIds: ["darkfactory-package-unregistered", "df-local-runner-offline"],
      platform: "win32",
      run
    }), /refused every mutation/);
    assert.deepEqual(calls, ["pwsh -NoProfile -File launcher state doctor --json"]);
  });
});

async function withFixture(run: (fixture: { agentsHome: string; packageRoot: string }) => Promise<void>): Promise<void> {
  const root = await mkdtemp(join(tmpdir(), "df-machine-setup-"));
  const agentsHome = join(root, "agents");
  const packageRoot = join(root, "darkfactory");
  try {
    await mkdir(join(agentsHome, "bin"), { recursive: true });
    await mkdir(packageRoot, { recursive: true });
    await writeFile(join(agentsHome, "bin", "agents.ps1"), "# fixture\n");
    await writeFile(join(packageRoot, "agent.package.json"), "{}\n");
    await writeFile(join(packageRoot, "package.json"), "{}\n");
    await run({ agentsHome, packageRoot });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}

function fixtureRunner(
  calls: string[],
  options: { stateHealthy?: boolean; packageRegistered?: boolean; runnerInstalled?: boolean }
): MachineProcessRunner {
  let runnerConverged = false;
  return (command, args) => {
    const normalized = [command, ...args.map((arg) => arg.includes("agents.ps1") ? "launcher" : arg)].join(" ");
    calls.push(normalized);
    if (args.includes("doctor")) {
      const healthy = options.stateHealthy !== false;
      return result({ ok: healthy, checks: [{ id: "state_repository", ok: healthy }, { id: "launcher", ok: healthy }] }, healthy ? 0 : 1);
    }
    if (args.includes("list") && args.includes("packages")) {
      const registrationPath = args.includes("list") && options.packageRegistered !== false
        ? calls.find((call) => call.includes(" packages register "))?.split(" packages register ")[1]
        : null;
      return result(registrationPath ? [{ id: "darkfactory", path: registrationPath }] : []);
    }
    if (args.includes("status") && args.includes("runner")) {
      const installed = runnerConverged || options.runnerInstalled !== false;
      return result({
        ok: runnerConverged,
        installed,
        readiness: {
          installed: runnerConverged,
          registered: runnerConverged,
          enabled: runnerConverged,
          persistent: runnerConverged,
          process: runnerConverged,
          online: runnerConverged,
          launcherBinding: runnerConverged
        }
      }, runnerConverged ? 0 : 1);
    }
    if (args.includes("install") || args.includes("repair")) runnerConverged = true;
    return result(null);
  };
}

function result(value: unknown, status = 0) {
  return { status, stdout: value === null ? "" : JSON.stringify(value), stderr: "" };
}
