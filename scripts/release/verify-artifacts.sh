#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

bash scripts/release/assert-clean-tree.sh

commit="$(git rev-parse HEAD)"
commit_epoch="$(git show -s --format=%ct HEAD)"
source_epoch="${SOURCE_DATE_EPOCH:-$commit_epoch}"
[[ "$source_epoch" == "$commit_epoch" ]] || { echo "SOURCE_DATE_EPOCH must equal the source commit timestamp" >&2; exit 1; }
expected_build_date="$(node -e 'process.stdout.write(new Date(Number(process.argv[1]) * 1000).toISOString().replace(".000Z", "Z"))' "$source_epoch")"
expected_version="${MOMENT_EXPECTED_VERSION:-}"

[[ -f dist/checksums.txt ]] || { echo "missing dist/checksums.txt" >&2; exit 1; }
(cd dist && shasum -a 256 -c checksums.txt)

mapfile_compat() {
  local array_name="$1"
  shift
  local line
  eval "$array_name=()"
  # shellcheck disable=SC2034 # eval assigns this line to the Bash 3-compatible destination array.
  while IFS= read -r line; do
    eval "$array_name+=(\"\$line\")"
  done < <("$@")
}

archives=()
mapfile_compat archives find dist -maxdepth 1 -type f '(' -name '*.tar.gz' -o -name '*.zip' ')' -print
[[ "${#archives[@]}" -eq 6 ]] || { echo "expected 6 release archives, found ${#archives[@]}" >&2; exit 1; }

scratch="$(mktemp -d "${TMPDIR:-/tmp}/moment-artifact.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
native_binary=""
: >"$scratch/targets.txt"

for archive in "${archives[@]}"; do
  if [[ "$archive" == *.zip ]]; then
    members="$(unzip -Z1 "$archive" | LC_ALL=C sort)"
    expected=$'README.md\nTHIRD_PARTY_LICENSES.txt\nTHIRD_PARTY_NOTICES.md\nmoment.exe'
  else
    members="$(tar -tzf "$archive" | sed 's#^\./##' | LC_ALL=C sort)"
    expected=$'README.md\nTHIRD_PARTY_LICENSES.txt\nTHIRD_PARTY_NOTICES.md\nmoment'
  fi
  [[ "$members" == "$expected" ]] || {
    echo "unexpected archive members in $archive" >&2
    printf '%s\n' "$members" >&2
    exit 1
  }
  archive_name="$(basename "$archive")"
  target=""
  for candidate in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
    target_name="${candidate//\//_}"
    if [[ "$archive_name" == *"_${target_name}."* ]]; then
      target="$candidate"
      break
    fi
  done
  [[ -n "$target" ]] || { echo "archive name does not identify a supported target: $archive_name" >&2; exit 1; }
  if [[ -n "$expected_version" && "$archive_name" != *"_${expected_version}_${target//\//_}."* ]]; then
    echo "archive $archive_name does not bind expected version $expected_version" >&2
    exit 1
  fi
  printf '%s\n' "$target" >>"$scratch/targets.txt"
  target_root="$scratch/${target//\//_}"
  mkdir -p "$target_root"
  if [[ "$archive" == *.zip ]]; then
    unzip -q "$archive" -d "$target_root"
    target_binary="$target_root/moment.exe"
  else
    tar -xzf "$archive" -C "$target_root"
    target_binary="$target_root/moment"
  fi
  [[ -f "$target_binary" ]] || { echo "missing binary for $target" >&2; exit 1; }
  build_info="$target_root/buildinfo.txt"
  go version -m "$target_binary" >"$build_info"
  grep -F $'\tpath\tgithub.com/moment/moment/cmd/moment' "$build_info" >/dev/null
  grep -F $'\tbuild\t-trimpath=true' "$build_info" >/dev/null
  grep -F $'\tbuild\tCGO_ENABLED=0' "$build_info" >/dev/null
  grep -F $'\tbuild\tGOOS='"${target%/*}" "$build_info" >/dev/null
  grep -F $'\tbuild\tGOARCH='"${target#*/}" "$build_info" >/dev/null
  if grep -F $'\tbuild\tvcs.' "$build_info" >/dev/null; then
    echo "release binary contains non-authoritative automatic VCS metadata: $target" >&2
    exit 1
  fi
  grep -aF "$commit" "$target_binary" >/dev/null
  grep -aF "$expected_build_date" "$target_binary" >/dev/null
  if [[ -n "$expected_version" ]]; then
    grep -aF "$expected_version" "$target_binary" >/dev/null
  fi
  while read -r module version; do
    jq -e --arg module "$module" --arg version "$version" '
      any(.components[]; .ecosystem == "Go" and .name == $module and .version == $version)
    ' release/third-party-components.json >/dev/null || {
      echo "release binary dependency is missing from the offline license bundle: $module@$version" >&2
      exit 1
    }
  done < <(awk '$1 == "dep" {print $2, $3}' "$build_info")
  if [[ "$target" == "$host_os/$host_arch" ]]; then
    native_binary="$target_binary"
  fi
