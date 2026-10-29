#!/usr/bin/env bash
# The playground: a second, disposable Moment instance beside the real one.
#
#   task playground                # build, seed once, serve on 127.0.0.1:18080
#   task playground -- serve       # same instance and settings, foreground
#   task playground -- reset       # throw the data away and seed again
#   task playground -- status      # running? which anchor? how many cases?
#   task playground -- case post.hidden   # the page for one showcase case
#   task playground -- cookie      # an account session cookie for curl or a headless browser
#   task playground -- stop
#
# It serves the `site-showcase` scenario: a seven-year site with every
# reader-facing state on it at least once -- hidden posts, quotes of hidden
# and deleted posts, link cards, every media grid, a calendar with a leap
# day and a year boundary, members in every account status, a post with more
# comments than one page holds. The scenario is dated from the clock when it
# is seeded, so the newest posts read as minutes and hours ago; reseed when
# they have drifted too far.
#
# Everything lives under .moment-local/playground/ (ignored): the binary, the
# data directory, the account token, the seed result with its case manifest,
# and the server log. Nothing here touches moment-data/ or port 8080.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
playground_home="${PLAYGROUND_DIR:-${repo_root}/.moment-local/playground}"
port="${PLAYGROUND_PORT:-18080}"
seed="${PLAYGROUND_SEED:-1}"
scenario="site-showcase"
binary="${playground_home}/moment"
data_dir="${playground_home}/data"
token_file="${playground_home}/account.token"
seed_file="${playground_home}/seed.json"
cookie_file="${playground_home}/cookie.txt"
serve_log="${playground_home}/serve.log"
base_url="http://127.0.0.1:${port}"

say() { printf '\033[36m▸\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  printf '%s\n' \
    'Run a disposable, seeded Moment beside the real one.' \
    '' \
    'Commands:' \
    '  up        Build, seed the site-showcase scenario if needed, and (re)serve (default)' \
    '  serve     Build, seed if needed, and serve in the foreground (Ctrl+C to stop)' \
    '  reset     Stop, discard the playground data, seed again, and serve' \
    '  stop      Stop the playground server' \
    '  status    Report whether it runs, what it was seeded with, and where things are' \
    '  token     Print the account token' \
    '  cookie    Sign in with the account token and print a moment_session cookie' \
    '  manifest  Print the case manifest as JSON' \
    '  cases     List every case key with its page and note' \
    '  case KEY  Print the page for one case key (e.g. post.recent.hidden)' \
    '  open      Open the playground in the default browser' \
    '' \
    'Environment:' \
    '  PLAYGROUND_PORT  Listen port (default 18080)' \
    '  PLAYGROUND_DIR   Playground directory (default ./.moment-local/playground)' \
    '  PLAYGROUND_SEED  Scenario seed (default 1)' \
    '  SKIP_BUILD=1     Serve the binary already in the playground without rebuilding' \
    '' \
    'Both up and serve load provider keys from .env.local, then .env.' \
    'Existing process environment values take precedence.'
}

