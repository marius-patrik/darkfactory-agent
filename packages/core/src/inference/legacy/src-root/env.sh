#!/usr/bin/env bash
# Runtime payload environment for every local agent/provider CLI.

_agents_env_src="${BASH_SOURCE[0]:-$0}"
_agents_env_dir="$(cd "$(dirname "$_agents_env_src")" >/dev/null 2>&1 && pwd)"
export AGENTS_ROOT="${AGENTS_ROOT:-$_agents_env_dir}"
unset _agents_env_src _agents_env_dir

# The development checkout is intentionally distinct from the runtime payload.
export AGENTS_REPO="${AGENTS_REPO:-/home/patrik/Projects/agents}"
unset AGENTS_REPO_MIRROR
export AGENTS_HOME="$AGENTS_ROOT"
export AGENTS_GATEWAY_URL="${AGENTS_GATEWAY_URL:-http://s001:4000}"

# Provider CLIs must use the same physical runtime root. Do not point these at
# ~/.claude, ~/.codex, ~/.gemini, ~/.kimi, or repo-local provider folders.
export CLAUDE_CONFIG_DIR="$AGENTS_ROOT"
export CODEX_HOME="$AGENTS_ROOT"
export KIMI_SHARE_DIR="$AGENTS_ROOT"
export GEMINI_ANTIGRAVITY_HOME="$AGENTS_ROOT"
export AGENTS_LOCAL_HOME="$AGENTS_ROOT"
export AGENTS_RUNNER_HOME="$AGENTS_ROOT"

case ":$PATH:" in
  *":$AGENTS_ROOT/bin:"*) ;;
  *) export PATH="$AGENTS_ROOT/bin:$PATH" ;;
esac
