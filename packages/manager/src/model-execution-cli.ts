import type { Readable } from "node:stream";
import type { SessionMode } from "../../harness/session";
import {
  readPromptFile,
  readPromptStdin,
  type ModelExecutionRequest,
} from "./model-execution";

const MODEL_RUN_MODES = new Set<SessionMode>(["orchestrator", "default", "chat", "task"]);

export interface ModelExecutionCliInput {
  values: string[];
  flags: Record<string, string | boolean>;
  workdir: string;
  stdin?: Readable;
}

function requiredStringFlag(flags: Record<string, string | boolean>, name: string): string {
  const value = flags[name];
  if (typeof value !== "string" || !value.trim()) throw new Error(`run requires --${name}`);
  return value.trim();
}

function optionalStringFlag(flags: Record<string, string | boolean>, name: string): string | undefined {
  const value = flags[name];
  if (value === undefined) return undefined;
  if (typeof value !== "string" || !value.trim()) throw new Error(`run --${name} requires a value`);
  return value.trim();
}

/**
 * Admit exactly one prompt source. Large or sensitive review material should
 * use --prompt-file or --prompt-stdin so it never appears in the process argv.
 */
export async function modelExecutionRequestFromCli(input: ModelExecutionCliInput): Promise<ModelExecutionRequest> {
  const promptFile = optionalStringFlag(input.flags, "prompt-file");
  const promptStdin = input.flags["prompt-stdin"] === true;
  if (input.flags["prompt-stdin"] !== undefined && !promptStdin) {
    throw new Error("run --prompt-stdin does not take a value");
  }
  const positional = input.values.join(" ").trim();
  const sourceCount = Number(Boolean(positional)) + Number(Boolean(promptFile)) + Number(promptStdin);
  if (sourceCount !== 1) {
    throw new Error("run requires exactly one prompt source: positional text, --prompt-file, or --prompt-stdin");
  }
  let prompt: string;
  if (promptFile) prompt = await readPromptFile(promptFile);
  else if (promptStdin) {
    if (!input.stdin) throw new Error("run --prompt-stdin requires piped input");
    prompt = await readPromptStdin(input.stdin);
  } else prompt = positional;

  const modeValue = optionalStringFlag(input.flags, "mode") ?? "default";
  if (!MODEL_RUN_MODES.has(modeValue as SessionMode)) throw new Error("run --mode is invalid");

  return {
    modelTier: requiredStringFlag(input.flags, "model-tier"),
    effort: requiredStringFlag(input.flags, "effort"),
    executionPolicy: requiredStringFlag(input.flags, "execution-policy"),
    receiptPath: requiredStringFlag(input.flags, "receipt"),
    workdir: input.workdir,
    mode: modeValue as SessionMode,
    prompt,
  };
}
