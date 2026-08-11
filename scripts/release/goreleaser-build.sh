#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

commit="$(git rev-parse HEAD)"
commit_epoch="$(git show -s --format=%ct HEAD)"
export SOURCE_DATE_EPOCH="$commit_epoch"
export MOMENT_RELEASE_COMMIT="$commit"
export MOMENT_RELEASE_EPOCH="$commit_epoch"
MOMENT_RELEASE_DATE="$(node -e 'process.stdout.write(new Date(Number(process.argv[1]) * 1000).toISOString().replace(".000Z", "Z"))' "$commit_epoch")"
export MOMENT_RELEASE_DATE

goreleaser release "$@"
