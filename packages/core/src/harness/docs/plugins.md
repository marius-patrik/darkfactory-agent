# Harness Plugin Policy

Agents Harness does not vendor installable memory implementations.

The harness may reference plugin behavior only at runtime integration points:
loading an Agentos-managed plugin path, passing environment/configuration into
the launched runtime, or documenting which package owns a capability.

Ownership:

- `marius-patrik/plugin-rommie` owns installable Rommie memory behavior,
  memory-hygiene skills, and Codex hook templates.
- `marius-patrik/dream` owns retrospective temporal replay.
- `agents-harness` owns only runtime-required harness capability sources. It
  currently carries no sample-only plugin, command, or skill content.

Sample-only plugins, commands, and skills are fixtures or templates, not harness
runtime behavior. They should live in the package that tests or installs them,
such as `agents-mono/os/agents-manager` fixtures or a template/example package.
