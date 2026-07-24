import { describe, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

const repoRoot = path.resolve(import.meta.dir, "..", "..", "..");
const cliPath = path.join(repoRoot, "src", "cli", "cli.ts");

async function runCli(
  cwd: string,
  args: string[],
): Promise<{ code: number; stdout: string; stderr: string }> {
  const child = Bun.spawn([process.execPath, cliPath, ...args], {
    cwd,
    env: {
      ...process.env,
      ANDROMEDA_HOME: path.join(cwd, ".agents"),
      ANDROMEDA_USER_HOME: cwd,
      ANDROMEDA_ROOT: cwd,
    },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [code, stdout, stderr] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);
  return { code, stdout, stderr };
}

describe("registered command router", () => {
  test("maps every public binary name to the same router", async () => {
    const manifest = (await Bun.file(path.join(repoRoot, "package.json")).json()) as {
      bin: Record<string, string>;
    };
    expect(manifest.bin.andromeda).toBe("./src/cli/cli.ts");
    expect(manifest.bin.agent).toBe(manifest.bin.andromeda);
    expect(manifest.bin.agents).toBe(manifest.bin.andromeda);
  });

  test("serves embedded version and plugin recovery commands", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "andromeda-router-"));
    try {
      const product = (await Bun.file(path.join(repoRoot, "package.json")).json()) as {
        version: string;
      };
      const version = await runCli(root, ["version"]);
      expect(version).toEqual({
        code: 0,
        stdout: `${product.version}\n`,
        stderr: "",
      });

      const plugins = await runCli(root, ["plugin", "list", "--json"]);
      expect(plugins.code).toBe(0);
      expect(JSON.parse(plugins.stdout)).toEqual([]);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });
});