read_local_setting() {
  local setting_file="$1" setting_name="$2"
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

# Provider and location keys come from the real instance's .env.local, read
# by name and never evaluated, exactly as scripts/dev/serve.sh reads them.
# Without them the login page draws no provider buttons and the location
# picker offers manual labels only, which is not what is being tested here.
load_provider_keys() {
  local config_file key value
  for config_file in "${repo_root}/.env.local" "${repo_root}/.env"; do
    [[ -f "$config_file" ]] || continue
    for key in AMAP_API_KEY GEOAPIFY_API_KEY \
      MOMENT_GOOGLE_CLIENT_ID MOMENT_GOOGLE_CLIENT_SECRET \
      MOMENT_GITHUB_CLIENT_ID MOMENT_GITHUB_CLIENT_SECRET \
      MOMENT_GOOGLE_IOS_CLIENT_ID; do
      [[ -z "${!key:-}" ]] || continue
      value="$(read_local_setting "$config_file" "$key")"
      [[ -z "$value" ]] || export "$key=$value"
    done
  done
}

json_field() {
  # json_field FILE EXPRESSION -- EXPRESSION reads `document`.
  mise exec -- node -e '
    const fs = require("node:fs");
    const document = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const value = Function("document", "return " + process.argv[2])(document);
    if (value === undefined || value === null) process.exit(3);
    process.stdout.write(typeof value === "string" ? value : JSON.stringify(value, null, 2));
  ' "$1" "$2"
}

listening_pid() {
  # Capture first, then take the first line: an early-exiting consumer on
  # a pipe would turn lsof's SIGPIPE into a failure under pipefail.
  local pids
  pids="$(lsof -nP -iTCP@127.0.0.1:"$port" -sTCP:LISTEN -t 2>/dev/null || true)"
  printf '%s\n' "$pids" | sed -n '1p'
}

running() {
  [[ -n "$(listening_pid)" ]]
}

build() {
  if [[ "${SKIP_BUILD:-}" == 1 ]]; then
    [[ -x "$binary" ]] || die "SKIP_BUILD=1 but there is no binary at ${binary}"
    return
  fi
  # Always from source, never "if missing": a stale binary answers with
  # yesterday's endpoints and looks exactly like a bug in today's change.
  say "构建 web 资源与后端（${repo_root}）"
  (cd -- "$repo_root" && mise exec -- pnpm --dir web build >/dev/null)
  (cd -- "$repo_root" && CGO_ENABLED=0 mise exec -- go build -buildvcs=false -trimpath -o "$binary" ./cmd/moment)
}

seeded() {
  [[ -e "${data_dir}/instance.json" && -s "$token_file" && -s "$seed_file" ]]
}

seed_playground() {
  running && die "playground 还在 ${port} 上跑着；先 stop 再 reset"
  umask 077
  local seed_args=(dev scenario "$scenario" --seed "$seed")
  [[ ! -s "$token_file" ]] || seed_args+=(--account-token-file "$token_file")
  mkdir -p -- "$data_dir"
  # Validate the target and saved credential before discarding any fixture data.
  "$binary" --data-dir "$data_dir" --no-input "${seed_args[@]}" --dry-run --json >/dev/null
  # dev scenario bootstraps only a strictly empty directory.
  rm -rf -- "$data_dir"
  mkdir -p -- "$data_dir"
  say "灌入 ${scenario}（seed ${seed}，以当前时间为锚）"
  "$binary" --data-dir "$data_dir" --no-input "${seed_args[@]}" --yes --json >"$seed_file"
  umask 077
  json_field "$seed_file" 'document.data.api_token' >"$token_file"
  chmod 600 "$token_file"
  chmod 600 "$seed_file"
  rm -f -- "$cookie_file"
  say "$(json_field "$seed_file" '"锚点 " + document.data.showcase.anchored_at + "，" + document.data.showcase.cases.length + " 个 case，展示账号 @" + document.data.showcase.profile_username')"
}

start_server() {
  if running; then
    say "playground 已在 ${base_url} 上（pid $(listening_pid)），复用它"
    return
  fi
  load_provider_keys
  say "启动 → ${base_url}（数据 ${data_dir}，日志 ${serve_log}）"
  nohup "$binary" serve --data-dir "$data_dir" --listen "127.0.0.1:${port}" >"$serve_log" 2>&1 &
  for _ in $(seq 1 60); do
    curl -sf -m 1 "${base_url}/api/v1/site" >/dev/null 2>&1 && break
    sleep 0.25
  done
  curl -sf -m 2 "${base_url}/api/v1/site" >/dev/null || die "没起来，看 ${serve_log}"
}

stop_server() {
  local pid
  pid="$(listening_pid)"
  if [[ -z "$pid" ]]; then
    say "playground 没在 ${port} 上跑"
    return
  fi
  # Only ever stop our own serve, never whatever else happens to own the port.
  local command
  command="$(ps -o command= -p "$pid" || true)"
  if [[ "$command" != *"$data_dir"* ]]; then
    die "端口 ${port} 上的进程 ${pid} 不是这个 playground 的（${command}），不动它"
  fi
  kill "$pid"
  for _ in $(seq 1 40); do
    running || break
    sleep 0.25
  done
  running && die "进程 ${pid} 没有退出"
  say "已停止（pid ${pid}）"
}

print_summary() {
  say "好了。${base_url}/ · 账号令牌 ${token_file} · case 清单：task playground -- cases"
}

cmd_up() {
  mkdir -p -- "$playground_home"
  build
  # A fresh build replaces a running server, so the playground always runs
  # the source that was just built; SKIP_BUILD=1 keeps whatever is up.
  if running && [[ "${SKIP_BUILD:-}" != 1 ]]; then
    stop_server
  fi
  seeded || seed_playground
  start_server
  print_summary
}

cmd_serve() {
  mkdir -p -- "$playground_home"
  build
  stop_server
  seeded || seed_playground
  load_provider_keys
  say "前台启动 → ${base_url}（Ctrl+C 停止）"
  exec "$binary" serve --data-dir "$data_dir" --listen "127.0.0.1:${port}"
}

cmd_reset() {
  mkdir -p -- "$playground_home"
  build
  stop_server
  seed_playground
  start_server
  print_summary
}

cmd_status() {
  if running; then
    printf 'running\t%s (pid %s)\n' "$base_url" "$(listening_pid)"
  else
    printf 'stopped\t%s\n' "$base_url"
  fi
  printf 'directory\t%s\n' "$playground_home"
  if seeded; then
    printf 'scenario\t%s seed %s\n' "$scenario" "$seed"
    printf 'anchored_at\t%s\n' "$(json_field "$seed_file" 'document.data.showcase.anchored_at')"
    printf 'counts\t%s\n' "$(json_field "$seed_file" 'JSON.stringify(document.data.plan.counts)')"
    printf 'cases\t%s\n' "$(json_field "$seed_file" 'String(document.data.showcase.cases.length)')"
  else
    printf 'scenario\tnot seeded\n'
  fi
}

cmd_cookie() {
  seeded || die "还没灌种子：task playground"
  running || die "playground 没在跑：task playground"
  local token response cookie
  token="$(tr -d '\r\n' <"$token_file")"
  # The token is the URL-safe string dev scenario minted; it needs no
  # escaping inside the JSON body. The cookie jar goes to stdout (-c -).
  response="$(curl -sS -m 5 -o /dev/null -c - -H 'Content-Type: application/json' \
    --data "{\"token\":\"${token}\"}" "${base_url}/api/v1/login/session")"
  cookie="$(printf '%s\n' "$response" | awk '$6 == "moment_session" { print $7 }')"
  [[ -n "$cookie" ]] || die "登录没有返回会话 cookie（看 ${serve_log}）"
  umask 077
  printf 'moment_session=%s\n' "$cookie" >"$cookie_file"
  chmod 600 "$cookie_file"
  cat "$cookie_file"
}

cmd_cases() {
  seeded || die "还没灌种子：task playground"
  # shellcheck disable=SC2016 # Template literals belong to the Node program.
  mise exec -- node -e '
    const fs = require("node:fs");
    const document = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const base = process.argv[2];
    for (const item of document.data.showcase.cases) {
      const page = item.path ? base + item.path : "(no page)";
      process.stdout.write(`${item.key.padEnd(30)} ${page.padEnd(52)} ${item.note}\n`);
    }
  ' "$seed_file" "$base_url"
}

cmd_case() {
  local key="${1:-}"
  [[ -n "$key" ]] || die "用法：task playground -- case <key>"
  seeded || die "还没灌种子：task playground"
  # shellcheck disable=SC2016 # Template literals belong to the Node program.
  mise exec -- node -e '
    const fs = require("node:fs");
    const document = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const item = document.data.showcase.cases.find((candidate) => candidate.key === process.argv[3]);
    if (!item) {
      process.stderr.write(`no case ${process.argv[3]}; run: task playground -- cases\n`);
      process.exit(3);
    }
    process.stdout.write(item.path ? process.argv[2] + item.path + "\n" : "(no page: " + item.note + ")\n");
  ' "$seed_file" "$base_url" "$key"
}

case "${1:-up}" in
  -h | --help | help) usage ;;
  up) cmd_up ;;
  serve) cmd_serve ;;
  reset) cmd_reset ;;
  stop) stop_server ;;
  status) cmd_status ;;
  token) seeded || die "还没灌种子：task playground"; tr -d '\r\n' <"$token_file"; printf '\n' ;;
  cookie) cmd_cookie ;;
  manifest) seeded || die "还没灌种子：task playground"; json_field "$seed_file" 'document.data.showcase'; printf '\n' ;;
  cases) cmd_cases ;;
  case) shift; cmd_case "${1:-}" ;;
  open) running || die "playground 没在跑：task playground"; open "${base_url}/" ;;
  *) usage >&2; exit 2 ;;
esac
