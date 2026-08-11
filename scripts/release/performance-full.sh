#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

bash scripts/release/assert-clean-tree.sh

if [[ "$(go env GOOS)" != linux ]]; then
  echo "full-scale qualification requires the declared Linux release runner" >&2
  exit 1
fi

available_kib="$(df -Pk . | awk 'NR==2 {print $4}')"
required_kib=$((110 * 1024 * 1024))
if (( available_kib < required_kib )); then
  echo "release-scale qualification requires at least 110 GiB free; found $((available_kib / 1024 / 1024)) GiB" >&2
  exit 1
fi

mkdir -p dist
evidence="$(cd dist && pwd -P)/performance-evidence.json"
rm -f -- "$evidence"
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
native_archive=""
for candidate in dist/*.tar.gz dist/*.zip; do
  [[ -f "$candidate" ]] || continue
  normalized="$(basename "$candidate" | tr '[:upper:]' '[:lower:]')"
  if [[ "$normalized" == *"_${host_os}_${host_arch}."* ]]; then
    native_archive="$candidate"
    break
  fi
done
[[ -n "$native_archive" ]] || { echo "native release archive not found for ${host_os}/${host_arch}" >&2; exit 1; }
scratch="$(mktemp -d "${TMPDIR:-/tmp}/moment-performance-binary.XXXXXX")"
work="$(mktemp -d "$repo_root/dist/moment-performance-work.XXXXXX")"
cleanup() {
  rm -rf -- "$scratch" "$work"
}
trap cleanup EXIT
if [[ "$native_archive" == *.zip ]]; then
  unzip -q "$native_archive" -d "$scratch"
  binary="$scratch/moment.exe"
else
  tar -xzf "$native_archive" -C "$scratch"
  binary="$scratch/moment"
fi
[[ -x "$binary" ]] || { echo "native release binary is not executable" >&2; exit 1; }

MOMENT_RUN_DEV_SCALE=1 MOMENT_PERFORMANCE_BINARY="$binary" MOMENT_PERFORMANCE_WORKDIR="$work" MOMENT_PERFORMANCE_EVIDENCE="$evidence" \
  go test -count=1 -timeout=90m -v ./internal/devtools -run '^TestAcceptanceScaleEndToEnd$'
[[ -s "$evidence" ]] || { echo "performance evidence was not produced" >&2; exit 1; }
jq -e '.qualified == true and .fixture.media_bytes == 21474836480' "$evidence" >/dev/null
