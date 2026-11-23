#!/data/data/com.termux/files/usr/bin/bash
set -u

deploy_root=""
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"
interval=30
grace_seconds=120
failure_limit=3

while (($#)); do
    case "$1" in
        --deploy-root)
            deploy_root="${2:?missing --deploy-root value}"
            shift 2
            ;;
        --state-root)
            state_root="${2:?missing --state-root value}"
            shift 2
            ;;
        --interval)
            interval="${2:?missing --interval value}"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [[ -z "$deploy_root" || ! "$interval" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: $0 --deploy-root PATH [--state-root PATH] [--interval SECONDS]" >&2
    exit 2
fi

deploy_root="$(cd "$deploy_root" && pwd -P)"
state_root="$(cd "$state_root" && pwd -P)"
headless_root="$state_root/headless-meet"
runtime_dir="$headless_root/runtime"
config_path="$state_root/config.local.json"
pid_path="$runtime_dir/supervisor.pid"
runner_pid_path="$runtime_dir/runner.pid"
target_path="$deploy_root/scripts/deploy/active-meet-monitor-target.txt"
mkdir -p "$runtime_dir" "$headless_root/logs"

log() {
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

read_pid() {
    local path="$1" value
    [[ -f "$path" ]] || return 1
    value="$(tr -d '[:space:]' <"$path")"
    [[ "$value" =~ ^[0-9]+$ ]] || return 1
    printf '%s\n' "$value"
}

if old_pid="$(read_pid "$pid_path" 2>/dev/null)" &&
    kill -0 "$old_pid" 2>/dev/null; then
    exit 0
fi
printf '%s' "$$" >"$pid_path"

runner_pid=""
stop_runner() {
    [[ -n "$runner_pid" ]] || runner_pid="$(read_pid "$runner_pid_path" 2>/dev/null || true)"
    if [[ "$runner_pid" =~ ^[0-9]+$ ]] && kill -0 "$runner_pid" 2>/dev/null; then
        kill -TERM -- "-$runner_pid" 2>/dev/null ||
            kill -TERM "$runner_pid" 2>/dev/null || true
        for _ in {1..30}; do
            kill -0 "$runner_pid" 2>/dev/null || break
            sleep 0.2
        done
        kill -KILL -- "-$runner_pid" 2>/dev/null ||
            kill -KILL "$runner_pid" 2>/dev/null || true
    fi
    [[ "$runner_pid" =~ ^[0-9]+$ ]] && wait "$runner_pid" 2>/dev/null || true
    runner_pid=""
    rm -f "$runner_pid_path"
}
cleanup() {
    stop_runner
    rm -f "$pid_path"
}
trap cleanup EXIT INT TERM

for required in \
    "$config_path" \
    "$headless_root/installed" \
    "$headless_root/profile-ready" \
    "$target_path"; do
    if [[ ! -f "$required" ]]; then
        log "Headless Meet supervisor cannot start; missing $required"
        exit 1
    fi
done

read_config() {
    python - "$config_path" "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8-sig") as config_file:
    value = json.load(config_file).get(sys.argv[2], "")
print(str(value).strip())
PY
}

api_token="$(read_config apiToken)"
api_addr="$(read_config apiAddr)"
meeting_code="$(read_config meetCode)"
[[ "$api_addr" =~ ^https?:// ]] || api_addr="http://$api_addr"
status_url="${api_addr%/}/v1/meet-status"
cdp_url="http://127.0.0.1:9222/json/list"

start_runner() {
    setsid "$deploy_root/scripts/deploy/run-headless-meet-termux.sh" \
        --deploy-root "$deploy_root" \
        --state-root "$state_root" \
        >>"$headless_root/logs/browser-runtime.log" 2>&1 </dev/null &
    runner_pid=$!
    printf '%s' "$runner_pid" >"$runner_pid_path"
    log "Started headless Meet runner as PID $runner_pid."
}

browser_healthy() {
    local tabs
    tabs="$(curl --silent --show-error --fail --max-time 10 "$cdp_url" 2>/dev/null)" ||
        return 1
    printf '%s' "$tabs" | grep -Fq "meet.google.com/$meeting_code"
}

monitor_healthy() {
    local status
    status="$(
        curl --silent --show-error --fail --max-time 10 \
            -H "Authorization: Bearer $api_token" \
            "$status_url" 2>/dev/null
    )" || return 1
    python - "$meeting_code" "$status" <<'PY'
import json
import sys

try:
    status = json.loads(sys.argv[2])
except Exception:
    raise SystemExit(1)
expected_meeting = sys.argv[1]
healthy = (
    status.get("monitorState") == "online"
    and status.get("meeting") == expected_meeting
    and status.get("forwardingEnabled") is True
)
raise SystemExit(0 if healthy else 1)
PY
}

backoff=15
while true; do
    target="$(tr -d '[:space:]' <"$target_path")"
    if [[ "$target" != "termux" ]]; then
        log "Headless Meet target is $target; supervisor is stopping."
        exit 0
    fi

    start_runner
    started_at="$(date +%s)"
    failures=0
    checks=0
    restart_reason="runner exited"
    while kill -0 "$runner_pid" 2>/dev/null; do
        sleep "$interval"
        target="$(tr -d '[:space:]' <"$target_path")"
        if [[ "$target" != "termux" ]]; then
            log "Headless Meet target changed to $target."
            exit 0
        fi
        now="$(date +%s)"
        if ((now - started_at < grace_seconds)); then
            continue
        fi

        if browser_healthy && monitor_healthy; then
            failures=0
            if ((now - started_at >= 600)); then
                backoff=15
            fi
        else
            failures=$((failures + 1))
            log "Headless Meet health check failed ($failures/$failure_limit)."
        fi
        checks=$((checks + 1))
        if ((checks % 10 == 0)); then
            chromium_rss="$(
                ps -A -o RSS,ARGS 2>/dev/null |
                    awk '/[c]hromium/ {sum += $1} END {print sum + 0}'
            )"
            log "Headless Meet Chromium RSS: ${chromium_rss} KiB."
        fi
        if ((failures >= failure_limit)); then
            restart_reason="sustained browser/API heartbeat failure"
            break
        fi
    done

    log "Restarting headless Meet: $restart_reason; backoff ${backoff}s."
    stop_runner
    sleep "$backoff"
    ((backoff < 300)) && backoff=$((backoff * 2))
    ((backoff > 300)) && backoff=300
done
