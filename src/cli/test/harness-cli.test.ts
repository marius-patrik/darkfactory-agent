import { describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
  ensureSharedState,
  sharedState,
} from "../state";

const repoRoot = path.resolve(import.meta.dir, "..");
const cliPath = path.join(repoRoot, "cli.ts");

function cleanEnv(): Record<string, string | undefined> {
  const copy = { ...process.env };
  for (const key of Object.keys(copy)) {
    if (key.startsWith("ANDROMEDA_")) delete copy[key];
  }
  return copy;
}

async function runAgents(
  cwd: string,
  args: string[],
  env: Record<string, string | undefined> = {},
): Promise<{ code: number; stdout: string; stderr: string }> {
  const proc = Bun.spawn([process.execPath, cliPath, ...args], {
    cwd,
    env: {
      ...cleanEnv(),
      ANDROMEDA_HOME: path.join(cwd, ".agents"),
      ANDROMEDA_USER_HOME: cwd,
      ANDROMEDA_ROOT: cwd,
      ...env,
    },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [stdout, stderr, code] = await Promise.all([new Response(proc.stdout).text(), new Response(proc.stderr).text(), proc.exited]);
  return { code, stdout, stderr };
}

describe("harness CLI", () => {
  test("doctor and run commands reject a tampered external legacy registry before execution", async () => {
    const root = await mkdtemp(
      path.join(os.tmpdir(), "agents-package-registry-tamper-"),
    );
    try {
      const state = sharedState(root);
      await ensureSharedState(state);
      const harness = path.join(root, "external-harness");
      const marker = path.join(root, "executed.txt");
      await mkdir(harness, { recursive: true });
      await Bun.write(
        path.join(harness, "agent.package.json"),
        JSON.stringify({
          schemaVersion: 1,
          id: "probe",
          kind: "harness",
          entry: `${process.execPath} probe.ts`,
        }),
      );
      await Bun.write(
        path.join(harness, "probe.ts"),
        `await Bun.write(${JSON.stringify(marker)}, "executed\\n");\n`,
      );
      await Bun.write(
        state.packagesFile,
        `${JSON.stringify(
          [
            {
              id: "probe",
              kind: "harness",
              path: harness,
              manifestPath: path.join(harness, "agent.package.json"),
              registeredAt: new Date(0).toISOString(),
            },
          ],
          null,
          2,
        )}\n`,
      );

      const doctor = await runAgents(root, ["state", "doctor", "--json"]);
      expect(doctor.code).toBe(1);
      const report = JSON.parse(doctor.stdout) as {
        checks: Array<{
          id: string;
          ok: boolean;
          details?: { issues?: string[] };
        }>;
      };
      const capability = report.checks.find(
        (check) => check.id === "capability_integrity",
      );
      expect(capability?.ok).toBe(false);
      expect(capability?.details?.issues?.join("\n")).toContain(
        "package registry contains no canonical install for: probe",
      );

      for (const command of [
        ["harness", "run", "probe"],
        ["packages", "run", "probe"],
      ]) {
        const run = await runAgents(root, command);
        expect(run.code).toBe(1);
        expect(run.stderr).toContain(
          "package registry contains no canonical install for: probe",
        );
        expect(await Bun.file(marker).exists()).toBe(false);
      }
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  test("cli pin uses only the canonical provider home and exposes no raw exec escape", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "agents-cli-env-"));
    try {
      const subdir = path.join(root, "nested", "cwd");
      await mkdir(subdir, { recursive: true });
      const output = path.join(root, "codex-env.json");
      const stateHome = path.join(root, ".agents");
      const codex = path.join(
        stateHome,
        "clis",
        "codex",
        "bin",
        process.platform === "win32" ? "codex.ps1" : "codex",
      );
      if (process.platform === "win32") {
        await Bun.write(
          codex,
          `if ($args[0] -eq '--version') { Write-Output 'codex-test 1.0.0'; exit 0 }\n@("CODEX_HOME=$env:CODEX_HOME", "args=$($args -join ' ')") | Set-Content -LiteralPath '${output.replaceAll("'", "''")}'\n`,
        );
      } else {
        await Bun.write(
          codex,
          `#!/bin/sh\nif [ "$1" = "--version" ]; then printf 'codex-test 1.0.0\\n'; exit 0; fi\nprintf 'CODEX_HOME=%s\\nargs=%s\\n' "$CODEX_HOME" "$*" > "${output}"\n`,
        );
        await Bun.$`chmod +x ${codex}`;
      }

      const pin = await runAgents(subdir, ["cli", "pin", "codex"], { ANDROMEDA_HOME: stateHome });
      expect(pin.code).toBe(0);
      const registry = JSON.parse(await Bun.file(path.join(stateHome, "providers.json")).text()) as {
        providers: { codex: { executable: string } };
      };
      expect(registry.providers.codex.executable).toBe(codex);

      const doctor = await runAgents(subdir, ["cli", "doctor", "codex"], { ANDROMEDA_HOME: stateHome });
      expect(doctor.code).toBe(0);
      expect(doctor.stdout).toContain(`binary=${codex}`);

      const rawExec = await runAgents(subdir, ["cli", "exec", "codex", "--", "--probe"], {
        ANDROMEDA_HOME: stateHome,
      });
      expect(rawExec.code).toBe(1);
      expect(rawExec.stderr).toContain("unknown cli action: exec");
      expect(await Bun.file(output).exists()).toBe(false);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });
});
