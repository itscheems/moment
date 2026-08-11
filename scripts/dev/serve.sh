#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
data_dir="${MOMENT_DATA_DIR:-${repo_root}/moment-data}"
listen_address="${MOMENT_LISTEN:-127.0.0.1:8080}"
binary="${repo_root}/dist/moment"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  printf '%s\n' \
    'Build and run a local Moment server in the foreground.' \
    '' \
    'Environment:' \
    '  MOMENT_DATA_DIR  Runtime data directory (default: ./moment-data)' \
    '  MOMENT_LISTEN    Listen address (default: 127.0.0.1:8080)' \
    '' \
    'Additional arguments are passed to `moment serve`.' \
    'Press Ctrl+C to stop the server.'
  exit 0
fi

cd -- "$repo_root"

printf 'Building Moment...\n'
mise exec -- task build

if [[ ! -e "$data_dir" ]]; then
  mkdir -p -- "$data_dir"
fi

if [[ ! -e "$data_dir/instance.json" ]]; then
  printf 'Initializing local data at %s...\n' "$data_dir"
  "$binary" init --data-dir "$data_dir"
fi

printf 'Starting Moment at http://%s (Ctrl+C to stop)...\n' "$listen_address"
exec "$binary" serve \
  --data-dir "$data_dir" \
  --listen "$listen_address" \
  "$@"
