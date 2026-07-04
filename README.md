# template-repo

Bun and TypeScript project template for a small repository.

## What it includes

- Bun runtime and package management.
- TypeScript with strict compiler settings.
- A small `src/` entrypoint with a Bun test.
- Scripts for development, typechecking, tests, build, and CI.
- GitHub Actions CI for pushes and pull requests to `main` and `dev`.

## Use this template

After creating a repository from this template, replace:

- package name in `package.json`
- README title and project summary
- sample code in `src/index.ts`
- sample tests in `tests/index.test.ts`
- project-specific context in `.agents/.project/`

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
bun run ci
```

The build output is written to `dist/`.

## Validation

The CI script runs typechecking, tests, and the build:

```powershell
bun run ci
```

## Release notes

- `v0.1.0` – Initial repository template scaffold with Bun, TypeScript, tests, build, and CI (merged via PR #2).
- This README refresh adds a managed-files note and release-notes section after the template rename and merge.