done

expected_targets=$'darwin/amd64\ndarwin/arm64\nlinux/amd64\nlinux/arm64\nwindows/amd64\nwindows/arm64'
actual_targets="$(LC_ALL=C sort "$scratch/targets.txt")"
[[ "$actual_targets" == "$expected_targets" ]] || { echo "release target set is incomplete" >&2; exit 1; }

sboms=()
mapfile_compat sboms find dist -maxdepth 1 -type f -name '*.sbom.json' -print
[[ "${#sboms[@]}" -eq 6 ]] || { echo "expected 6 archive SBOMs, found ${#sboms[@]}" >&2; exit 1; }
for sbom in "${sboms[@]}"; do
  archive_path="${sbom%.sbom.json}"
  [[ -f "$archive_path" ]] || { echo "SBOM does not correspond to an archive: $sbom" >&2; exit 1; }
  archive_name="$(basename "$archive_path")"
  sbom_target=""
  for candidate in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
    if [[ "$archive_name" == *"_${candidate//\//_}."* ]]; then
      sbom_target="$candidate"
      break
    fi
  done
  [[ -n "$sbom_target" ]] || { echo "SBOM archive target is unknown: $sbom" >&2; exit 1; }
  jq -e '
    .spdxVersion == "SPDX-2.3" and
    any(.packages[]; .name == "github.com/moment/moment" or .name == "moment") and
    any(.packages[]; .name == "github.com/gin-gonic/gin") and
    any(.packages[]; .name == "Go standard library") and
    any(.packages[]; .name == "react") and
    any(.packages[]; .name == "@base-ui/react")
  ' "$sbom" >/dev/null
  while read -r module version; do
    jq -e --arg module "$module" --arg version "$version" '
      any(.packages[]; .name == $module and .versionInfo == $version)
    ' "$sbom" >/dev/null || {
      echo "SBOM $sbom is missing binary dependency $module@$version" >&2
      exit 1
    }
  done < <(awk '$1 == "dep" {print $2, $3}' "$scratch/${sbom_target//\//_}/buildinfo.txt")
  while IFS=$'\t' read -r name version purl; do
    jq -e --arg name "$name" --arg version "$version" --arg purl "$purl" '
      any(.packages[];
        .name == $name and .versionInfo == $version and
        any(.externalRefs[]?; .referenceType == "purl" and .referenceLocator == $purl)
      )
    ' "$sbom" >/dev/null || {
      echo "SBOM $sbom is missing embedded dependency $name@$version" >&2
      exit 1
    }
  done < <(jq -r '.components[] | select(.ecosystem == "Web" or .name == "Go standard library") | [.name,.version,.purl] | @tsv' release/third-party-components.json)
  grype "sbom:$sbom" --fail-on high
done

manifest_count="$(awk 'NF == 2 && $1 ~ /^[0-9a-f]{64}$/ {count++} END {print count+0}' dist/checksums.txt)"
[[ "$manifest_count" -eq 12 ]] || { echo "expected checksums for 6 archives and 6 SBOMs, found $manifest_count" >&2; exit 1; }
for artifact in "${archives[@]}" "${sboms[@]}"; do
  artifact_name="$(basename "$artifact")"
  awk -v name="$artifact_name" '$2 == name {found++} END {exit found == 1 ? 0 : 1}' dist/checksums.txt || {
    echo "checksum manifest does not bind exactly one $artifact_name" >&2
    exit 1
  }
done

[[ -n "$native_binary" && -x "$native_binary" ]] || { echo "native archive binary is not executable" >&2; exit 1; }
native_sha256="$(shasum -a 256 "$native_binary" | awk '{print $1}')"

