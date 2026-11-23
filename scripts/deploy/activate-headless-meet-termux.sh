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
state_root="$(cd "$state_root" && pwd -P)"
headless_root="$state_root/headless-meet"
profile_root="$headless_root/profile"
target_path="$deploy_root/scripts/deploy/active-meet-monitor-target.txt"

if [[ ! -f "$headless_root/installed" ||
      ! -f "$headless_root/extension/manifest.json" ]]; then
    echo "Run install-headless-meet-termux.sh first." >&2
    exit 1
fi
if [[ ! -f "$profile_root/Default/Cookies" &&
      ! -f "$profile_root/Default/Network/Cookies" ]]; then
    echo "The Chromium profile has no login cookies. Complete --setup first." >&2
    exit 1
fi

"$deploy_root/scripts/deploy/run-headless-meet-termux.sh" \
    --deploy-root "$deploy_root" \
    --state-root "$state_root" \
    --setup \
    --check

{
    printf 'activated_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'source_commit=%s\n' "$(git -C "$deploy_root" rev-parse HEAD)"
} >"$headless_root/profile-ready"
chmod 600 "$headless_root/profile-ready"

target="$(tr -d '[:space:]' <"$target_path")"
echo "Headless Meet profile is ready."
if [[ "$target" == "termux" ]]; then
    echo "Monitor target is termux; the supervisor will start on the next deploy cycle."
else
    echo "Monitor target is $target. Switch active-meet-monitor-target.txt to termux when ready."
fi
echo "Acceptance command:"
echo "  $deploy_root/scripts/deploy/check-headless-meet-termux.sh --deploy-root \"$deploy_root\" --state-root \"$state_root\" --duration 1800"
