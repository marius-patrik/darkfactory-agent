# Handoff

## Current Work

The active branch is `codex/managed-folder-enforcement` for issue #1.

## Goal

Add `.agents` to `vibe-bot` and make the bot enforce shared repository setup in installed repositories.

## Validation To Run Before PR

```powershell
npm run typecheck
npm test
npm run build
```

Confirm GitHub Actions `validate` passes after opening the PR.

## Follow-Up

Consider Checks API enforcement later if comments are not strong enough.
