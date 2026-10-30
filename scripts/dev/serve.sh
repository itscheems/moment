#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
data_dir="${MOMENT_DATA_DIR:-${repo_root}/moment-data}"
listen_address="${MOMENT_LISTEN:-127.0.0.1:8080}"
token_file="${MOMENT_TOKEN_FILE:-${repo_root}/.moment-local/account.token}"
binary="${repo_root}/dist/moment"
serve_args=("$@")

read_local_setting() {
  local setting_file="$1"
  local setting_name="$2"
  [[ -f "$setting_file" ]] || return 0
  awk -v setting_name="$setting_name" '
    index($0, setting_name "=") == 1 {
      value = substr($0, length(setting_name) + 2)
      sub(/\r$/, "", value)
      print value
      exit
    }
  ' "$setting_file"
}

load_local_settings() {
  local config_file key value

  # Process environment wins, followed by .env.local, then .env. Parse only the
  # explicit keys listed below; never source or evaluate local files.
  for config_file in "${repo_root}/.env.local" "${repo_root}/.env"; do
    [[ -f "$config_file" ]] || continue
    for key in MOMENT_USERNAME AMAP_API_KEY GEOAPIFY_API_KEY \
      MOMENT_GOOGLE_CLIENT_ID MOMENT_GOOGLE_CLIENT_SECRET \
      MOMENT_GITHUB_CLIENT_ID MOMENT_GITHUB_CLIENT_SECRET \
      MOMENT_GOOGLE_IOS_CLIENT_ID \
      MOMENT_GMAIL_USERNAME MOMENT_GMAIL_APP_PASSWORD; do
      [[ -z "${!key:-}" ]] || continue
      value="$(read_local_setting "$config_file" "$key")"
      [[ -z "$value" ]] || export "$key=$value"
    done
  done
}

if [[ "$token_file" != /* ]]; then
  token_file="${repo_root}/${token_file#./}"
fi

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  printf '%s\n' \
    'Build and run a local Moment server in the foreground.' \
    '' \
    'Environment:' \
    '  MOMENT_DATA_DIR  Runtime data directory (default: ./moment-data)' \
    '  MOMENT_LISTEN    Listen address (default: 127.0.0.1:8080)' \
    '  MOMENT_USERNAME  Required account name when initializing a new instance' \
    '  MOMENT_TOKEN_FILE' \
    '                   Initial account token output (default: ./.moment-local/account.token)' \
    '  AMAP_API_KEY     AMap Web Service key for mainland place suggestions' \
    '  GEOAPIFY_API_KEY' \
    '                   Geoapify key for global place suggestions' \
    '' \
    'Local config:' \
    '  .env.local and .env are read without shell evaluation. Existing process' \
    '  environment values take precedence. Account name and provider keys are read.' \
    '' \
    'Additional arguments are passed to `moment serve`.' \
    'Press Ctrl+C to stop the server.'
  exit 0
fi

load_local_settings
if [[ ! -e "$data_dir/instance.json" && -z "${MOMENT_USERNAME:-}" ]]; then
  printf 'Set MOMENT_USERNAME to name the first administrator account.\n' >&2
  exit 2
fi

cd -- "$repo_root"

printf 'Building Moment...\n'
mise exec -- task build

if [[ ! -e "$data_dir" ]]; then
  umask 077
  mkdir -p -- "$data_dir"
fi

save_initial_token() {
  local token_dir response_file token_temp
  umask 077
  token_dir="$(dirname -- "$token_file")"
  [[ ! -L "$token_dir" && ! -e "$token_file" && ! -L "$token_file" ]] || {
    printf 'Initial credential output already exists or is symlinked: %s\n' "$token_file" >&2
    return 1
  }
  mkdir -p -- "$token_dir"
  response_file="$(mktemp "${token_dir}/.account-response.XXXXXX")"
  token_temp="$(mktemp "${token_dir}/.account-token.XXXXXX")"
  trap 'rm -f -- "${response_file:-}" "${token_temp:-}"' RETURN
  "$binary" init --data-dir "$data_dir" --username "$MOMENT_USERNAME" --json >"$response_file"
  mise exec -- node -e '
    const fs = require("node:fs");
    const document = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const token = document?.ok === true ? document?.data?.token : null;
    if (typeof token !== "string" || !token || /[\r\n]/u.test(token)) throw new Error("Account initialization returned no credential");
    process.stdout.write(`${token}\n`);
  ' "$response_file" >"$token_temp"
  chmod 600 "$token_temp"
  mv -- "$token_temp" "$token_file"
  rm -- "$response_file"
  trap - RETURN
  printf 'Initial account token saved to: %s\n' "$token_file"
}

if [[ ! -e "$data_dir/instance.json" ]]; then
  [[ -n "${MOMENT_USERNAME:-}" ]] || {
    printf 'Set MOMENT_USERNAME to name the first administrator account.\n' >&2
    exit 2
  }
  printf 'Initializing local data at %s...\n' "$data_dir"
  save_initial_token
fi

if [[ -n "${AMAP_API_KEY:-}" || -n "${GEOAPIFY_API_KEY:-}" ]]; then
  provider_summary=''
  [[ -z "${AMAP_API_KEY:-}" ]] || provider_summary='AMap'
  if [[ -n "${GEOAPIFY_API_KEY:-}" ]]; then
    [[ -z "$provider_summary" ]] || provider_summary+=', '
    provider_summary+='Geoapify'
  fi
  printf 'Location providers: %s\n' "$provider_summary"
else
  printf '%s\n' 'Location providers: none (manual location labels only)' >&2
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
