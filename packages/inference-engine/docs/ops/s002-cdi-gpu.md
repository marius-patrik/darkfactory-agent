# s002 — GPU-in-Docker via CDI (reboot fragility + fix)

**Status:** known fragility surfaced during VS1 bring-up (2026-06-12). Worked around live; the durable fix lands in the VS3 deploy config but is captured here so a reboot before then doesn't silently break inference.

## Symptom
`docker run --gpus all …` (or `--device nvidia.com/gpu=all …`) fails with:
```
could not select device driver "" with capabilities: [[gpu]]
```
even though host `nvidia-smi` works and `nvidia-container-toolkit` is installed.

## Root cause
s002 is NixOS. The `nvidia-container-toolkit` CDI generator runs at activation and writes a valid spec to `/run/cdi/nvidia-container-toolkit.json`, but **the Docker daemon does not pick up the CDI spec unless it (re)starts after the spec exists**. On boot the ordering isn't guaranteed, so dockerd can come up before/without the CDI devices registered → GPU invisible to containers.

## Immediate workaround (live, no rebuild)
```bash
ssh s002.ts 'sudo systemctl restart docker.service'
# verify:
ssh s002.ts 'docker run --rm --device nvidia.com/gpu=all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi --query-gpu=name --format=csv,noheader'
# → NVIDIA GeForce RTX 3090
```
Engines must then be (re)started, since a docker restart stops the `rommie-engine-*` containers.

## Durable fix (commit before any planned s002 reboot — finding from the VS0/VS1 review)
NixOS module ensuring dockerd starts after CDI generation. Pattern (adapt to s002's actual config layout / option names before applying — DO NOT blind-apply to the shared host):
```nix
# hardware.nvidia-container-toolkit.enable = true;  # generates /run/cdi/*.json
systemd.services.docker = {
  after = [ "nvidia-container-toolkit-cdi-generator.service" ];
  wants = [ "nvidia-container-toolkit-cdi-generator.service" ];
};
# Belt-and-suspenders: reload CDI if the spec is newer than dockerd start.
```
Then `nixos-rebuild switch` and reboot-test once. **Gate:** this touches `/etc/nixos` on a shared host — co-tenant-safe (additive) but verify the exact service name (`nvidia-container-toolkit-cdi-generator.service` vs the installed unit) on s002 first.

## Why not now
Requires a `nixos-rebuild switch` (a system config change on the shared host) + a reboot-test to validate — best done as a deliberate VS2/VS3 deploy task, not mid-review. Until then: **don't reboot s002 without re-running the workaround after**, and the live working core stays up.
