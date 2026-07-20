# template-web

Bun and TypeScript web application template.

## What it includes

- Bun runtime and package management.
- TypeScript with strict compiler settings.
- A small HTTP server that serves static HTML and browser TypeScript.
- Testable page and HTML helpers.
- Scripts for development, typechecking, tests, build, and CI.
- GitHub Actions CI for pushes and pull requests to `main` and `dev`.

## Use this template

After creating a repository from this template, replace:

- package name in `package.json`
- README title and project summary
- sample page content in `src/page.ts`
- server routes in `src/server.ts`
- browser behavior in `src/client.ts`
- sample tests in `tests/page.test.ts`
- project-specific agent context in `.agents/.project/`

Keep `.agents/.global/` when you want the shared agent operating rules. Replace only `.agents/.project/` for the new repository's facts, commands, decisions, status, and handoff.

## Managed files

- `.agents/.global/` – reusable agent operating rules. Keep these files intact.
- `.agents/.project/` – project-specific facts, commands, decisions, status, and handoff. Replace these after creating a new repository from this template.

## Requirements

- Bun 1.3.14 or newer.

## Setup

```powershell
bun install
bun run ci
```

## Scripts

```powershell
bun run dev
bun run typecheck
bun test
bun run build
bun run start
```

The browser bundle is written to `dist/client.js`.

## Validation

The CI script runs typechecking, tests, and the build:

```powershell
bun run ci
```

## Release notes

- `v0.1.0` – Initial web template scaffold with server, page helpers, client bundle, tests, and CI.
- This README refresh adds a managed-files note and release-notes section after the template rename and merge.
