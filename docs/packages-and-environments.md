# Packages and Environments Groundwork

Issue #10 expands `agents-manager` from local agent package registration toward real OS/container package and named environment management. This document defines the first local contracts only. Real distro package installation, image pulls, environment switching, and workspace provisioning depend on agents-mono #8 and #9.

## Current Boundary

Implemented now:

- `.agents/environments.json` state file with typed records for future distro packages, container packages, and environments.
- `AGENTS_ENVIRONMENTS` exported through `.agents/env` and package/harness execution environments.
- CLI command skeletons for `agents packages distro ...`, `agents packages container ...`, and `agents env ...`.
- Clear `not yet implemented` errors for operations that would mutate OS packages, images, or active environments.

Not implemented now:

- Installing host OS packages.
- Pulling, pinning, upgrading, or removing container images.
- Creating, switching, or syncing named environments.
- Per-agent workspace environment provisioning.

## State File

Path: `.agents/environments.json`

```json
{
  "schemaVersion": 1,
  "activeEnvironmentId": "host",
  "distroPackages": [
    {
      "id": "base-curl",
      "target": "host",
      "manager": "apt",
      "name": "curl",
      "version": "8.0.0",
      "source": "agents-os"
    }
  ],
  "containerPackages": [
    {
      "id": "agents-os-base",
      "image": "ghcr.io/marius-patrik/agents-os",
      "digest": "sha256:...",
      "tags": ["latest"],
      "runtime": "docker"
    }
  ],
  "environments": [
    {
      "id": "host",
      "kind": "host",
      "packages": ["base-curl"],
      "secretsScope": "host",
      "createdAt": "2026-07-03T00:00:00.000Z"
    }
  ]
}
```

The initial file is empty except for `schemaVersion: 1` and empty arrays. Future implementations should treat records as desired state, not proof that a host package or container image is currently installed.

## CLI Skeleton

```text
agents packages distro <define|install|upgrade|remove> ...
agents packages container <define|pull|pin|upgrade|remove> ...
agents env list [--json]
agents env create <id> [--kind host|container|agent-workspace]
agents env switch <id>
agents env sync <id>
```

`agents env list` may read local desired-state records. Mutating commands must stay stubbed until agents-mono #8 defines the architecture/data contracts and agents-mono #9 defines the base image and release pipeline.

## Integration Rules

- Keep all package and environment operations under the `agents` CLI to satisfy the single-management-surface mandate from #7.
- Reuse `.agents/secrets` scopes rather than creating a separate secret store for environments.
- Use Docker as the first container runtime target unless a future issue changes that contract.
- Never pretend a package was installed or an image was pulled without invoking the real provider and recording evidence.
