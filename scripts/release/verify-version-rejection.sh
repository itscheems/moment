#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

if MOMENT_EXPECTED_VERSION=definitely-not-this-release MOMENT_REQUIRE_QUALIFICATION=0 MOMENT_VERIFY_ONLY=1 \
  bash scripts/release/verify-artifacts.sh >/dev/null 2>&1; then
  echo "artifact verifier accepted an unrelated release version" >&2
  exit 1
fi
