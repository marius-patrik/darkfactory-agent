---
name: plan-workflow
description: Plan large-scale, ambiguous, or multi-workstream tasks into orchestration-ready execution workflows for subagents. Use when Codex needs to break down a major project, PRD, roadmap item, repo migration, incident response, research/build effort, or cross-repo change into scoped lanes, dependencies, acceptance criteria, prompts, validation gates, integration steps, and handoff artifacts for subagent or multi-agent execution.
---

# Plan Workflow

Use this skill to turn a large task into an execution plan that another orchestrator can hand to subagents with minimal interpretation.

The output should be concrete enough that each lane can start from its own brief, know its boundaries, produce reviewable artifacts, and prove its work at the real boundary.

## Workflow

1. Reconstruct context.
   - Read repo docs, PRD/specs, issue/PR state, existing plans, current branch status, and relevant memory.
   - Identify what is known, what is assumed, and what is still a decision.
   - Do not start implementation while the task still has unresolved product or architecture forks.

2. State the objective in one sentence.
   - Include the concrete user-visible or repo-visible outcome.
   - Name the system boundary: repo, service, plugin, runtime, deployment, docs, or external integration.

3. Define done.
   - Write behavioral acceptance criteria, not just build/test commands.
   - Include artifact expectations: files, PRs, docs, dashboards, scripts, migrations, releases, or validated outputs.
   - Mark any live, destructive, credentialed, or release-publish step as a gate.

4. Split into lanes.
   - Create lanes by ownership boundary, artifact type, or validation surface.
   - Keep each lane independently executable and reviewable.
   - Prefer fewer strong lanes over many vague lanes.
   - Give each lane: scope, non-scope, inputs, expected outputs, validation, and integration notes.

5. Order the lanes.
   - Identify prerequisites, parallelizable work, serial integration points, and risky unknowns.
   - Put discovery spikes before dependent implementation.
   - Put contract/schema/API work before consumers.
   - Put validation harnesses before broad implementation when no-false-green risk is high.

6. Write subagent briefs.
   - Each brief must be self-contained.
   - Include exact files or areas to inspect, constraints, output format, and stop conditions.
   - Ask for evidence, not confidence.
   - Avoid leaking your intended solution when asking for adversarial review.

7. Add integration and review gates.
   - Define who integrates outputs and how conflicts are resolved.
   - Require adversarial review for security, persistence, migrations, generated code, release flows, and "all green" claims.
   - Require real-boundary proof for user-visible behavior.

8. Produce the plan artifact.
   - Use `references/plan-template.md` when the user asks for a written plan or when the plan will be saved.
   - Keep open questions explicit and small.
   - Recommend defaults instead of listing options without a take.

## Lane Design Rules

- **Discovery lane:** use for unknown code shape, provider behavior, data migration inventory, or external API uncertainty.
- **Contract lane:** use for schemas, interfaces, manifests, CLI surfaces, and acceptance definitions.
- **Implementation lane:** use for bounded code/doc changes after contracts are stable.
- **Validation lane:** use for tests, probes, fixtures, smoke checks, and no-false-green attacks.
- **Integration lane:** use when several outputs must be reconciled into one branch, PR, release, or deployment.
- **Adversarial lane:** use when the author's green could be misleading.

## Subagent Brief Format

Use this compact shape:

```text
Lane: <name>
Goal: <one sentence>
Context to read: <paths/issues/docs>
Scope: <included work>
Non-scope: <explicit exclusions>
Constraints: <safety, style, provider, repo, branch, no secrets>
Deliverables: <files/artifacts/summary>
Validation: <commands and behavioral proof>
Stop and ask if: <decision gates or danger conditions>
Report back with: <evidence format>
```

## Planning Quality Bar

A good plan makes these things obvious:

- what can run in parallel
- what must happen first
- what each subagent owns
- what each subagent must not touch
- what evidence proves each lane
- how outputs merge
- what decisions remain with the user
- what would make the plan unsafe to execute

If those are not obvious, sharpen the plan before delegating.

