# Dialogue Log

## 2026-07-02

The user first asked to install Vibe Bot into `template-repo`, then corrected the request to copy the `.agents` folder into `vibe-bot` and call it `.agents`.

The user clarified that Vibe Bot should enforce `.agents/.global` freshness in any repository where the bot is installed.

The user added that `.github` should get similar treatment, then clarified that for `.github`, bootstrap is more important than up-to-date version parity.

This branch adds `.agents/.global` and `.agents/.project` to `vibe-bot`, version-enforces `.agents/.global/VERSION`, and bootstrap-enforces `.github/workflows/ci.yml`.
