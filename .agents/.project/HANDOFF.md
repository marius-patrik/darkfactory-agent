# Handoff

## Active reconciliation

Branch: `reconcile/single-agent-os`

The current change removes copied global agent files, managed-version checks,
old data/workspace aliases, and DarkFactory's independent provider/model
registry. Local workers now require a healthy Agent OS installation and invoke
`agents` without provider or model flags.

Before handoff, run `npm run check` and confirm the no-drift search finds no
retired product names or copied global-agent paths.
