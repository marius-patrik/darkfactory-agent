# Project

## Identity

- Name: `vibe-bot`
- GitHub: `marius-patrik/vibe-bot`
- Visibility: public

## Purpose

Run a GitHub App bot that responds to GitHub webhooks and enforces shared repository setup conventions.

## Stack

- Runtime: Node.js 22
- Package manager: npm
- Language: TypeScript
- Module format: ESM
- Webhook framework: `@octokit/app`
- Test runner: Node test runner with `tsx`
- CI: GitHub Actions

## Managed Setup Policy

- Version-enforce `.agents/.global/VERSION` against `vibe-bot@<package version>`.
- Bootstrap-enforce `.github/workflows/ci.yml`.
- Comment on pull requests when installed repositories are missing or behind these conventions.
