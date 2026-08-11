#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
mkdir -p dist/cross

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  goos="${target%/*}"
  goarch="${target#*/}"
  suffix=""
  if [[ "$goos" == windows ]]; then
    suffix=".exe"
  fi
  output="dist/cross/moment-${goos}-${goarch}${suffix}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -o "$output" ./cmd/moment
  go version -m "$output" >/dev/null
done
