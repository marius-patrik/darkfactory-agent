import { describe, expect, test } from "bun:test";
import { parseGitmodules, serializeGitmodules } from "../../src/manager/gitmodules";

describe("gitmodules", () => {
  test("parses submodule entries", () => {
    expect(
      parseGitmodules(`[submodule "packages/skyblock-agent"]
\tpath = packages/skyblock-agent
\turl = https://github.com/marius-patrik/skyblock-agent.git
\tbranch = main
`),
    ).toEqual([
      {
        name: "packages/skyblock-agent",
        path: "packages/skyblock-agent",
        url: "https://github.com/marius-patrik/skyblock-agent.git",
        branch: "main",
      },
    ]);
  });

  test("serializes stable entries", () => {
    expect(
      serializeGitmodules([
        {
          name: "packages/darkfactory",
          path: "packages/darkfactory",
          url: "https://github.com/marius-patrik/agent-darkfactory.git",
          branch: "main",
        },
      ]),
    ).toBe(`[submodule "packages/darkfactory"]
\tpath = packages/darkfactory
\turl = https://github.com/marius-patrik/agent-darkfactory.git
\tbranch = main
`);
  });
});


