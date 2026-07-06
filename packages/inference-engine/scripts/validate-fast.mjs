import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const isWindows = process.platform === "win32";

function commandPath(name, fallback) {
  if (process.env[name]) {
    return process.env[name];
  }
  if (fallback && existsSync(fallback)) {
    return fallback;
  }
  return name.toLowerCase();
}

const uv = commandPath("UV", null);
const go = commandPath("GO", isWindows ? "C:\\Program Files\\Go\\bin\\go.exe" : null);

// This repo depends on ../agents-core/contracts-go via go.work; require it
// so CI cannot silently skip the Go validation targets.
const agentsCoreContractsGo = join(root, "..", "agents-core", "contracts-go", "go.mod");
const goWorkspaceReady = existsSync(agentsCoreContractsGo);

const checks = [
  {
    name: "python-agent unit tests",
    cwd: "python-agent",
    command: uv,
    args: ["run", "pytest", "-q", "-m", "not live"],
  },
  {
    name: "engine-go fast packages",
    cwd: "engine-go",
    command: go,
    args: ["test", "./pkg/contracts", "./internal/config", "./internal/events"],
  },
  {
    name: "coordination fast packages",
    cwd: "services/coordination",
    command: go,
    args: ["test", "./internal/events", "./internal/ops"],
  },
  {
    name: "daemon packages",
    cwd: "services/daemon",
    command: go,
    args: ["test", "./..."],
  },
];

if (!goWorkspaceReady) {
  console.error(`Required cross-repo workspace dependency is missing: ${agentsCoreContractsGo}`);
  console.error("Run with ../agents-core checked out next to this repository, or clone it from https://github.com/marius-patrik/agents-core.");
  process.exit(1);
}

for (const check of checks) {
  console.log(`\n==> ${check.name}`);
  const result = spawnSync(check.command, check.args, {
    cwd: join(root, check.cwd),
    stdio: "inherit",
    shell: false,
  });
  if (result.error) {
    console.error(`${check.name} failed to start: ${result.error.message}`);
    process.exit(1);
  }
  if (result.status !== 0) {
    console.error(`${check.name} failed with exit code ${result.status}`);
    process.exit(result.status ?? 1);
  }
}

console.log("\nfast validation passed");

