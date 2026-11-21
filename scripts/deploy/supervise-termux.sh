#!/data/data/com.termux/files/usr/bin/bash
set -u

deploy_root=""
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"
interval=60

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
mkdir -p "$state_root/runtime" "$state_root/logs"
state_root="$(cd "$state_root" && pwd -P)"
pid_path="$state_root/runtime/supervisor.pid"

if [[ -f "$pid_path" ]]; then
    old_pid="$(tr -d '[:space:]' <"$pid_path")"
    if [[ "$old_pid" =~ ^[0-9]+$ ]] && kill -0 "$old_pid" 2>/dev/null; then
        exit 0
    fi
fi
echo "$$" >"$pid_path"

cleanup() {
    rm -f "$pid_path"
}
shutdown() {
    exit 0
}
trap cleanup EXIT
trap shutdown INT TERM

command -v termux-wake-lock >/dev/null 2>&1 && termux-wake-lock || true

while true; do
    "$deploy_root/scripts/deploy/auto-deploy-termux.sh" \
        --deploy-root "$deploy_root" \
        --state-root "$state_root" || true
    sleep "$interval"
done