bash scripts/release/check-vulnerabilities.sh
metadata="$($native_binary version --json)"
jq -e --arg commit "$commit" --arg build_date "$expected_build_date" --arg version "$expected_version" '
  .ok == true and .data.version != "dev" and .data.commit == $commit and .data.build_date == $build_date and
  ($version == "" or .data.version == $version)
' <<<"$metadata" >/dev/null
release_version="$(jq -er '.data.version' <<<"$metadata")"

MOMENT_SMOKE_BINARY="$native_binary" go test -count=1 ./cmd/moment -run '^(TestServeBinaryStructuredLogsAndGracefulSignal|TestInitAliasAndBrokenPipeBinaryContracts)$'

checksums="$(cat dist/checksums.txt)"
artifact_hashes="$(awk '{print "{\"sha256\":\"" $1 "\",\"name\":\"" $2 "\"}"}' dist/checksums.txt | jq -sS 'sort_by(.name)')"
vulnerability_policy="$(jq -cS . release/vulnerability-exemptions.json)"
vulnerability_policy_sha256="$(shasum -a 256 release/vulnerability-exemptions.json | awk '{print $1}')"

verify_performance_evidence() {
  jq -e --arg commit "$commit" --arg binary_sha256 "$native_sha256" --arg version "$release_version" --arg build_date "$expected_build_date" '
    .qualified == true and .validation_only == false and .commit == $commit and .binary.commit == $commit and
    .binary.sha256 == $binary_sha256 and .binary.version == $version and .binary.build_date == $build_date and
    .binary.go_version == "go1.25.12" and
    .environment.goos == "linux" and .environment.goarch == "amd64" and .environment.go_version == "go1.25.12" and
    .environment.gomaxprocs > 0 and .environment.rss_sample_interval_ms == 25 and
    .fixture.probe_scenario == "timeline-empty" and .fixture.probe_backup_scenario == "post-with-images" and
    .fixture.probe_stage_timeout == "30m" and
    .fixture.probe_timeout_no_partial == true and .fixture.probe_archive_round_trip == true and
    .fixture.probe_deep_doctor == true and .fixture.recipe_version == "moment-dev-v2" and
    .fixture.entries == 10000 and .fixture.comments == 50000 and .fixture.media == 20000 and
    .fixture.media_bytes == 21474836480 and .fixture.peak_rss_bytes > 0 and .fixture.peak_rss_bytes < .fixture.rss_limit_bytes and
    .fixture.rss_limit_bytes == 805306368 and
    .latency.timeline.samples == 31 and .latency.timeline.limit_ms == 100 and .latency.timeline.p95_ms < 100 and
    .latency.search.samples == 31 and .latency.search.limit_ms == 200 and .latency.search.p95_ms < 200 and
    .latency.archive.samples == 31 and .latency.archive.limit_ms == 250 and .latency.archive.p95_ms < 250 and
    .streaming.export.bytes > .fixture.media_bytes and
    .streaming.export.bytes == .streaming.import.bytes and .streaming.import.bytes == .streaming.import_validation.bytes and
    .streaming.export.limit_bytes == 805306368 and .streaming.import.limit_bytes == 805306368 and
    .streaming.import_validation.limit_bytes == 805306368 and
    .streaming.export.peak_rss_bytes > 0 and .streaming.export.peak_rss_bytes < .streaming.export.limit_bytes and
    .streaming.import.peak_rss_bytes > 0 and .streaming.import.peak_rss_bytes < .streaming.import.limit_bytes and
    .streaming.import_validation.peak_rss_bytes > 0 and .streaming.import_validation.peak_rss_bytes < .streaming.import_validation.limit_bytes and
    .streaming.import_validation.target_state == "initialized-empty" and
    .streaming.import_validation.target_unchanged == true and
    .streaming.import_validation.validation_before_actual_import == true and
    .restored_doctor.expected_missing_previews == 20000 and
    .restored_doctor.preview_warning == true and (.restored_doctor.staging_warning | type == "boolean") and
    .streaming.export.elapsed_ms > 0 and .streaming.import.elapsed_ms > 0 and .streaming.import_validation.elapsed_ms > 0 and
    (.archive_sha256 | test("^[0-9a-f]{64}$")) and
    .environment.disk_available_before_bytes >= 118111600640 and
    .environment.minimum_disk_available_bytes > 0 and
    .environment.minimum_disk_available_bytes <= .environment.disk_available_after_bytes and
    .environment.peak_disk_used_bytes == (.environment.disk_available_before_bytes - .environment.minimum_disk_available_bytes)
  ' dist/performance-evidence.json >/dev/null
}

