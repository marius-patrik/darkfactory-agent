# Deploy Notes

This directory holds deployment artifacts for the Agents platform.

## Release framing

- Latest release: **`v0.1.1`**
- Current repo version: **`v0.1.1`** plus post-release `main` changes
- Release scope is tracked in this README, the root `README.md`, `engine-go/README.md`, and the evidence under
  `docs/proofs/`. This repository does not currently carry `.agents/context` PRD/ADR/PLAN files.

## Current vs target

- **Execution truth:** runs execute as **k8s-scheduled Jobs**; **CI stays orchestration/validation/record,
  not the executor**.
- **Current cluster reality:** s002 is the k3s server and s001 is joined as an agent. Basic Jobs and GPU Jobs
  (`nvidia.com/gpu=1`, `gpu-vectoradd`) have passed on both nodes.
- **Current inference reality:** all LLM calls must enter through the gateway. General/research and judge traffic are
  local llama.cpp/GGUF endpoints backed by CPU threads and system RAM; coding traffic is a gateway role pool across
  the s001 and s002 GPU coder endpoints. Self-hosted NVCF remains a disabled burst path until deployed and verified.

## Cluster facts

- Nodes: `s001`, `s002`
- Both nodes: NixOS, one RTX 3090 each, Docker with CDI GPU access
- NATS coordination: both node-local NATS containers must join the same `agents-ha` route mesh on port `6222`
  so JetStream/KV leader leases are shared across `s001` and `s002`.
- Research/judge: CPU-backed local llama.cpp/GGUF inference
- Coding: gateway-spread work across both GPU coder endpoints is the minimum operating mode; self-hosted NVCF is the
  burst/dynamic-GPU target and must not be documented as live until verified.
- k3s GPU on NixOS: apply `deploy/k8s/nvidia-device-plugin-nixos.yaml`; smoke with
  `deploy/k8s/gpu-vectoradd-nixos-smoke.yaml`.

## QFT autoresearch loop on k8s (I12)

The QFT autoresearch loop (seed + bridge + iterate) runs as **bounded k8s Jobs**, not as host
user-systemd services:

- `deploy/k8s/qft-autoresearch-loop.yaml` defines two Jobs in the `agents` namespace:
  `qft-seeder-loop` (drives `seeder_loop.py` → bounded `seeder.py` iterations) and `qft-queue-bridge`
  (turns pending `queue.jsonl` tasks into GitHub PRD issues). Both use the `agents/harness` image,
  mount `$AGENTS_ROOT` (so queue/heartbeat/baseline/evidence are durable), and are bounded by
  `backoffLimit: 0` + a fixed `activeDeadlineSeconds`. The **Go daemon is the sole scheduler**: it
  applies/reconciles these Jobs. There is **no CronJob, no fan-out, no self-trigger** in the manifest.
- **Restart-safe + idempotent:** the seeder is a flock singleton with atomic (tempfile+rename) state
  writes, so a re-apply while a Job is running is a no-op and a second pod exits on the lock. The bridge
  dedupes issues by exact Task ID and writes an atomic heartbeat.
- **RETIRED:** the user-systemd units `deploy/dekstop/systemd/agents-qft-seeder.service` and
  `agents-qft-bridge.service` are retired (they ran `Restart=always` as a second host-local scheduler,
  violating the daemon-sole-scheduler rule). They are not installed by `deploy-services.sh` and must not
  be enabled; the unit files are kept only for history and point at this manifest.

## HA NATS setup

`deploy/docker-compose.cluster.yml` runs one NATS container per node, but those containers are not independent
coordination planes. Each node must set a unique `NODE_ID` before compose starts:

```bash
# on s001
NODE_ID=s001 AGENTS_NATS_CLUSTER_ROUTES=nats://s001:6222,nats://s002:6222 deploy/deploy.sh

# on s002
NODE_ID=s002 AGENTS_NATS_CLUSTER_ROUTES=nats://s001:6222,nats://s002:6222 deploy/deploy.sh
```

