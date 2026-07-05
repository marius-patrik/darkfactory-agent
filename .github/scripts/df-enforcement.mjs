import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import path from "node:path";
import {
  getOptionalFileContent,
  repoName
} from "./df-lib.mjs";

export const ENFORCEMENT_RULES_PATH = ".darkfactory/enforcement-rules.json";

export const DEFAULT_ENFORCEMENT_RULES = {
  schemaVersion: 1,
  rules: [
    {
      id: "never-merge-red",
      description: "Merge gates require all reported checks and required checks to be successful.",
      enabled: true,
      events: ["merge"],
      assertions: [
        {
          fact: "checks.allReportedChecksGreen",
          op: "equals",
          value: true,
          message: "All reported checks must be successful before merge."
        },
        {
          fact: "checks.requiredChecksGreen",
          op: "equals",
          value: true,
          message: "All required checks must be present and successful before merge."
        }
      ]
    },
    {
      id: "no-force-push",
      description: "DarkFactory must not force-push worker branches.",
      enabled: true,
      events: ["dispatch", "worker-preflight"],
      assertions: [
        {
          fact: "git.forcePush",
          op: "equals",
          value: false,
          message: "Force-push is not allowed."
        }
      ]
    },
    {
      id: "no-admin-bypass",
      description: "DarkFactory must not use admin bypass merge paths.",
      enabled: true,
      events: ["merge"],
      assertions: [
        {
          fact: "merge.adminBypass",
          op: "equals",
          value: false,
          message: "Admin bypass merge is not allowed."
        }
      ]
    },
    {
      id: "secrets-never-logged",
      description: "DarkFactory action logs and errors must redact configured secrets.",
      enabled: true,
      events: ["dispatch", "worker-preflight", "merge"],
      assertions: [
        {
          fact: "logging.secretsRedacted",
          op: "equals",
          value: true,
          message: "Secrets must be redacted from logs and ledger errors."
        }
      ]
    },
    {
      id: "parked-repos-untouched",
      description: "DarkFactory must not dispatch workers or merge PRs in parked repositories.",
      enabled: true,
      events: ["dispatch", "worker-preflight", "merge"],
      assertions: [
        {
          fact: "repository.parked",
          op: "equals",
          value: false,
          message: "Parked repositories are not eligible for DarkFactory actions."
        },
        {
          fact: "repository.lifecycleState",
          op: "notIn",
          value: ["parked", "archived", "completed", "removed"],
          message: "Only active repositories are eligible for DarkFactory actions."
        }
      ]
    },
    {
      id: "work-prs-target-dev",
      description: "Worker PRs target dev when the repository has a dev branch.",
      enabled: true,
      events: ["worker-preflight", "merge"],
      assertions: [
        {
          fact: "pullRequest.baseRefName",
          op: "equalsFact",
          value: "repository.expectedWorkBaseBranch",
          message: "DarkFactory worker PRs must target the expected work base branch."
        }
      ]
    }
  ]
};

export async function loadEnforcementRules(options = {}) {
  const localConfig = await readLocalRules(options.localRoot);
  const remoteConfig = options.gh && options.repository
    ? await readRemoteRules(options.gh, options.repository, options.ref)
    : null;
  return normalizeEnforcementRules(mergeRuleConfigs(DEFAULT_ENFORCEMENT_RULES, localConfig, remoteConfig));
}

export function normalizeEnforcementRules(config) {
  const rules = Array.isArray(config?.rules) ? config.rules : [];
  return {
    schemaVersion: Number(config?.schemaVersion) || 1,
    rules: rules.map(normalizeRule).filter(Boolean)
  };
}

export function enforceRules(config, event, facts) {
  const failures = [];
  const evaluated = [];
  for (const rule of normalizeEnforcementRules(config).rules) {
    if (!rule.enabled || !rule.events.includes(event)) continue;
    evaluated.push(rule.id);
    for (const assertion of rule.assertions) {
      const actual = getPath(facts, assertion.fact);
      const expected = assertion.op === "equalsFact" ? getPath(facts, assertion.value) : assertion.value;
      if (!assertionPasses(assertion.op, actual, expected)) {
        failures.push({
          rule: rule.id,
          fact: assertion.fact,
          op: assertion.op,
          expected,
          actual,
          message: assertion.message || `${assertion.fact} failed ${assertion.op}`
        });
      }
    }
  }

  return {
    ok: failures.length === 0,
    event,
    evaluated,
    failures
  };
}

