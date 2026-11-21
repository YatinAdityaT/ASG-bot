#!/data/data/com.termux/files/usr/bin/bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
deploy_root="$(git -C "$script_dir" rev-parse --show-toplevel)"
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"
config_path=""
interval=60

while (($#)); do
    case "$1" in
        --config)
            config_path="${2:?missing --config value}"
            shift 2
            ;;
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

if [[ -z "$config_path" || ! -f "$config_path" ]]; then
    echo "Usage: $0 --config FILE [--deploy-root PATH] [--interval SECONDS]" >&2
    exit 2
fi
if [[ ! "$interval" =~ ^[1-9][0-9]*$ ]]; then
    echo "Interval must be a positive number of seconds." >&2
    exit 2
fi

for command_name in git go nohup; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "Missing command: $command_name" >&2
        echo "Install prerequisites with: pkg install git golang termux-api" >&2
        exit 1
    fi
done

deploy_root="$(cd "$deploy_root" && pwd -P)"
if [[ ! -d "$deploy_root/.git" ]]; then
    echo "Deploy root is not a Git checkout: $deploy_root" >&2
    exit 1
fi

mkdir -p "$state_root/runtime" "$state_root/logs" "$HOME/.termux/boot"
state_root="$(cd "$state_root" && pwd -P)"
destination_config="$state_root/config.local.json"
if [[ "$(readlink -f "$config_path")" != "$(readlink -f "$destination_config" 2>/dev/null || true)" ]]; then
    cp "$config_path" "$destination_config"
fi
chmod 600 "$destination_config"

chmod 700 \
    "$deploy_root/scripts/deploy/auto-deploy-termux.sh" \
    "$deploy_root/scripts/deploy/supervise-termux.sh"

exclude_path="$deploy_root/.git/info/exclude"
touch "$exclude_path"
for entry in "/runtime-termux/" "/logs-termux/"; do
    grep -Fxq "$entry" "$exclude_path" || printf '%s\n' "$entry" >>"$exclude_path"
done

boot_launcher="$HOME/.termux/boot/asg-bot-auto-deploy"
{
    echo '#!/data/data/com.termux/files/usr/bin/bash'
    printf 'deploy_root=%q\n' "$deploy_root"
    printf 'state_root=%q\n' "$state_root"
    printf 'interval=%q\n' "$interval"
    cat <<'EOF'
mkdir -p "$state_root/logs"
termux-wake-lock 2>/dev/null || true
nohup "$deploy_root/scripts/deploy/supervise-termux.sh" \
    --deploy-root "$deploy_root" \
    --state-root "$state_root" \
    --interval "$interval" \
    >>"$state_root/logs/supervisor.log" 2>&1 </dev/null &
EOF
} >"$boot_launcher"
chmod 700 "$boot_launcher"

supervisor_pid_path="$state_root/runtime/supervisor.pid"
if [[ -f "$supervisor_pid_path" ]]; then
    supervisor_pid="$(tr -d '[:space:]' <"$supervisor_pid_path")"
    if [[ "$supervisor_pid" =~ ^[0-9]+$ ]] && kill -0 "$supervisor_pid" 2>/dev/null; then
        kill "$supervisor_pid" 2>/dev/null || true
        for _ in {1..20}; do
            kill -0 "$supervisor_pid" 2>/dev/null || break
            sleep 0.1
        done
    fi
    rm -f "$supervisor_pid_path"
fi

bot_pid_path="$state_root/runtime/bot.pid"
if [[ -f "$bot_pid_path" ]]; then
    bot_pid="$(tr -d '[:space:]' <"$bot_pid_path")"
    if [[ "$bot_pid" =~ ^[0-9]+$ ]] && kill -0 "$bot_pid" 2>/dev/null; then
        kill "$bot_pid" 2>/dev/null || true
        for _ in {1..30}; do
            kill -0 "$bot_pid" 2>/dev/null || break
            sleep 0.1
        done
        kill -0 "$bot_pid" 2>/dev/null && kill -KILL "$bot_pid" 2>/dev/null || true
    fi
    rm -f "$bot_pid_path"
fi

"$deploy_root/scripts/deploy/auto-deploy-termux.sh" \
    --deploy-root "$deploy_root" \
    --state-root "$state_root"

nohup "$deploy_root/scripts/deploy/supervise-termux.sh" \
    --deploy-root "$deploy_root" \
    --state-root "$state_root" \
    --interval "$interval" \
    >>"$state_root/logs/supervisor.log" 2>&1 </dev/null &

echo "Termux auto-deploy installed."
echo "Repository: $deploy_root"
echo "State: $state_root"
echo "Boot launcher: $boot_launcher"
echo "Deployment log: $state_root/logs/auto-deploy.log"
echo "Bot output: $state_root/logs/bot.stdout.log"
