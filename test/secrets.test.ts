import { describe, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { listSecrets, readSecret, secretPath, validateSecretName, writeSecret } from "../src/secrets";
import { ensureSharedState, sharedState } from "../src/state";

describe("secrets", () => {
  test("stores secret values in shared state without listing values", async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), "agents-secrets-"));
    try {
      const state = sharedState(root);
      await ensureSharedState(state);
      await writeSecret(state, "CODEX_AUTH_JSON", "{\"token\":\"redacted\"}\n");

      expect(await listSecrets(state)).toEqual(["CODEX_AUTH_JSON"]);
      expect(await readSecret(state, "CODEX_AUTH_JSON")).toBe("{\"token\":\"redacted\"}\n");
      expect(secretPath(state, "CODEX_AUTH_JSON")).toBe(path.join(root, ".agents", "secrets", "CODEX_AUTH_JSON.secret"));
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  test("rejects unsafe secret names", () => {
    expect(() => validateSecretName("../TOKEN")).toThrow(/invalid secret name/);
    expect(() => validateSecretName("lowercase")).toThrow(/invalid secret name/);
  });
});
