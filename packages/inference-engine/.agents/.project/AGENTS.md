# Inference Engine Project Rules

Use the managed global rules first, then this project context.

This repository owns the Agentos inference runtime, Python agent loop, Go services, deployment assets, and package release automation. Keep CI wired through `bun run validate`, which delegates to the repo's fast validation entrypoint.
