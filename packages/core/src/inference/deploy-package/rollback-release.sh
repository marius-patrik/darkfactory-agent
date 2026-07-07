#!/usr/bin/env bash
# Guarded rollback planner. Mutates only with --apply.

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEPLOY_DIR/../.." && pwd)"
TO_TAG=""
APPLY=0
EVIDENCE=""

usage() {
  cat <<'EOF'
usage: deploy/rollback-release.sh --to-tag vX.Y.Z [--evidence path] [--apply]

Default mode is read-only: write rollback evidence and print the exact
deploy-release command that would roll agents services back to the tag. With
--apply, it first captures evidence, then calls deploy-release.sh --apply.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --to-tag)
      TO_TAG="${2:?missing tag}"
      shift 2
      ;;
    --evidence)
      EVIDENCE="${2:?missing evidence path}"
      shift 2
      ;;
    --apply)
      APPLY=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$TO_TAG" ]]; then
  echo "--to-tag is required" >&2
  usage >&2
  exit 2
fi

if [[ -z "$EVIDENCE" ]]; then
  EVIDENCE="$REPO_ROOT/dist/deploy/$TO_TAG/rollback-before.json"
fi

"$DEPLOY_DIR/rollback-evidence.sh" --tag "$TO_TAG" --output "$EVIDENCE" >/dev/null

cmd=("$DEPLOY_DIR/deploy-release.sh" --tag "$TO_TAG" --evidence "$REPO_ROOT/dist/deploy/$TO_TAG/rollback-apply.json")
if [[ "$APPLY" -eq 1 ]]; then
  cmd+=(--apply)
  exec "${cmd[@]}"
fi

printf 'rollback evidence: %s\n' "$EVIDENCE"
printf 'dry-run rollback command:'
printf ' %q' "${cmd[@]}"
printf '\n'