if [[ -f dist/performance-evidence.json ]]; then
  verify_performance_evidence
fi

if [[ "${MOMENT_REQUIRE_QUALIFICATION:-0}" == "1" ]]; then
  [[ -n "$expected_version" ]] || { echo "MOMENT_EXPECTED_VERSION is required for publish qualification" >&2; exit 1; }
  for required in dist/performance-evidence.json dist/release-evidence.json dist/container-evidence.json dist/container.spdx.json; do
    [[ -s "$required" ]] || { echo "missing required qualification evidence: $required" >&2; exit 1; }
  done
  verify_performance_evidence
  jq -e \
    --arg commit "$commit" \
    --arg version "$release_version" \
    --arg epoch "$source_epoch" \
    --arg checksums "$checksums" \
    --arg policy_sha256 "$vulnerability_policy_sha256" \
    --argjson policy "$vulnerability_policy" \
    --argjson hashes "$artifact_hashes" '
      .commit == $commit and .version == $version and .source_date_epoch == ($epoch|tonumber) and
      .artifacts.archive_count == 6 and .artifacts.sbom_count == 6 and
      .artifacts.checksum_manifest == $checksums and .artifacts.hashes == $hashes and
      .vulnerability_policy.sha256 == $policy_sha256 and .vulnerability_policy.document == $policy and
      .environment.go == "go version go1.25.12 linux/amd64" and
      .environment.node == "v24.19.0" and .environment.pnpm == "10.34.5" and
      .environment.goreleaser.git_version == "2.17.1" and
      (.environment.goreleaser.git_commit | test("^[0-9a-f]{40}$")) and
      .environment.goreleaser.git_tree_state == false and .environment.goreleaser.platform == "linux/amd64" and
      .scanners.syft.version == "1.50.0" and .scanners.grype.version == "0.116.1" and
      .scanners.grype_database.valid == true and (.scanners.grype_database | has("path") | not) and
      (.scanners.govulncheck | contains("govulncheck@v1.6.0")) and
      ([.scanners | .. | strings | select(startswith("/") or test("^[A-Za-z]:[\\\\/]"))] | length == 0) and
      .verification.archive_members == true and .verification.native_metadata == true and
      .verification.embedded_web_and_migrations == true and .verification.checksums == true and
      .verification.govulncheck_reachable_and_imported_vulnerabilities_absent == true and
      .verification.high_or_critical_vulnerabilities_absent == true
    ' dist/release-evidence.json >/dev/null
  container_sbom_sha256="$(shasum -a 256 dist/container.spdx.json | awk '{print $1}')"
  base_images="$(awk '/^FROM / {print $2}' deploy/Dockerfile | jq -Rsc 'split("\n") | map(select(length > 0))')"
  jq -e --arg commit "$commit" --arg build_date "$expected_build_date" --arg sbom_sha256 "$container_sbom_sha256" --arg policy_sha256 "$vulnerability_policy_sha256" --argjson base_images "$base_images" '
    .commit == $commit and .build_date == $build_date and (.image_id | test("^sha256:[0-9a-f]{64}$")) and .base_images == $base_images and
    all(.base_images[]; test("@sha256:[0-9a-f]{64}$")) and
    .user == "65532:65532" and .data_dir == "/data" and .sbom.sha256 == $sbom_sha256 and
    .scanners.syft.version == "1.50.0" and .scanners.grype.version == "0.116.1" and
    .scanners.grype_database.valid == true and (.scanners.grype_database | has("path") | not) and
    ([.scanners | .. | strings | select(startswith("/") or test("^[A-Za-z]:[\\\\/]"))] | length == 0) and
    .vulnerability_policy_sha256 == $policy_sha256 and
    .verification.anonymous_volume_init == true and .verification.named_volume_init == true and
    .verification.persistence_restart == true and .verification.graceful_sigterm == true and
    .verification.high_or_critical_vulnerabilities_absent == true
  ' dist/container-evidence.json >/dev/null
  jq -e '
    .spdxVersion == "SPDX-2.3" and
    any(.packages[]; .name == "github.com/moment/moment" or .name == "moment") and
    any(.packages[]; .name == "github.com/gin-gonic/gin") and
    any(.packages[]; .name == "react") and
    any(.packages[]; .name == "@base-ui/react") and
    any(.packages[]; .name == "busybox" or .name == "alpine-baselayout")
  ' dist/container.spdx.json >/dev/null
  while read -r module version; do
    jq -e --arg module "$module" --arg version "$version" '
      any(.packages[]; .name == $module and .versionInfo == $version)
    ' dist/container.spdx.json >/dev/null || {
      echo "container SBOM is missing binary dependency $module@$version" >&2
      exit 1
    }
  done < <(awk '$1 == "dep" {print $2, $3}' "$scratch/linux_amd64/buildinfo.txt")
  while IFS=$'\t' read -r name version purl; do
    jq -e --arg name "$name" --arg version "$version" --arg purl "$purl" '
      any(.packages[];
        .name == $name and .versionInfo == $version and
        any(.externalRefs[]?; .referenceType == "purl" and .referenceLocator == $purl)
      )
    ' dist/container.spdx.json >/dev/null || {
      echo "container SBOM is missing embedded dependency $name@$version" >&2
      exit 1
    }
  done < <(jq -r '.components[] | select(.ecosystem == "Web" or .name == "Go standard library") | [.name,.version,.purl] | @tsv' release/third-party-components.json)
