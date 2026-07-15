import assert from "node:assert/strict";
import test from "node:test";

import { parseCleanCliArgs, parseDoctorCliArgs, parseSetupCliArgs } from "../src/cli.js";

test("doctor CLI defaults to read-only control-repository diagnosis", () => {
  const parsed = parseDoctorCliArgs([]);
  assert.equal(parsed.target, "marius-patrik/DarkFactory");
  assert.equal(parsed.all, false);
  assert.equal(parsed.writeIssues, false);
});

test("doctor CLI parses explicit report and local evidence options", () => {
  const parsed = parseDoctorCliArgs([
    "marius-patrik/Andromeda",
    "--write-issues",
    "--json",
    "--local",
    "C:\\work\\Andromeda",
    "--agents-home",
    "C:\\Users\\patrik\\.agents"
  ]);
  assert.equal(parsed.target, "marius-patrik/Andromeda");
  assert.equal(parsed.writeIssues, true);
  assert.equal(parsed.json, true);
  assert.equal(parsed.localPath, "C:\\work\\Andromeda");
});

test("doctor CLI rejects ambiguous, unknown, and repair options", () => {
  assert.throws(() => parseDoctorCliArgs(["--all", "marius-patrik/Andromeda"]), /cannot be combined/);
  assert.throws(() => parseDoctorCliArgs(["--all", "--local", "."]), /cannot inspect/);
  assert.throws(() => parseDoctorCliArgs(["--repair"]), /intentionally unavailable/);
  assert.throws(() => parseDoctorCliArgs(["--unknown"]), /unknown doctor option/);
});

test("setup CLI shares doctor targeting and rejects destructive bypasses", () => {
  const parsed = parseSetupCliArgs(["marius-patrik/Andromeda", "--watch", "--json"]);
  assert.equal(parsed.target, "marius-patrik/Andromeda");
  assert.equal(parsed.watch, true);
  assert.equal(parsed.json, true);
  assert.throws(() => parseSetupCliArgs(["--force"]), /intentionally unavailable/);
});

test("clean CLI defaults to plan and requires an explicit durable apply ID", () => {
  const previous = process.env.AGENTS_HOME;
  process.env.AGENTS_HOME = "C:\\Users\\patrik\\.agents";
  try {
    const plan = parseCleanCliArgs(["marius-patrik/Andromeda", "--local", "C:\\work\\Andromeda"]);
    assert.equal(plan.mode, "plan");
    assert.equal(plan.target, "marius-patrik/Andromeda");
    assert.throws(() => parseCleanCliArgs(["apply"]), /requires a durable plan ID/);
    assert.throws(() => parseCleanCliArgs(["apply", "clean-123", "--force"]), /intentionally unavailable/);
  } finally {
    if (previous === undefined) delete process.env.AGENTS_HOME;
    else process.env.AGENTS_HOME = previous;
  }
});
