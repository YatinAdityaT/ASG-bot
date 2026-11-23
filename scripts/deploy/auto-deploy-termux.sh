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
deployed_config_hash_path="$runtime_dir/deployed-config-hash.txt"
bot_pid_path="$runtime_dir/bot.pid"
runtime_mode_path="$runtime_dir/runtime-mode.txt"
current_binary="$runtime_dir/asg-bot"
next_binary="$runtime_dir/asg-bot.next"
previous_binary="$runtime_dir/asg-bot.previous"
lock_dir="$runtime_dir/deploy.lock"
headless_root="$state_root/headless-meet"
headless_supervisor_pid_path="$headless_root/runtime/supervisor.pid"

log() {
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$deploy_log"
}

sync_mobile_extension() {
    local script="$deploy_root/scripts/deploy/sync-meet-monitor-termux.sh"
    if [[ ! -x "$script" ]]; then
        return
    fi
    if ! "$script" \
        --deploy-root "$deploy_root" \
        --state-root "$state_root" >>"$deploy_log" 2>&1; then
        log "warning: mobile Meet Monitor sync failed"
    fi
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
        nohup env \
            ASG_BOT_CONFIG_FILE="$config_path" \
            ASG_BOT_RUNTIME_MODE="phone-bot" \
            "$current_binary" >>"$bot_stdout" 2>>"$bot_stderr" </dev/null &
        echo "$!" >"$bot_pid_path"
    )
    sleep 2
    bot_is_running
}

headless_monitor_is_running() {
    local pid
    pid="$(read_pid "$headless_supervisor_pid_path")" || return 1
    kill -0 "$pid" 2>/dev/null
}

stop_headless_monitor() {
    local pid
    pid="$(read_pid "$headless_supervisor_pid_path")" || {
        rm -f "$headless_supervisor_pid_path"
        return
    }
    kill "$pid" 2>/dev/null || true
    for _ in {1..30}; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.2
    done
    kill -KILL "$pid" 2>/dev/null || true
    rm -f "$headless_supervisor_pid_path"
}

manage_headless_monitor() {
    local target="$1"
    local supervisor="$deploy_root/scripts/deploy/supervise-headless-meet-termux.sh"
    if [[ "$target" != "termux" ]]; then
        if headless_monitor_is_running; then
            stop_headless_monitor
            log "stopped headless Meet monitor because active target is $target"
        fi
        return
    fi
    if [[ ! -f "$headless_root/installed" ||
          ! -f "$headless_root/profile-ready" ]]; then
        return
    fi
    if [[ ! -x "$supervisor" ]]; then
        log "warning: headless Meet supervisor is not executable"
        return
    fi
    if ! headless_monitor_is_running; then
        mkdir -p "$headless_root/logs" "$headless_root/runtime"
        nohup "$supervisor" \
            --deploy-root "$deploy_root" \
            --state-root "$state_root" \
            >>"$headless_root/logs/supervisor.log" 2>&1 </dev/null &
        log "started headless Meet monitor supervisor"
    fi
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
active_monitor_target="$(
    git -C "$deploy_root" show \
        "${remote_commit}:scripts/deploy/active-meet-monitor-target.txt" |
        tr -d '[:space:]'
)"
if [[ "$active_target" != "windows" && "$active_target" != "termux" ]]; then
    log "deployment failed: invalid active bot target '$active_target'"
    exit 1
fi
if [[ "$active_monitor_target" != "windows" &&
      "$active_monitor_target" != "termux" ]]; then
    log "deployment failed: invalid active Meet monitor target '$active_monitor_target'"
    exit 1
fi
if [[ "$active_monitor_target" == "termux" &&
      "$active_target" != "termux" ]]; then
    log "deployment failed: Termux Meet monitor requires the Termux bot"
    exit 1
fi
if [[ "$active_target" == "termux" ]] && ! command -v cloudflared >/dev/null 2>&1; then
    log "installing cloudflared for the remote Meet relay"
    if ! pkg install -y cloudflared >>"$deploy_log" 2>&1; then
        log "deployment failed: cloudflared installation failed"
        exit 1
    fi
fi
deployed_commit=""
[[ -f "$deployed_commit_path" ]] &&
    deployed_commit="$(tr -d '[:space:]' <"$deployed_commit_path")"
config_hash="$(sha256sum "$config_path" | cut -d' ' -f1)"
deployed_config_hash=""
[[ -f "$deployed_config_hash_path" ]] &&
    deployed_config_hash="$(tr -d '[:space:]' <"$deployed_config_hash_path")"

if [[ "$deployed_commit" == "$remote_commit" && -x "$current_binary" ]]; then
    sync_mobile_extension
    manage_headless_monitor "$active_monitor_target"
    if [[ "$active_target" != "termux" ]]; then
        if bot_is_running; then
            stop_bot
            log "stopped bot because active target is $active_target"
        fi
        printf '%s' "inactive" >"$runtime_mode_path"
        exit 0
    fi
    deployed_runtime_mode=""
    [[ -f "$runtime_mode_path" ]] &&
        deployed_runtime_mode="$(tr -d '[:space:]' <"$runtime_mode_path")"
    if bot_is_running &&
        [[ "$deployed_config_hash" != "$config_hash" ||
           "$deployed_runtime_mode" != "phone-bot" ]]; then
        stop_bot
        if start_bot; then
            printf '%s' "$config_hash" >"$deployed_config_hash_path"
            printf '%s' "phone-bot" >"$runtime_mode_path"
            log "restarted current commit $remote_commit after runtime/config change"
        else
            log "deployment failed: bot exited after config change"
            exit 1
        fi
        exit 0
    fi
    if ! bot_is_running; then
        if start_bot; then
            printf '%s' "$config_hash" >"$deployed_config_hash_path"
            printf '%s' "phone-bot" >"$runtime_mode_path"
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

sync_mobile_extension
manage_headless_monitor "$active_monitor_target"
stop_bot
rm -f "$previous_binary"
[[ -f "$current_binary" ]] && mv "$current_binary" "$previous_binary"
mv "$next_binary" "$current_binary"
chmod 700 "$current_binary"

if [[ "$active_target" != "termux" ]]; then
    printf '%s' "$remote_commit" >"$deployed_commit_path"
    printf '%s' "$config_hash" >"$deployed_config_hash_path"
    printf '%s' "inactive" >"$runtime_mode_path"
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
printf '%s' "$config_hash" >"$deployed_config_hash_path"
printf '%s' "phone-bot" >"$runtime_mode_path"
log "deployed commit $remote_commit"
