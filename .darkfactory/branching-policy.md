# DarkFactory Branching Policy

Managed code repositories use `dev` as the integration branch and `main` as the release branch.

- Work pull requests target `dev`.
- `dev` to `main` pull requests are releases only.
- Merging to `main` should correspond to tag and GitHub release automation where the repository ships releases.
- Both `dev` and `main` use strict branch protection with the `Validate` and
  `Codex Review` status checks required.
- Force pushes and branch deletion are disabled on both protected branches.
- Repository auto-merge may be enabled, but a queued merge lands only after the
  protected branch is current and both required checks report success.
- A separate pull-request approval is not required; the required `Codex Review`
  check is the automated review gate. Repository administrators retain GitHub's
  configured protection bypass, but automation never uses that bypass.
- State and data repositories may use continuous `main` only when their own
  documented protection and admission policy permits it.

The live Andromeda settings and evidence for this policy are recorded in
[`docs/managed-enforcement.md`](../docs/managed-enforcement.md).
