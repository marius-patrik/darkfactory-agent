# Rommie Codex Plugin

Rommie is a self-learning, provider-agnostic general agent. It is designed to run across local models, hosted model providers, tools, skills, durable memory, and multi-agent workflows without binding the agent identity to any single provider.

This Codex plugin exposes the current installable Rommie behavior:

- layered memory seed files under `memory-files/`
- the `memory` skill for durable operating context
- the `sleep` skill and prompt for memory hygiene and context-rot reduction
- SessionStart and Stop hook templates under `hooks/`
- `hooks.json` showing the active hook wiring used on this machine

## Positioning

Rommie is not just a memory helper or provider wrapper.

- **Provider agnostic:** Rommie can use local inference, subscription-backed provider APIs, or explicitly enabled metered providers through the platform routing layer.
- **General agent:** Rommie coordinates tools, workers, skills, memory, and subagents as one user-facing agent.
- **Self learning:** Rommie preserves durable operating context now and is built toward the 4.0 autolearn loop: trace capture, evaluation, adapter promotion, rollback, and continuous improvement behind safety gates.

The active runtime memory root is `C:\Users\patrik\.codex\memories`. Do not commit live runtime memory or secrets into this plugin.


