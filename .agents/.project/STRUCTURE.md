# Structure

## Project Files

- `src/index.ts` - runtime entrypoint
- `src/bot.ts` - GitHub App webhook handlers
- `src/repository-setup.ts` - installed-repository setup enforcement
- `src/server.ts` - HTTP webhook server
- `src/config.ts` - environment parsing
- `tests/` - Node test suite
- `package.json` - npm package metadata and scripts
- `tsconfig.json` - TypeScript configuration
- `.github/workflows/ci.yml` - GitHub Actions validation
- `README.md` - template setup and usage notes

## Agent Files

- `AGENTS.md` - root entrypoint for agents
- `.agents/.global/` - reusable agent protocol, workflow, validation, docs, skills, and version marker
- `.agents/.project/` - project-specific context, commands, status, decisions, dialogue, and handoff

## Generated Or Ignored

- `node_modules/`
- `dist/`
- coverage output
- local environment files
- runtime-generated `.agents` metadata
