#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

bash scripts/release/assert-clean-tree.sh

commit="$(git rev-parse HEAD)"
commit_epoch="$(git show -s --format=%ct HEAD)"
build_date="$(node -e 'process.stdout.write(new Date(Number(process.argv[1]) * 1000).toISOString().replace(".000Z", "Z"))' "$commit_epoch")"
version="0.0.0-container-smoke"
image="moment:release-smoke-${commit:0:12}"
volume="moment-release-smoke-${commit:0:12}-$$"
container="moment-release-smoke-${commit:0:12}-$$"
restart_container="${container}-restart"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/moment-container.XXXXXX")"

cleanup() {
  docker rm -f "$container" "$restart_container" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf "$scratch"
}
trap cleanup EXIT

docker build \
  --build-arg "VERSION=$version" \
  --build-arg "COMMIT=$commit" \
  --build-arg "BUILD_DATE=$build_date" \
  --tag "$image" \
  --file deploy/Dockerfile .

docker run --rm --mount type=volume,dst=/data "$image" owner init --data-dir /data --json >"$scratch/anonymous-init.json"
jq -e '.ok == true and (.data.token | length > 0)' "$scratch/anonymous-init.json" >/dev/null

docker volume create "$volume" >/dev/null
docker run --rm --mount "type=volume,src=$volume,dst=/data" "$image" owner init --data-dir /data --json >"$scratch/init.json"
token="$(jq -er '.data.token' "$scratch/init.json")"

docker run -d --name "$container" --mount "type=volume,src=$volume,dst=/data" -p 127.0.0.1::8080 "$image" >/dev/null

user="$(docker inspect --format '{{.Config.User}}' "$container")"
[[ "$user" == "65532:65532" ]] || { echo "container user is $user" >&2; exit 1; }
inside_identity="$(docker exec "$container" id -u):$(docker exec "$container" id -g)"
[[ "$inside_identity" == "65532:65532" ]] || { echo "runtime identity is $inside_identity" >&2; exit 1; }
docker exec "$container" test -s /licenses/THIRD_PARTY_NOTICES.md
docker exec "$container" test -s /licenses/THIRD_PARTY_LICENSES.txt

deadline=$((SECONDS + 60))
while [[ "$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container")" != healthy ]]; do
  (( SECONDS < deadline )) || { docker logs "$container" >&2; exit 1; }
  sleep 1
done

port="$(docker port "$container" 8080/tcp | sed -n 's/.*://p' | head -n 1)"
curl --fail --silent --show-error "http://127.0.0.1:${port}/readyz" >/dev/null
curl --fail --silent --show-error --dump-header "$scratch/web-headers.txt" \
  --output "$scratch/container-index.html" "http://127.0.0.1:${port}/"
grep -q '<div id="root"></div>' "$scratch/container-index.html"
grep -Eiq '^Content-Type: text/html' "$scratch/web-headers.txt"
grep -Eiq "^Content-Security-Policy: .*default-src 'self'" "$scratch/web-headers.txt"
while IFS= read -r asset; do
  relative="${asset#internal/webassets/dist/}"
  destination="$scratch/$(basename "$relative")"
  curl --fail --silent --show-error --output "$destination" "http://127.0.0.1:${port}/${relative}"
  cmp "$asset" "$destination"
done < <(find internal/webassets/dist -type f ! -name index.html -print | LC_ALL=C sort)
docker exec "$container" wget --spider -q http://127.0.0.1:8080/readyz
if docker exec "$container" wget --spider -q http://127.0.0.1:8080/api/v1/__release_probe_missing; then
  echo "container health client accepted a non-success response" >&2
  exit 1
fi

docker exec -e "MOMENT_TOKEN=$token" "$container" /moment --url http://127.0.0.1:8080 post create \
  --content "container persistence smoke" --idempotency-key container-release-smoke --json >"$scratch/post.json"
post_id="$(jq -er '.data.id' "$scratch/post.json")"

metadata="$(docker exec "$container" /moment version --json)"
jq -e --arg version "$version" --arg commit "$commit" --arg build_date "$build_date" '
  .data.version == $version and .data.commit == $commit and .data.build_date == $build_date
' <<<"$metadata" >/dev/null

docker stop --time 15 "$container" >/dev/null
docker logs "$container" >"$scratch/server.log" 2>&1
grep -q '"event":"server_stopped"' "$scratch/server.log"
docker rm "$container" >/dev/null

docker run -d --name "$restart_container" --mount "type=volume,src=$volume,dst=/data" -p 127.0.0.1::8080 "$image" >/dev/null
deadline=$((SECONDS + 60))
while [[ "$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$restart_container")" != healthy ]]; do
  (( SECONDS < deadline )) || { docker logs "$restart_container" >&2; exit 1; }
  sleep 1
done
restart_port="$(docker port "$restart_container" 8080/tcp | sed -n 's/.*://p' | head -n 1)"
curl --fail --silent --show-error "http://127.0.0.1:${restart_port}/api/v1/posts/${post_id}" | jq -e --arg id "$post_id" '.data.id == $id' >/dev/null
docker stop --time 15 "$restart_container" >/dev/null
docker logs "$restart_container" >"$scratch/restart-server.log" 2>&1
grep -q '"event":"server_stopped"' "$scratch/restart-server.log"

mkdir -p dist
container_sbom="dist/container.spdx.json"
syft "$image" -o "spdx-json=$container_sbom"
SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)" node scripts/release/merge-spdx-components.mjs "$container_sbom"
grype "sbom:$container_sbom" --fail-on high
image_id="$(docker image inspect --format '{{.Id}}' "$image")"
sbom_sha256="$(shasum -a 256 "$container_sbom" | awk '{print $1}')"
base_images="$(awk '/^FROM / {print $2}' deploy/Dockerfile | jq -Rsc 'split("\n") | map(select(length > 0))')"
syft_version="$(syft version -o json)"
grype_version="$(grype version -o json)"
grype_database="$(grype db status -o json | jq -c '{schemaVersion,from,built,valid}')"
vulnerability_policy_sha256="$(shasum -a 256 release/vulnerability-exemptions.json | awk '{print $1}')"
jq -n \
  --arg commit "$commit" \
  --arg version "$version" \
  --arg build_date "$build_date" \
  --arg image_id "$image_id" \
  --arg sbom_sha256 "$sbom_sha256" \
  --arg vulnerability_policy_sha256 "$vulnerability_policy_sha256" \
  --argjson base_images "$base_images" \
  --argjson syft "$syft_version" \
  --argjson grype "$grype_version" \
  --argjson grype_database "$grype_database" \
  '{schema_version:1,commit:$commit,version:$version,build_date:$build_date,image_id:$image_id,base_images:$base_images,user:"65532:65532",data_dir:"/data",sbom:{format:"SPDX JSON",sha256:$sbom_sha256},scanners:{syft:$syft,grype:$grype,grype_database:$grype_database},vulnerability_policy_sha256:$vulnerability_policy_sha256,verification:{anonymous_volume_init:true,named_volume_init:true,default_serve:true,readiness:true,persistence_restart:true,graceful_sigterm:true,high_or_critical_vulnerabilities_absent:true}}' \
  >dist/container-evidence.json
