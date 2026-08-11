#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

bash scripts/release/assert-clean-tree.sh

scratch="$(mktemp -d "${TMPDIR:-/tmp}/moment-reproducible.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT

goreleaser check
bash scripts/release/goreleaser-build.sh --snapshot --clean --skip=publish
bash scripts/release/finalize-artifacts.sh
[[ "$(awk 'NF == 2 && $1 ~ /^[0-9a-f]{64}$/ {count++} END {print count+0}' dist/checksums.txt)" -eq 12 ]] || {
  echo "snapshot checksum manifest must bind 6 archives and 6 SBOMs" >&2
  exit 1
}
cp dist/checksums.txt "$scratch/first-checksums.txt"

bash scripts/release/goreleaser-build.sh --snapshot --clean --skip=publish
bash scripts/release/finalize-artifacts.sh
cmp "$scratch/first-checksums.txt" dist/checksums.txt
[[ "$(find dist -maxdepth 1 -type f -name '*.sbom.json' | wc -l | tr -d ' ')" -eq 6 ]] || {
  echo "second snapshot did not produce 6 archive SBOMs" >&2
  exit 1
}
