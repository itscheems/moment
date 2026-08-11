#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
data_dir="${MOMENT_DATA_DIR:-${repo_root}/moment-data}"
listen_address="${MOMENT_LISTEN:-127.0.0.1:8080}"
token_file="${MOMENT_OWNER_TOKEN_FILE:-${repo_root}/.moment-local/owner.token}"
binary="${repo_root}/dist/moment"
rotate_token=false
serve_args=()

if [[ "$token_file" != /* ]]; then
  token_file="${repo_root}/${token_file#./}"
fi

for argument in "$@"; do
  case "$argument" in
    --rotate-owner-token)
      rotate_token=true
      ;;
    *)
      serve_args+=("$argument")
      ;;
  esac
done

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  printf '%s\n' \
    'Build and run a local Moment server in the foreground.' \
    '' \
    'Environment:' \
    '  MOMENT_DATA_DIR  Runtime data directory (default: ./moment-data)' \
    '  MOMENT_LISTEN    Listen address (default: 127.0.0.1:8080)' \
    '  MOMENT_OWNER_TOKEN_FILE' \
    '                   Local owner token file (default: ./.moment-local/owner.token)' \
    '' \
    'Options:' \
    '  --rotate-owner-token  Rotate a missing or compromised local owner token before serving' \
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

save_owner_token() {
  local operation="$1"
  local token_dir response_file token_temp
  umask 077
  token_dir="$(dirname -- "$token_file")"
  if [[ -L "$token_dir" ]]; then
    printf 'Refusing symlinked owner-token directory: %s\n' "$token_dir" >&2
    return 1
  fi
  mkdir -p -- "$token_dir"
  response_file="$(mktemp "${token_dir}/.owner-response.XXXXXX")"
  token_temp="$(mktemp "${token_dir}/.owner-token.XXXXXX")"
  trap 'rm -f -- "${response_file:-}" "${token_temp:-}"' RETURN

  if [[ "$operation" == "init" ]]; then
    "$binary" init --data-dir "$data_dir" --json >"$response_file"
  else
    "$binary" owner rotate-token --data-dir "$data_dir" --json >"$response_file"
  fi

  mise exec -- node -e '
    const fs = require("node:fs");
    const document = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const token = document?.ok === true ? document?.data?.token : null;
    if (typeof token !== "string" || token.length === 0 || /[\r\n]/u.test(token)) {
      throw new Error("Moment did not return one canonical owner token");
    }
    process.stdout.write(`${token}\n`);
  ' "$response_file" >"$token_temp"
  chmod 600 "$token_temp"
  if [[ -L "$token_file" ]]; then
    printf 'Refusing symlinked owner-token file: %s\n' "$token_file" >&2
    return 1
  fi
  mv -f -- "$token_temp" "$token_file"
  rm -f -- "$response_file"
  trap - RETURN
}

if [[ ! -e "$data_dir/instance.json" ]]; then
  printf 'Initializing local data at %s...\n' "$data_dir"
  save_owner_token init
elif [[ "$rotate_token" == true ]]; then
  printf 'Rotating the local owner token...\n'
  save_owner_token rotate
elif [[ ! -f "$token_file" ]]; then
  printf '%s\n' \
    "No saved local owner token was found at ${token_file}." \
    'The token cannot be recovered. Rotate it while the server is stopped with:' \
    '  mise exec -- task run -- --rotate-owner-token' >&2
  exit 2
fi

if [[ -L "$token_file" ]]; then
  printf 'Refusing symlinked owner-token file: %s\n' "$token_file" >&2
  exit 1
fi
chmod 600 "$token_file"
printf 'Owner token file: %s\n' "$token_file"
if [[ -t 1 ]]; then
  printf 'Owner token (local testing only): '
  tr -d '\r\n' <"$token_file"
  printf '\n'
fi

printf 'Starting Moment at http://%s (Ctrl+C to stop)...\n' "$listen_address"
if ((${#serve_args[@]} > 0)); then
  exec "$binary" serve \
    --data-dir "$data_dir" \
    --listen "$listen_address" \
    "${serve_args[@]}"
fi
exec "$binary" serve \
  --data-dir "$data_dir" \
  --listen "$listen_address"
