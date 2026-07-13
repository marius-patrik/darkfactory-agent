# Managed Enforcement

This document records the live enforcement and compensating-control posture for
Andromeda issue [#203](https://github.com/marius-patrik/Andromeda/issues/203).
It describes repository policy and verification evidence; provider routing,
baton ownership, and work dispatch remain outside this scope.

## Protected Andromeda branches

Andromeda uses `dev` for feature integration and `main` for releases. On
2026-07-13 both branches were configured with strict branch protection:

- required status checks: `Validate` and `Codex Review`;
- force pushes: disabled;
- branch deletion: disabled;
- repository auto-merge: enabled, with protected merges waiting for both checks
  and an up-to-date branch;
- separate pull-request approval: not required; `Codex Review` is the automated
  review gate;
- administrator enforcement: disabled; automation does not use the
  administrator bypass.

Disposable evidence PRs proved that red changes are blocked on both branches:

- [#204](https://github.com/marius-patrik/Andromeda/pull/204) targeted `dev`;
  `Validate` and `Codex Review` failed and GitHub reported
  `mergeStateStatus=BLOCKED`.
- [#205](https://github.com/marius-patrik/Andromeda/pull/205) targeted `main`;
  `Validate` and `Codex Review` failed and GitHub reported
  `mergeStateStatus=BLOCKED`.

The probe PRs and their branches were closed and deleted after the evidence was
captured.

## Managed Validate baseline

The managed `Validate` workflow provisions Bun, Go, and uv before dependency
installation and validation. Python package operations use the `uv` CLI rather
than `python -m uv`.

The pre-convergence `go.work` exception is obsolete. The Go contract module is
now inside this repository at `packages/core/contracts-go/go.mod`; validation no
longer depends on a sibling contracts checkout, and the monorepo has no root
`go.work` file to conditionally discover.

## Base-trusted Codex Review bootstrap

`Codex Review` uses `pull_request_target`, so its workflow and image inputs are
trusted infrastructure. The workflow checks out the exact PR base SHA and
builds the image before checking out the PR head. A complete base builds its
own image inputs. If any managed image input is absent, the workflow checks out
the same three assets from immutable Andromeda commit
`0040bc60d76ee251feb25d4eacfb04eff1e40e43` and builds that trusted bootstrap.

The PR checkout is never an image-build context. It is mounted read-only only
after the image exists. This lets a PR introduce or repair managed image inputs
without executing or building from untrusted PR content.

## Credential-isolated review takeover

The landed #148 and #152-#162 implementation was rechecked criterion by
criterion:

| Criterion | Enforced by |
| --- | --- |
| Export the exact immutable prompt on primary automation failure | `run-codex-review.sh` exports the prompt, seals it with SHA-256, and rejects mutation before takeover. |
| Invoke Kimi only for automation failure | Exit code `42` is the sole takeover signal; a schema-valid changes-required Codex result remains authoritative. |
| Keep checkout read-only and credentials isolated | The PR workspace is mounted `:ro`; Codex and Kimi credentials exist in separate workflow steps. |
| Give Kimi no filesystem or tool authority | Kimi receives one HTTP chat request with the sealed prompt and no `tools` field. |
| Normalize the result | `run-kimi-review.mjs` validates and emits the existing `approved`, `summary`, `blocking_findings`, and `non_blocking_notes` schema. |
| Refresh OAuth without exposing plaintext | Refresh occurs in memory; rotated credentials move through an in-memory stdin pipe to the trusted GitHub secret API and are never logged or written to the workspace. |
| Preserve future takeover context and isolation | `skills/orchestrator/SKILL.md` requires the same canonical session, immutable context, provider credential isolation, read-only fallback judgment, and trusted rotation persistence. |
| Regression coverage | Parser, credential-envelope, no-secret-in-prompt, prompt-integrity, refresh, retry, read-only mount, and no-tools cases run in the authoritative CI gate. |
| Fail closed when both providers fail | The Kimi boundary writes a blocking review on every failure and the final workflow step rejects any unapproved result or blocking finding. |

The live provider paths were rechecked from durable Actions output:

- [#154's Kimi review](https://github.com/marius-patrik/Andromeda/pull/154#issuecomment-4951840764)
  is a schema-normalized, approved quota-takeover verdict.
- [#153's failed takeover](https://github.com/marius-patrik/Andromeda/pull/153#issuecomment-4951808059)
  remained changes-required when the exported prompt was unreadable, proving
  the two-provider failure path stayed closed.
- [#204's primary Codex review](https://github.com/marius-patrik/Andromeda/pull/204#issuecomment-4958253878)
  produced a valid changes-required verdict and did not invoke Kimi.

The recheck found no residual behavior gap; #203 adds explicit read-only-mount
and no-tools regression assertions.

## Andromeda-data protection posture

`marius-patrik/Andromeda-data` is a private repository. On 2026-07-13 the
branch-protection API returned HTTP 403 with GitHub's plan decision:
"Upgrade to GitHub Pro or make this repository public to enable this feature."
Native required checks therefore cannot protect its `main` branch under the
current plan.

The compensating control is admission enforcement in Andromeda, merged in
[#190](https://github.com/marius-patrik/Andromeda/pull/190), together with the
Andromeda-data contract merged in
[Andromeda-data #1](https://github.com/marius-patrik/Andromeda-data/pull/1):

- mutable Agent OS state may be backed up only as authenticated encrypted event
  bundles under `backups/events/<machine>/<payload-hash>.bundle.json`;
- tracked plaintext credentials, provider state, memory, sessions, runtime
  state, projections, synchronization material, and similar sensitive paths are
  rejected before status, backup, restore, or synchronization can proceed;
- every tracked bundle is inspected and its authenticated payload hash must
  match its content-addressed filename before import;
- static repository policy and documentation may remain tracked, but they are
  not mutable personal state.

No billing or visibility change was made for #203. Enabling native branch
protection requires Patrik to choose either a plan upgrade or public visibility;
that owner decision remains explicit rather than being inferred by automation.
