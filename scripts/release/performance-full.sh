#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

validation_only=0
if (( $# > 1 )); then
  echo "usage: $0 [--validation-only]" >&2
  exit 2
fi
case "${1:-}" in
  "") ;;
  --validation-only) validation_only=1 ;;
  *) echo "usage: $0 [--validation-only]" >&2; exit 2 ;;
esac

bash scripts/release/assert-clean-tree.sh

host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
if (( validation_only == 0 )) && [[ "$host_os/$host_arch" != linux/amd64 ]]; then
  echo "full-scale qualification requires the declared Linux/amd64 release runner" >&2
  exit 1
fi

available_kib="$(df -Pk . | awk 'NR==2 {print $4}')"
required_kib=$((110 * 1024 * 1024))
if (( available_kib < required_kib )); then
  echo "release-scale qualification requires at least 110 GiB free; found $((available_kib / 1024 / 1024)) GiB" >&2
  exit 1
fi

mkdir -p dist
if (( validation_only == 1 )); then
  evidence="$(cd dist && pwd -P)/performance-validation-evidence.json"
else
  evidence="$(cd dist && pwd -P)/performance-evidence.json"
fi
rm -f -- "$evidence"
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
commit="$(git rev-parse HEAD)"
binary_sha256="$(shasum -a 256 "$binary" | awk '{print $1}')"
metadata="$($binary version --json)"
binary_version="$(jq -er '.data.version' <<<"$metadata")"
binary_commit="$(jq -er '.data.commit' <<<"$metadata")"
binary_build_date="$(jq -er '.data.build_date' <<<"$metadata")"
[[ "$binary_commit" == "$commit" ]] || { echo "performance binary commit does not match source HEAD" >&2; exit 1; }

MOMENT_RUN_DEV_SCALE=1 MOMENT_PERFORMANCE_VALIDATION_ONLY="$validation_only" MOMENT_PERFORMANCE_BINARY="$binary" MOMENT_PERFORMANCE_WORKDIR="$work" MOMENT_PERFORMANCE_EVIDENCE="$evidence" \
  go test -count=1 -timeout=90m -v ./internal/devtools -run '^TestAcceptanceScaleEndToEnd$'
[[ -s "$evidence" ]] || { echo "performance evidence was not produced" >&2; exit 1; }
if (( validation_only == 1 )); then
  jq -e \
    --arg commit "$commit" \
    --arg goos "$host_os" \
    --arg goarch "$host_arch" \
    --arg version "$binary_version" \
    --arg build_date "$binary_build_date" \
    --arg binary_sha256 "$binary_sha256" '
    .qualified == false and .validation_only == true and .commit == $commit and
    .binary.version == $version and .binary.commit == $commit and
    .binary.build_date == $build_date and .binary.sha256 == $binary_sha256 and
    .binary.go_version == "go1.25.12" and
    .environment.goos == $goos and .environment.goarch == $goarch and
    .environment.go_version == "go1.25.12" and .environment.gomaxprocs > 0 and
    .environment.rss_sample_interval_ms == 25 and
    .fixture.probe_scenario == "timeline-empty" and .fixture.probe_stage_timeout == "30m" and
    .fixture.probe_timeout_no_partial == true and .fixture.probe_archive_round_trip == true and
    .fixture.probe_deep_doctor == true and .fixture.recipe_version == "moment-dev-v1" and
    .fixture.entries == 10000 and .fixture.comments == 50000 and .fixture.media == 20000 and
    .fixture.media_bytes == 21474836480 and .fixture.peak_rss_bytes > 0 and
    .fixture.rss_limit_bytes == 805306368 and .fixture.peak_rss_bytes < .fixture.rss_limit_bytes and
    .latency.timeline.samples == 31 and .latency.timeline.limit_ms == 100 and .latency.timeline.p95_ms < 100 and
    .latency.search.samples == 31 and .latency.search.limit_ms == 200 and .latency.search.p95_ms < 200 and
    .latency.archive.samples == 31 and .latency.archive.limit_ms == 250 and .latency.archive.p95_ms < 250 and
    .streaming.export.bytes > .fixture.media_bytes and
    .streaming.export.bytes == .streaming.import.bytes and .streaming.import.bytes == .streaming.import_validation.bytes and
    .streaming.export.limit_bytes == 805306368 and .streaming.import.limit_bytes == 805306368 and
    .streaming.import_validation.limit_bytes == 805306368 and
    .streaming.export.peak_rss_bytes > 0 and .streaming.export.peak_rss_bytes < .streaming.export.limit_bytes and
    .streaming.import.peak_rss_bytes > 0 and .streaming.import.peak_rss_bytes < .streaming.import.limit_bytes and
    .streaming.import_validation.peak_rss_bytes > 0 and
    .streaming.import_validation.peak_rss_bytes < .streaming.import_validation.limit_bytes and
    .streaming.export.elapsed_ms > 0 and .streaming.import.elapsed_ms > 0 and
    .streaming.import_validation.elapsed_ms > 0 and .fixture_elapsed_ms > 0 and .total_elapsed_ms > 0 and
    (.archive_sha256 | test("^[0-9a-f]{64}$")) and
    .environment.disk_available_before_bytes >= 118111600640 and
    .environment.minimum_disk_available_bytes > 0 and
    .environment.minimum_disk_available_bytes <= .environment.disk_available_after_bytes and
    .environment.peak_disk_used_bytes == (.environment.disk_available_before_bytes - .environment.minimum_disk_available_bytes)
  ' "$evidence" >/dev/null
else
  jq -e '
    .qualified == true and .validation_only == false and
    .environment.goos == "linux" and .environment.goarch == "amd64" and
    .fixture.media_bytes == 21474836480
  ' "$evidence" >/dev/null
fi
