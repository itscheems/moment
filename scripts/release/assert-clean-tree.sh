#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

status="$(git status --porcelain --untracked-files=all)"
if [[ -n "$status" ]]; then
  echo "release evidence requires a clean source tree" >&2
  exit 1
fi
