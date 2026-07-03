import { describe, expect, test } from "bun:test";
import { parseGitmodules, serializeGitmodules } from "../src/gitmodules";

describe("gitmodules", () => {
  test("parses submodule entries", () => {
    expect(
      parseGitmodules(`[submodule "agents/skyblock-agent"]
\tpath = agents/skyblock-agent
\turl = https://github.com/marius-patrik/skyblock-agent.git
\tbranch = main
`),
    ).toEqual([
      {
        name: "agents/skyblock-agent",
        path: "agents/skyblock-agent",
        url: "https://github.com/marius-patrik/skyblock-agent.git",
        branch: "main",
      },
    ]);
  });

  test("serializes stable entries", () => {
    expect(
      serializeGitmodules([
        {
          name: "harnesses/andromeda-harness",
          path: "harnesses/andromeda-harness",
          url: "https://github.com/marius-patrik/andromeda-harness.git",
          branch: "main",
        },
      ]),
    ).toBe(`[submodule "harnesses/andromeda-harness"]
\tpath = harnesses/andromeda-harness
\turl = https://github.com/marius-patrik/andromeda-harness.git
\tbranch = main
`);
  });
});

