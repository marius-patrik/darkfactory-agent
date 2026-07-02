# Project Agent Rules

These rules are specific to `vibe-bot`.

## Scope

This repository builds a TypeScript GitHub App bot that receives GitHub webhooks and comments on repository activity.

## Managed Repository Setup

- `.agents/.global/` is version-enforced by Vibe Bot in installed repositories.
- `.github` is bootstrap-enforced by Vibe Bot in installed repositories.
- Do not auto-mutate installed repositories yet; comment with precise missing or stale setup.
- Keep runtime-generated `.agents` metadata out of git.

## Bot Boundary

- Keep webhook handlers registered in `src/bot.ts`.
- Keep managed setup checks in `src/repository-setup.ts`.
- Keep HTTP routing and signature handoff behavior in `src/server.ts`.
- Keep environment parsing in `src/config.ts`.
- Add tests under `tests/` for new config, webhook, and setup-check behavior.
