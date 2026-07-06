# Agents OS Build And Release

This document describes how to build, smoke-test, and release the `agents-os`
container image. It implements the build pipeline shape defined in
`ARCHITECTURE.md` for agents-mono #9.

## Local Build

Build the dev image from the repository root:

```sh
bun run image:build
```

Equivalent Docker command:

```sh
docker build -f os/agents-os/Dockerfile -t agents-os:dev --build-arg AGENTS_OS_CHANNEL=dev .
```

The build:

1. Starts from an Ubuntu 24.04 base.
2. Installs CA certificates, git, OpenSSH client, curl, unzip, bash, and `tini`.
3. Installs pinned Bun, Node.js, Go, and uv toolchains.
4. Copies the repository root into `/opt/agents-os`.
5. Runs `bun install --frozen-lockfile`.
6. Filters `.gitmodules` to only the submodule entries present in the image.
7. Exposes `agents` on `PATH` and declares the shared-state mount contract.

## Local Smoke Test

Run a self-contained smoke test that exercises the image with a throwaway
shared-state directory:

```sh
bun run image:smoke
```

The smoke test mounts the temporary directories into the container and runs:

```sh
agents state init
agents doctor
```

No host `.agents` directory is modified.

## Release Pipeline

`.github/workflows/release-agents-os.yml` publishes the image on release or
manual workflow dispatch:

1. Checks out the repository.
2. Logs in to GitHub Container Registry with `GITHUB_TOKEN`.
3. Builds `os/agents-os/Dockerfile`.
4. Tags and pushes:
   - `ghcr.io/marius-patrik/agents-os:<version>`
   - `ghcr.io/marius-patrik/agents-os:<channel>` (e.g. `dev`, `latest`)
5. Records build metadata in image labels.

## Image Naming

- Local image: `agents-os:<version>` and `agents-os:dev`
- Registry image: `ghcr.io/marius-patrik/agents-os:<version>`
- Channels: `dev`, `latest`, and optional prerelease tags such as `edge`

Image labels (`docker inspect ghcr.io/marius-patrik/agents-os:<version>`):

- `io.agents.os.version`
- `io.agents.os.channel`
- `io.agents.os.commit`
- `io.agents.os.built-at`

## Secrets Safety

Secrets, credentials, and mutable operational data are never baked into the
image. The Dockerfile copies only the root package files, `os/agents-manager`,
and `os/agents-os` tooling. Runtime mounts provide `.agents`, `data/`, and
`workspaces/` from the host.
