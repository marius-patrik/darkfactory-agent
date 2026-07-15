import { afterEach, describe, expect, test } from "bun:test";
import { Readable } from "node:stream";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { modelExecutionRequestFromCli } from "../src/model-execution-cli";

const roots: string[] = [];
afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

async function rootFixture(): Promise<string> {
  const root = await mkdtemp(path.join(os.tmpdir(), "agents-model-cli-"));
  roots.push(root);
  return root;
}

function flags(root: string): Record<string, string | boolean> {
  return {
    "model-tier": "high",
    effort: "high",
    "execution-policy": "read-only",
    receipt: path.join(root, "receipt.json"),
    mode: "orchestrator",
  };
}

describe("model execution CLI prompt boundary", () => {
  test("positional prompt produces the canonical independent-axis request", async () => {
    const root = await rootFixture();
    const request = await modelExecutionRequestFromCli({
      values: ["review", "this"],
      flags: flags(root),
      workdir: root,
    });
    expect(request).toEqual({
      modelTier: "high",
      effort: "high",
      executionPolicy: "read-only",
      receiptPath: path.join(root, "receipt.json"),
      workdir: root,
      mode: "orchestrator",
      prompt: "review this",
    });
  });

  test("prompt file and stdin keep the full context out of CLI values", async () => {
    const root = await rootFixture();
    const promptPath = path.join(root, "large-review.txt");
    const prompt = "TRUSTED_REVIEW_CONTEXT\n".repeat(1024);
    await writeFile(promptPath, prompt);
    const fromFile = await modelExecutionRequestFromCli({
      values: [],
      flags: { ...flags(root), "prompt-file": promptPath },
      workdir: root,
    });
    expect(fromFile.prompt).toBe(prompt);

    const fromStdin = await modelExecutionRequestFromCli({
      values: [],
      flags: { ...flags(root), "prompt-stdin": true },
      workdir: root,
      stdin: Readable.from(["first", " second"]),
    });
    expect(fromStdin.prompt).toBe("first second");
  });

  test("multiple, missing, and malformed prompt sources fail closed", async () => {
    const root = await rootFixture();
    await expect(
      modelExecutionRequestFromCli({ values: [], flags: flags(root), workdir: root }),
    ).rejects.toThrow("exactly one prompt source");
    await expect(
      modelExecutionRequestFromCli({
        values: ["positional"],
        flags: { ...flags(root), "prompt-stdin": true },
        workdir: root,
        stdin: Readable.from(["stdin"]),
      }),
    ).rejects.toThrow("exactly one prompt source");
    await expect(
      modelExecutionRequestFromCli({
        values: [],
        flags: { ...flags(root), "prompt-stdin": "yes" },
        workdir: root,
      }),
    ).rejects.toThrow("does not take a value");
    await expect(
      modelExecutionRequestFromCli({ values: ["prompt"], flags: { ...flags(root), mode: "unsafe" }, workdir: root }),
    ).rejects.toThrow("--mode is invalid");
  });

  test("required policy flags remain explicit rather than silently defaulting", async () => {
    const root = await rootFixture();
    for (const name of ["model-tier", "effort", "execution-policy", "receipt"]) {
      const incomplete = flags(root);
      delete incomplete[name];
      await expect(
        modelExecutionRequestFromCli({ values: ["prompt"], flags: incomplete, workdir: root }),
      ).rejects.toThrow(`run requires --${name}`);
    }
  });
});
