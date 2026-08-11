#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct HEAD)}"

sboms=()
while IFS= read -r sbom; do
  sboms+=("$sbom")
done < <(find dist -maxdepth 1 -type f -name '*.sbom.json' -print | LC_ALL=C sort)
[[ "${#sboms[@]}" -eq 6 ]] || { echo "expected 6 native archive SBOMs" >&2; exit 1; }
node scripts/release/merge-spdx-components.mjs "${sboms[@]}"

artifacts=()
while IFS= read -r artifact; do
  artifacts+=("$artifact")
done < <(find dist -maxdepth 1 -type f '(' -name '*.tar.gz' -o -name '*.zip' -o -name '*.sbom.json' ')' -print | LC_ALL=C sort)
[[ "${#artifacts[@]}" -eq 12 ]] || { echo "expected 6 archives and 6 native SBOMs" >&2; exit 1; }
: >dist/checksums.txt
for artifact in "${artifacts[@]}"; do
  shasum -a 256 "$artifact" | awk '{print $1 "  " substr($2, 6)}' >>dist/checksums.txt
done