The compose derives the NATS server name and route advertisement from `NODE_ID` by default. Leave `NATS_URL` at its
default `nats://nats:4222`: daemon and manager connect to their node-local NATS server, and NATS routes share JetStream
KV state between the two servers. If a node must use a nonstandard route identity, set `AGENTS_NATS_SERVER_NAME` and
`AGENTS_NATS_CLUSTER_ADVERTISE` explicitly.

`NODE_ID` intentionally defaults to empty. With `NATS_URL` configured, daemon and manager exit on an empty node ID rather
than sharing a lease identity such as `unknown`.

## What this directory should support today

1. Gateway, daemon, and manager deployment with honest health checks.
2. Bounded k8s Job dispatch through the daemon.
3. No autoscaler, no self-trigger, no cron fan-out.
4. A clean migration path toward the self-hosted NVCF layer.

## Current Documentation Sources

- Root package layout and fast validation: `README.md`.
- Go daemon build, run, API, and package tests: `engine-go/README.md`.
- Live loop acceptance evidence: `docs/proofs/vs2-s3.3-loop-acceptance.md`.
- GPU/NixOS operational notes: `docs/ops/s002-cdi-gpu.md`.
- Deployment and release-candidate scope: this file.

## Release Automation

The managed `DarkFactory Release` GitHub workflow runs on `v*.*.*` tags. It
checks out the `agents-core` contracts sibling required by `go.work`, installs
the Go/Python/uv toolchain used by `bun run validate`, runs
`.github/scripts/dark-factory-release-check.mjs --mode release`, and creates the
GitHub release with generated notes.

This repository does not currently publish daemon, manager, comms helper,
harness, gateway, or self-improvement binary payloads. There is no
`deploy-package/scripts/build-release-artifacts.sh` in the current release path,
and `v0.1.0`/`v0.1.1` intentionally have no uploaded release assets. Add a
build script, smoke script, and release-policy artifact globs before documenting
binary release payloads.

Release evidence:

- `v0.1.1` release: https://github.com/marius-patrik/inference-engine/releases/tag/v0.1.1
- successful `DarkFactory Release` run: https://github.com/marius-patrik/inference-engine/actions/runs/28693774123
- `v0.1.0` release exists, but its original release workflow run failed before
  `v0.1.1` restored the release setup.

The release workflow does not currently autodeploy. Manual deploy commands stay
credential-gated and non-invasive by default: `deploy/deploy-release.sh` plans
without mutation unless `--apply` is passed, and any future autodeploy job must
require deploy host/user/key credentials before running apply mode. It must not
reboot hosts, change GPU ownership, or touch co-tenant services.

Run the non-invasive preflight before any manual deploy:

```bash
deploy/preflight.sh --repo marius-patrik/inference-engine --tag v0.1.1
deploy/preflight.sh --repo marius-patrik/inference-engine --tag v0.1.1 --health
```

The health mode probes endpoints only; it does not restart or mutate services.

For local/global harness installs, `agents update` now targets the latest GitHub
release by default. It fetches tags and fast-forwards only; dirty worktrees fail
unless `--allow-dirty` is passed. Use `--release <tag>` to pin a release, or
`--no-release-check` to update from the current branch instead.

## Anti-runaway rules

1. The **Go daemon is the sole scheduler**.
2. K8s Job dispatch stays **fixed and bounded** by the daemon cap.
3. Workflow triggers stay **record/control only**; do not reintroduce `schedule`-driven loops.
4. QFT automation must remain honest about wired-vs-live-proven state and about target-vs-current architecture.

## Authentication

Prefer **GitHub App** tokens for issue/PR/check operations. See
`.user/projects/qft/docs/github-app-setup.md`.

## Operator guidance

- Treat this directory as deployment support for the **k8s executor path plus the remaining cluster bring-up**.
- Do not document k3s/NVCF as already stood up unless it has been verified live.
- Keep `README.md` here aligned with the root `README.md`, `engine-go/README.md`, and evidence under `docs/`
  whenever execution topology changes.

