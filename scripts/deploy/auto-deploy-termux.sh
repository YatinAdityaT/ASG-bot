#!/data/data/com.termux/files/usr/bin/bash
set -Eeuo pipefail

deploy_root=""
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"

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
        *)
            echo "Unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [[ -z "$deploy_root" ]]; then
    echo "Usage: $0 --deploy-root PATH [--state-root PATH]" >&2
    exit 2
fi

deploy_root="$(cd "$deploy_root" && pwd -P)"
mkdir -p "$state_root/runtime" "$state_root/logs"
state_root="$(cd "$state_root" && pwd -P)"

runtime_dir="$state_root/runtime"
log_dir="$state_root/logs"
deploy_log="$log_dir/auto-deploy.log"
bot_stdout="$log_dir/bot.stdout.log"
bot_stderr="$log_dir/bot.stderr.log"
config_path="$state_root/config.local.json"
deployed_commit_path="$runtime_dir/deployed-commit.txt"
bot_pid_path="$runtime_dir/bot.pid"
current_binary="$runtime_dir/asg-bot"
next_binary="$runtime_dir/asg-bot.next"
previous_binary="$runtime_dir/asg-bot.previous"
lock_dir="$runtime_dir/deploy.lock"

log() {
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$deploy_log"
}

read_pid() {
    local path="$1"
    [[ -f "$path" ]] || return 1
    local value
    value="$(tr -d '[:space:]' <"$path")"
    [[ "$value" =~ ^[0-9]+$ ]] || return 1
    printf '%s\n' "$value"
}

bot_is_running() {
    local pid
    pid="$(read_pid "$bot_pid_path")" || return 1
    kill -0 "$pid" 2>/dev/null
}

stop_bot() {
    local pid
    pid="$(read_pid "$bot_pid_path")" || {
        rm -f "$bot_pid_path"
        return
    }
    if ! kill -0 "$pid" 2>/dev/null; then
        rm -f "$bot_pid_path"
        return
    fi
    kill "$pid" 2>/dev/null || true
    for _ in {1..30}; do
        if ! kill -0 "$pid" 2>/dev/null; then
            rm -f "$bot_pid_path"
            return
        fi
        sleep 0.5
    done
    kill -KILL "$pid" 2>/dev/null || true
    rm -f "$bot_pid_path"
}

start_bot() {
    if [[ ! -x "$current_binary" ]]; then
        echo "Bot executable is missing: $current_binary" >&2
        return 1
    fi
    (
        cd "$deploy_root"
        nohup env ASG_BOT_CONFIG_FILE="$config_path" \
            "$current_binary" >>"$bot_stdout" 2>>"$bot_stderr" </dev/null &
        echo "$!" >"$bot_pid_path"
    )
    sleep 2
    bot_is_running
}

if [[ -d "$lock_dir" ]]; then
    existing_pid=""
    [[ -f "$lock_dir/pid" ]] && existing_pid="$(cat "$lock_dir/pid" 2>/dev/null || true)"
    if [[ "$existing_pid" =~ ^[0-9]+$ ]] && kill -0 "$existing_pid" 2>/dev/null; then
        exit 0
    fi
    rm -rf "$lock_dir"
fi
mkdir "$lock_dir" 2>/dev/null || exit 0
echo "$$" >"$lock_dir/pid"
cleanup() {
    rm -rf "$lock_dir"
}
trap cleanup EXIT

if [[ ! -d "$deploy_root/.git" ]]; then
    log "deployment failed: deploy root is not a Git checkout"
    exit 1
fi
if [[ ! -f "$config_path" ]]; then
    log "deployment failed: missing $config_path"
    exit 1
fi

if ! git -C "$deploy_root" fetch origin master >>"$deploy_log" 2>&1; then
    log "deployment failed: git fetch failed"
    exit 1
fi
remote_commit="$(git -C "$deploy_root" rev-parse origin/master)"
active_target="$(
    git -C "$deploy_root" show \
        "${remote_commit}:scripts/deploy/active-bot-target.txt" |
        tr -d '[:space:]'
)"
if [[ "$active_target" != "windows" && "$active_target" != "termux" ]]; then
    log "deployment failed: invalid active bot target '$active_target'"
    exit 1
fi
deployed_commit=""
[[ -f "$deployed_commit_path" ]] &&
    deployed_commit="$(tr -d '[:space:]' <"$deployed_commit_path")"

if [[ "$deployed_commit" == "$remote_commit" && -x "$current_binary" ]]; then
    if [[ "$active_target" != "termux" ]]; then
        if bot_is_running; then
            stop_bot
            log "stopped bot because active target is $active_target"
        fi
        exit 0
    fi
    if ! bot_is_running; then
        if start_bot; then
            log "restarted current commit $remote_commit"
        else
            log "deployment failed: current bot exited during startup"
            exit 1
        fi
    fi
    exit 0
fi

tracked_changes="$(git -C "$deploy_root" status --porcelain --untracked-files=no)"
if [[ -n "$tracked_changes" ]]; then
    log "deployment failed: checkout has tracked local changes"
    exit 1
fi

staging_root="$state_root/staging.$$"
worktree_added=false
rm -rf "$staging_root" "$next_binary"
finish_staging() {
    if $worktree_added; then
        git -C "$deploy_root" worktree remove --force "$staging_root" \
            >>"$deploy_log" 2>&1 || true
    fi
    rm -rf "$staging_root"
}
trap 'finish_staging; cleanup' EXIT

if ! git -C "$deploy_root" worktree add --detach "$staging_root" "$remote_commit" \
    >>"$deploy_log" 2>&1; then
    log "deployment failed: could not create staging worktree"
    exit 1
fi
worktree_added=true
if ! (
    cd "$staging_root"
    go build -o "$next_binary" .
) >>"$deploy_log" 2>&1; then
    log "deployment failed: Go build failed"
    exit 1
fi
finish_staging
worktree_added=false

if ! git -C "$deploy_root" merge --ff-only origin/master >>"$deploy_log" 2>&1; then
    log "deployment failed: checkout could not fast-forward"
    exit 1
fi

stop_bot
rm -f "$previous_binary"
[[ -f "$current_binary" ]] && mv "$current_binary" "$previous_binary"
mv "$next_binary" "$current_binary"
chmod 700 "$current_binary"

if [[ "$active_target" != "termux" ]]; then
    printf '%s' "$remote_commit" >"$deployed_commit_path"
    log "deployed commit $remote_commit; bot inactive on termux"
    exit 0
fi

if ! start_bot; then
    rm -f "$current_binary" "$bot_pid_path"
    if [[ -f "$previous_binary" ]]; then
        mv "$previous_binary" "$current_binary"
        start_bot || true
    fi
    log "deployment failed: new bot exited during startup; rollback attempted"
    exit 1
fi

printf '%s' "$remote_commit" >"$deployed_commit_path"
log "deployed commit $remote_commit"
