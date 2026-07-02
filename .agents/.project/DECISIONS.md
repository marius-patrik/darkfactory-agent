# Decisions

## 2026-07-02

### Reusable Agent Split

- Decision: Separate agent files into `.agents/.global/` and `.agents/.project/`.
- Rationale: Shared agent operating content should be reusable across repositories, while project facts remain easy to replace.
- Status: Accepted.

### Agents Global Version Enforcement

- Decision: Vibe Bot checks `.agents/.global/VERSION` in pull requests for every installed repository.
- Rationale: Repositories should keep shared agent operating rules in sync with the Vibe Bot canonical version.
- Status: Accepted.

### GitHub Bootstrap Enforcement

- Decision: Vibe Bot checks for `.github/workflows/ci.yml` as bootstrap scaffolding, but does not version-enforce `.github` yet.
- Rationale: For `.github`, getting baseline CI installed matters more than exact up-to-date parity.
- Status: Accepted.

### Runtime Agent Metadata Excluded

- Decision: Do not commit `.agents/env`, `.agents/credits.json`, or `.agents/installs.json`.
- Rationale: Existing repositories show these as runtime or local agent metadata, not portable project instructions.
- Status: Accepted.
