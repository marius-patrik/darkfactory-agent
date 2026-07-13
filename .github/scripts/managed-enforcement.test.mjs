import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import test from "node:test";

const ciWorkflowPath = ".github/workflows/ci.yml";
const reviewWorkflowPath = ".github/workflows/codex-review.yml";

function step(workflow, name) {
  return workflow.match(new RegExp(`- name: ${name}[\\s\\S]*?(?=\\n\\s{6}- name:|$)`))?.[0] ?? "";
}

test("managed Validate provisions Go and uv before dependency installation", async () => {
  const workflow = await readFile(ciWorkflowPath, "utf8");
  const go = workflow.indexOf("uses: actions/setup-go@v6");
  const uv = workflow.indexOf("uses: astral-sh/setup-uv@v8.3.2");
  const install = workflow.indexOf("- name: Install dependencies");
  assert.ok(go >= 0, "managed Validate must provision Go");
  assert.ok(uv >= 0, "managed Validate must provision uv");
  assert.match(workflow, /cache-dependency-path: packages\/core\/contracts-go\/go\.sum/);
  assert.ok(go < install && uv < install, "language runtimes must be ready before install and validation");
});

test("monorepo validation uses the uv CLI without a cross-repository go.work", async () => {
  const commands = await readFile(".agents/.project/COMMANDS.md", "utf8");
  assert.doesNotMatch(commands, /python(?:3)?\s+-m\s+uv/);
  assert.match(commands, /\buv sync --frozen\b/);
  assert.equal(existsSync("go.work"), false);
  assert.equal(existsSync("packages/core/contracts-go/go.mod"), true);
});

test("review image primary path is pinned to the pull request base", async () => {
  const workflow = await readFile(reviewWorkflowPath, "utf8");
  const checkout = step(workflow, "Checkout trusted base");
  const select = step(workflow, "Select trusted review image inputs");
  assert.match(checkout, /ref: \$\{\{ github\.event\.pull_request\.base\.sha \}\}/);
  assert.match(checkout, /persist-credentials: false/);
  assert.match(select, /context=\./);
  assert.match(select, /\.github\/codex-review\.Dockerfile/);
  assert.match(select, /\.github\/codex-review\.schema\.json/);
  assert.match(select, /\.github\/scripts\/run-codex-review\.sh/);
});

test("review image edge path uses an immutable trusted bootstrap", async () => {
  const workflow = await readFile(reviewWorkflowPath, "utf8");
  const bootstrap = step(workflow, "Checkout immutable review bootstrap");
  assert.match(bootstrap, /if: steps\.review-image\.outputs\.bootstrap == 'true'/);
  assert.match(bootstrap, /repository: marius-patrik\/Andromeda/);
  assert.match(bootstrap, /ref: [a-f0-9]{40}/);
  assert.match(bootstrap, /path: trusted-review-bootstrap/);
  assert.match(bootstrap, /persist-credentials: false/);
});

test("review image denied path never builds from untrusted PR content", async () => {
  const workflow = await readFile(reviewWorkflowPath, "utf8");
  const build = step(workflow, "Build Codex review image");
  assert.match(build, /steps\.review-image\.outputs\.context/);
  assert.doesNotMatch(build, /pr-workspace/);
  assert.ok(
    workflow.indexOf("- name: Build Codex review image") < workflow.indexOf("- name: Checkout PR head"),
    "the trusted image must exist before the untrusted PR checkout",
  );
});

test("documented branch policy matches the enforced check names", async () => {
  const policy = await readFile(".darkfactory/branching-policy.md", "utf8");
  assert.match(policy, /Both `dev` and `main` use strict branch protection/);
  assert.match(policy, /`Validate` and\s+`Codex Review` status checks required/);
  assert.match(policy, /Force pushes and branch deletion are disabled/);
  assert.match(policy, /queued merge lands only after[\s\S]*both required checks report success/);
});

test("Andromeda-data posture records the compensating control without choosing billing or visibility", async () => {
  const posture = await readFile("docs/managed-enforcement.md", "utf8");
  assert.match(posture, /private repository/);
  assert.match(posture, /Upgrade to GitHub Pro or make this repository public/);
  assert.match(posture, /authenticated encrypted event\s+bundles/);
  assert.match(posture, /No billing or visibility change was made/);
});
