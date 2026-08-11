#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

report="$(mktemp "${TMPDIR:-/tmp}/moment-govulncheck.XXXXXX")"
trap 'rm -f -- "$report"' EXIT

govulncheck -json ./... >"$report"
jq -se '
  ([.[] | select(has("config")) | .config] | length == 1) and
  ([.[] | select(has("finding")) | .finding] == [{
    osv:"GO-2026-5932",
    trace:[{module:"golang.org/x/crypto",version:"v0.53.0"}]
  }])
' "$report" >/dev/null
