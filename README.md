# template-mono

Bun and TypeScript monorepo template.

## Packages

- `packages/cli` - command-line package
- `packages/web` - web package served by Bun

## Setup

```powershell
bun install
bun run typecheck
bun run build
```

Run package scripts from the root:

```powershell
bun run dev:cli
bun run dev:web
```
