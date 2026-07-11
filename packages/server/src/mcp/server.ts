import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";
import { KnowledgeBase, runQuery, runMutation } from "@okf-agent/core";
import { buildSeedMemory, seedInstructions } from "./seed.js";

/**
 * Build the OKF MCP server. Each knowledge tool internally drives the LLM
 * agent (OKF spec in its system prompt) against the bundle.
 * Transport-agnostic — used by both the stdio bin and the HTTP endpoint.
 *
 * Seed memory: a session-start overview of what the KB contains, injected via
 * BOTH channels that reach the client LLM — the initialize `instructions`
 * field (standards channel) and the memory_query tool description (universal
 * fallback; every tool-calling client loads descriptions). Without it the
 * client model has no signal that memory might hold an answer.
 */
export async function buildMcpServer(kb: KnowledgeBase): Promise<McpServer> {
  // Seed generation must never prevent the server from starting — a missing
  // or empty bundle root degrades to a minimal seed, not a crash.
  const seed = await buildSeedMemory(kb).catch((err: Error) => {
    console.error(`[okf-mcp] seed generation failed: ${err.message}`);
    return "(memory overview unavailable — the bundle may be empty or unreadable; memory_status can diagnose)";
  });

  const queryDescription = (s: string) =>
    `Ask a natural-language question. An internal agent searches the OKF knowledge base, ` +
    `reads relevant concepts, and answers with cited bundle paths.\n\n` +
    `CURRENT MEMORY OVERVIEW:\n${s}`;

  const server = new McpServer(
    { name: "okf-knowledge-agent", version: "0.1.0" },
    { instructions: seedInstructions(seed) }
  );

  const queryTool = server.registerTool(
    "memory_query",
    {
      title: "Query the knowledge base",
      description: queryDescription(seed),
      inputSchema: { question: z.string().describe("The question to answer") },
    },
    async ({ question }) => {
      const { answer } = await runQuery(kb, question);
      return { content: [{ type: "text", text: answer }] };
    }
  );

  /**
   * Re-derive the seed after a mutation and push it into memory_query's
   * description; RegisteredTool.update() emits tools/list_changed so
   * long-lived (stdio) sessions see the fresh overview. Best-effort — a
   * refresh failure must never fail the mutation that triggered it.
   * (Instructions can't be updated mid-session; they refresh per session.)
   */
  const refreshSeed = async () => {
    try {
      const fresh = await buildSeedMemory(kb);
      queryTool.update({ description: queryDescription(fresh) });
    } catch (err) {
      console.error(`[okf-mcp] seed refresh failed: ${(err as Error).message}`);
    }
  };

  server.registerTool(
    "memory_add",
    {
      title: "Add knowledge",
      description:
        "Provide free-form knowledge (facts, docs, decisions, runbooks). An internal agent searches for overlap, then creates or extends OKF concepts; indexes and the update log are maintained automatically.",
      inputSchema: {
        content: z.string().describe("The knowledge to record, in any prose form"),
        suggested_path: z
          .string()
          .optional()
          .describe('Optional bundle path hint, e.g. "/apis/payments.md"'),
      },
    },
    async ({ content, suggested_path }) => {
      // Wrap the payload as an explicit directive. Bare content (e.g. a plain
      // fact like "The user's name is Anirban Kar.") otherwise reads as a chat
      // message and the agent replies conversationally instead of persisting it.
      const instruction =
        `Persist the following knowledge into the knowledge base. First search for ` +
        `related or owning concepts. If this is an attribute or detail of an ` +
        `existing concept, patch it into that concept rather than creating a new ` +
        `one. Only a distinct stand-alone entity or substantial topic gets its own ` +
        `concept — and then you must also patch the related existing concepts to ` +
        `link back to it. This is content to store, not a message to answer — you ` +
        `must use the write tools.\n\n` +
        `KNOWLEDGE TO RECORD:\n${content}` +
        (suggested_path ? `\n\nIf it fits, place new content at ${suggested_path}.` : "");
      const { summary, filesChanged } = await runMutation(kb, instruction);
      await refreshSeed();
      return {
        content: [
          { type: "text", text: `${summary}\n\nFiles changed:\n${filesChanged.map((f) => `- ${f}`).join("\n") || "- none"}` },
        ],
      };
    }
  );

  server.registerTool(
    "memory_update",
    {
      title: "Update knowledge",
      description:
        "Instruct a change to existing knowledge (correct a fact, deprecate a concept, restructure). An internal agent locates the concepts and applies targeted edits.",
      inputSchema: {
        instruction: z.string().describe("What to change, in natural language"),
      },
    },
    async ({ instruction }) => {
      const { summary, filesChanged } = await runMutation(kb, instruction);
      await refreshSeed();
      return {
        content: [
          { type: "text", text: `${summary}\n\nFiles changed:\n${filesChanged.map((f) => `- ${f}`).join("\n") || "- none"}` },
        ],
      };
    }
  );

  server.registerTool(
    "memory_status",
    {
      title: "Knowledge base status",
      description:
        "Deterministic (no LLM): bundle statistics and OKF conformance report.",
      inputSchema: {},
    },
    async () => {
      const report = await kb.validate();
      const types = await kb.listTypes();
      return {
        content: [
          {
            type: "text",
            text: JSON.stringify(
              {
                conformant: report.conformant,
                concepts: report.conceptCount,
                directories: report.directoryCount,
                types,
                errors: report.issues.filter((i) => i.severity === "error"),
                warnings: report.issues.filter((i) => i.severity === "warning").length,
              },
              null,
              2
            ),
          },
        ],
      };
    }
  );

  return server;
}
