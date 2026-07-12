import { generateText, streamText, stepCountIs, type LanguageModel, type ModelMessage } from "ai";
import type { KnowledgeBase } from "../okf/index.js";
import { resolveModel, type ProviderName } from "../providers/index.js";
import { buildSystemPrompt } from "./system-prompt.js";
import { buildReadTools, buildWriteTools, formatTree } from "./tools.js";
import { TraceRecorder, TraceStore } from "./trace.js";

const MAX_STEPS = 12;

export interface AgentOptions {
  provider?: ProviderName;
  model?: string;
}

export interface QueryResult {
  answer: string;
  steps: number;
  traceId: string;
}

export interface MutationResult {
  summary: string;
  filesChanged: string[];
  steps: number;
  traceId: string;
}

async function promptContext(kb: KnowledgeBase, mode: "query" | "mutate" | "chat") {
  const [types, tree] = await Promise.all([kb.listTypes(), kb.listTree()]);
  return { existingTypes: types, treeSummary: formatTree(tree), mode };
}

function pickModel(options: AgentOptions): Promise<LanguageModel> {
  return resolveModel(options.provider, options.model);
}

function traceStore(kb: KnowledgeBase): TraceStore {
  return new TraceStore(kb.bundle.root);
}

/** Read-only Q&A over the bundle. */
export async function runQuery(
  kb: KnowledgeBase,
  question: string,
  options: AgentOptions = {}
): Promise<QueryResult> {
  const ctx = await promptContext(kb, "query");
  const recorder = new TraceRecorder();
  const result = await generateText({
    model: await pickModel(options),
    system: buildSystemPrompt(ctx),
    prompt: question,
    tools: buildReadTools(kb, recorder),
    stopWhen: stepCountIs(MAX_STEPS),
  });
  const trace = recorder.finalize("query", question, result.text);
  await traceStore(kb).save(trace);
  return { answer: result.text, steps: result.steps.length, traceId: trace.id };
}

/** Knowledge add/update — full toolset, low temperature. */
export async function runMutation(
  kb: KnowledgeBase,
  instruction: string,
  options: AgentOptions = {}
): Promise<MutationResult> {
  const ctx = await promptContext(kb, "mutate");
  const recorder = new TraceRecorder();
  const filesChanged = new Set<string>();
  const result = await generateText({
    model: await pickModel(options),
    system: buildSystemPrompt(ctx),
    prompt: instruction,
    tools: { ...buildReadTools(kb, recorder), ...buildWriteTools(kb, filesChanged, recorder) },
    stopWhen: stepCountIs(MAX_STEPS),
    temperature: 0.2,
  });
  const trace = recorder.finalize("mutation", instruction, result.text);
  await traceStore(kb).save(trace);
  return {
    summary: result.text,
    filesChanged: [...filesChanged].sort(),
    steps: result.steps.length,
    traceId: trace.id,
  };
}

/** Interactive chat — full toolset, streaming. Caller converts to a UI stream response. */
export async function streamChat(
  kb: KnowledgeBase,
  messages: ModelMessage[],
  options: AgentOptions = {}
) {
  const ctx = await promptContext(kb, "chat");
  const recorder = new TraceRecorder();
  const filesChanged = new Set<string>();
  // The user turn that started this run, for the trace record.
  const lastUser = [...messages].reverse().find((m) => m.role === "user");
  const input =
    typeof lastUser?.content === "string"
      ? lastUser.content
      : lastUser?.content
          ?.map((part) => (part.type === "text" ? part.text : ""))
          .join(" ")
          .trim() ?? "(chat)";

  const result = streamText({
    model: await pickModel(options),
    system: buildSystemPrompt(ctx),
    messages,
    tools: { ...buildReadTools(kb, recorder), ...buildWriteTools(kb, filesChanged, recorder) },
    stopWhen: stepCountIs(MAX_STEPS),
    onFinish: async ({ text }) => {
      // Persist only turns that actually touched the bundle.
      if (recorder.steps.length > 0) {
        await traceStore(kb).save(recorder.finalize("chat", input, text));
      }
    },
  });
  return { result, filesChanged };
}
