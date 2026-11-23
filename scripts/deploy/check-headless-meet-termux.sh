#!/data/data/com.termux/files/usr/bin/bash
set -Eeuo pipefail

deploy_root=""
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"
duration=0
interval=30

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
        --duration)
            duration="${2:?missing --duration value}"
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

if [[ -z "$deploy_root" ||
      ! "$duration" =~ ^[0-9]+$ ||
      ! "$interval" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: $0 --deploy-root PATH [--state-root PATH] [--duration SECONDS]" >&2
    exit 2
fi

deploy_root="$(cd "$deploy_root" && pwd -P)"
state_root="$(cd "$state_root" && pwd -P)"
config_path="$state_root/config.local.json"
headless_root="$state_root/headless-meet"
target_path="$deploy_root/scripts/deploy/active-meet-monitor-target.txt"
supervisor_pid_path="$headless_root/runtime/supervisor.pid"

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
deadline=$(($(date +%s) + duration))
checks=0

echo "Keep the phone charging and turn its physical screen off now."
while true; do
    target="$(tr -d '[:space:]' <"$target_path")"
    [[ "$target" == "termux" ]] || {
        echo "FAIL: Meet monitor target is $target, not termux." >&2
        exit 1
    }
    supervisor_pid="$(tr -d '[:space:]' <"$supervisor_pid_path" 2>/dev/null || true)"
    [[ "$supervisor_pid" =~ ^[0-9]+$ ]] &&
        kill -0 "$supervisor_pid" 2>/dev/null || {
            echo "FAIL: headless Meet supervisor is not running." >&2
            exit 1
        }
    tabs="$(curl --silent --show-error --fail --max-time 10 \
        http://127.0.0.1:9222/json/list)" || {
        echo "FAIL: Chromium debugging endpoint is offline." >&2
        exit 1
    }
    printf '%s' "$tabs" | grep -Fq "meet.google.com/$meeting_code" || {
        echo "FAIL: configured Meet tab is not open." >&2
        exit 1
    }
    status="$(
        curl --silent --show-error --fail --max-time 10 \
            -H "Authorization: Bearer $api_token" \
            "$status_url"
    )"
    python - "$meeting_code" "$status" <<'PY'
import json
import sys

expected = sys.argv[1]
status = json.loads(sys.argv[2])
if status.get("monitorState") != "online":
    raise SystemExit("Meet extension heartbeat is offline")
if status.get("meeting") != expected:
    raise SystemExit("Meet heartbeat belongs to a different meeting")
if status.get("forwardingEnabled") is not True:
    raise SystemExit("Meet forwarding is disabled")
PY

    checks=$((checks + 1))
    echo "PASS $(date -u +%Y-%m-%dT%H:%M:%SZ): Chromium and Meet heartbeat are healthy."
    ((duration == 0 || $(date +%s) >= deadline)) && break
    sleep "$interval"
done

echo "Headless Meet acceptance passed ($checks checks)."