export function assertEnforcement(config, event, facts) {
  const result = enforceRules(config, event, facts);
  if (!result.ok) {
    const lines = result.failures.map((failure) => {
      return `${failure.rule}: ${failure.message} (${failure.fact}=${formatValue(failure.actual)})`;
    });
    const error = new Error(`DarkFactory enforcement blocked ${event}: ${lines.join("; ")}`);
    error.enforcement = result;
    throw error;
  }
  return result;
}

export function mergeRuleConfigs(...configs) {
  const byId = new Map();
  let schemaVersion = 1;
  for (const config of configs.filter(Boolean)) {
    if (Number.isInteger(config.schemaVersion)) schemaVersion = config.schemaVersion;
    for (const rule of Array.isArray(config.rules) ? config.rules : []) {
      if (!rule?.id) continue;
      byId.set(rule.id, { ...(byId.get(rule.id) || {}), ...rule });
    }
  }
  return { schemaVersion, rules: [...byId.values()] };
}

async function readLocalRules(localRoot = process.cwd()) {
  const filePath = path.join(localRoot, ENFORCEMENT_RULES_PATH);
  if (!existsSync(filePath)) return null;
  return parseRules(await readFile(filePath, "utf8"), filePath);
}

async function readRemoteRules(gh, repository, ref) {
  const content = await getOptionalFileContent(gh, repository, ENFORCEMENT_RULES_PATH, ref);
  if (!content) return null;
  return parseRules(content, `${repoName(repository)}:${ENFORCEMENT_RULES_PATH}`);
}

function parseRules(content, source) {
  try {
    return JSON.parse(content);
  } catch (error) {
    throw new Error(`Invalid DarkFactory enforcement rules at ${source}: ${error.message || String(error)}`);
  }
}

function normalizeRule(rule) {
  if (!rule || typeof rule.id !== "string" || !rule.id.trim()) return null;
  return {
    id: rule.id.trim(),
    description: typeof rule.description === "string" ? rule.description : "",
    enabled: rule.enabled !== false,
    events: normalizeEvents(rule.events),
    assertions: normalizeAssertions(rule.assertions)
  };
}

function normalizeEvents(events) {
  if (!Array.isArray(events) || events.length === 0) return ["dispatch", "worker-preflight", "merge"];
  return events.map(String).map((event) => event.trim()).filter(Boolean);
}

function normalizeAssertions(assertions) {
  if (!Array.isArray(assertions)) return [];
  return assertions
    .filter((assertion) => assertion && typeof assertion.fact === "string" && typeof assertion.op === "string")
    .map((assertion) => ({
      fact: assertion.fact,
      op: assertion.op,
      value: assertion.value,
      message: typeof assertion.message === "string" ? assertion.message : ""
    }));
}

function assertionPasses(op, actual, expected) {
  if (op === "equals" || op === "equalsFact") return actual === expected;
  if (op === "notEquals") return actual !== expected;
  if (op === "in") return Array.isArray(expected) && expected.includes(actual);
  if (op === "notIn") return !Array.isArray(expected) || !expected.includes(actual);
  if (op === "includes") return Array.isArray(actual) && actual.includes(expected);
  if (op === "notIncludes") return !Array.isArray(actual) || !actual.includes(expected);
  if (op === "matches") return new RegExp(String(expected)).test(String(actual ?? ""));
  if (op === "exists") return actual !== undefined && actual !== null;
  if (op === "notExists") return actual === undefined || actual === null;
  throw new Error(`Unsupported DarkFactory enforcement assertion op: ${op}`);
}

function getPath(value, pathName) {
  return String(pathName).split(".").reduce((current, part) => {
    if (current === undefined || current === null) return undefined;
    return current[part];
  }, value);
}

function formatValue(value) {
  if (typeof value === "string") return JSON.stringify(value);
  return JSON.stringify(value);
}