fi

if [[ "${MOMENT_VERIFY_ONLY:-0}" == "1" ]]; then
  exit 0
fi

go_version="$(go version)"
node_version="$(node --version)"
pnpm_version="$(pnpm --version)"
goreleaser_output="$(goreleaser --version)"
goreleaser_pin="$(awk -F '"' '$1 ~ /^goreleaser = / {print $2}' .mise.toml)"
goreleaser_git_version="$(awk '$1 == "GitVersion:" {print $2}' <<<"$goreleaser_output")"
[[ "$goreleaser_pin" == "2.17.1" && "$goreleaser_git_version" == "$goreleaser_pin" ]] || {
  echo "GoReleaser version does not match the locked 2.17.1 toolchain" >&2
  exit 1
}
goreleaser_metadata="$(jq -n \
  --arg git_version "$goreleaser_git_version" \
  --arg git_commit "$(awk '$1 == "GitCommit:" {print $2}' <<<"$goreleaser_output")" \
  --arg git_tree_state "$(awk '$1 == "GitTreeState:" {print $2}' <<<"$goreleaser_output")" \
  --arg build_date "$(awk '$1 == "BuildDate:" {print $2}' <<<"$goreleaser_output")" \
  --arg go_version "$(awk '$1 == "GoVersion:" {print $2}' <<<"$goreleaser_output")" \
  --arg platform "$(awk '$1 == "Platform:" {print $2}' <<<"$goreleaser_output")" \
  '{git_version:$git_version,git_commit:$git_commit,git_tree_state:($git_tree_state == "true"),build_date:$build_date,go_version:$go_version,platform:$platform}')"
syft_version="$(syft version -o json)"
grype_version="$(grype version -o json)"
grype_db="$(grype db status -o json | jq -c '{schemaVersion,from,built,valid}')"
govulncheck_version="$(govulncheck -version)"
jq -n \
  --arg commit "$commit" \
  --arg version "$release_version" \
  --arg source_date_epoch "$source_epoch" \
  --arg go "$go_version" \
  --arg node "$node_version" \
  --arg pnpm "$pnpm_version" \
  --argjson goreleaser "$goreleaser_metadata" \
  --arg govulncheck "$govulncheck_version" \
  --argjson syft "$syft_version" \
  --argjson grype "$grype_version" \
  --argjson grype_database "$grype_db" \
  --argjson vulnerability_policy "$vulnerability_policy" \
  --arg vulnerability_policy_sha256 "$vulnerability_policy_sha256" \
  --arg checksums "$checksums" \
  --argjson artifact_hashes "$artifact_hashes" \
  '{schema_version:1, commit:$commit, version:$version, source_date_epoch:($source_date_epoch|tonumber), environment:{go:$go,node:$node,pnpm:$pnpm,goreleaser:$goreleaser}, scanners:{syft:$syft,grype:$grype,grype_database:$grype_database,govulncheck:$govulncheck}, vulnerability_policy:{document:$vulnerability_policy,sha256:$vulnerability_policy_sha256}, artifacts:{archive_count:6,sbom_count:6,checksum_manifest:$checksums,hashes:$artifact_hashes}, verification:{archive_members:true,native_metadata:true,embedded_web_and_migrations:true,checksums:true,govulncheck_reachable_and_imported_vulnerabilities_absent:true,high_or_critical_vulnerabilities_absent:true}}' \
  >dist/release-evidence.json
