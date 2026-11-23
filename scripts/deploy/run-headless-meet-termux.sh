#!/data/data/com.termux/files/usr/bin/bash
set -Eeuo pipefail

deploy_root=""
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"
setup_mode=false
check_only=false

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
        --setup)
            setup_mode=true
            shift
            ;;
        --check)
            check_only=true
            shift
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [[ -z "$deploy_root" ]]; then
    echo "Usage: $0 --deploy-root PATH [--state-root PATH] [--setup] [--check]" >&2
    exit 2
fi

deploy_root="$(cd "$deploy_root" && pwd -P)"
mkdir -p "$state_root"
state_root="$(cd "$state_root" && pwd -P)"
config_path="$state_root/config.local.json"
headless_root="$state_root/headless-meet"
extension_root="$headless_root/extension"
profile_root="$headless_root/profile"

for required in \
    "$config_path" \
    "$headless_root/installed" \
    "$extension_root/manifest.json" \
    "$deploy_root/scripts/deploy/headless-meet-browser.sh"; do
    if [[ ! -e "$required" ]]; then
        echo "Headless Meet prerequisite is missing: $required" >&2
        exit 1
    fi
done
if ! $setup_mode && [[ ! -f "$headless_root/profile-ready" ]]; then
    echo "Google Meet profile setup is incomplete. Run with --setup first." >&2
    exit 1
fi
for command_name in proot-distro python; do
    command -v "$command_name" >/dev/null 2>&1 || {
        echo "Missing command: $command_name" >&2
        exit 1
    }
done

meeting_code="$(
    python - "$config_path" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8-sig") as config_file:
    value = str(json.load(config_file).get("meetCode", "")).strip()
if not value:
    raise SystemExit("meetCode is missing from the bot config")
print(value)
PY
)"
if [[ ! "$meeting_code" =~ ^[a-z]{3}-[a-z]{4}-[a-z]{3}$ ]]; then
    echo "Configured Meet code is invalid: $meeting_code" >&2
    exit 1
fi

if $check_only; then
    echo "Headless Meet prerequisites are ready for $meeting_code."
    exit 0
fi

chmod 700 "$profile_root" "$headless_root" 2>/dev/null || true
browser_script="/opt/asg-repo/scripts/deploy/headless-meet-browser.sh"
common_args=(
    login debian
    --shared-tmp
    --bind "$deploy_root:/opt/asg-repo"
    --bind "$headless_root:/opt/asg-headless"
    --
    env
    "ASG_HEADLESS_STATE=/opt/asg-headless"
    "ASG_HEADLESS_EXTENSION=/opt/asg-headless/extension"
    "ASG_HEADLESS_PROFILE=/opt/asg-headless/profile"
    "ASG_MEET_CODE=$meeting_code"
    "ASG_HEADLESS_CDP_PORT=9222"
)

if ! $setup_mode; then
    exec proot-distro "${common_args[@]}" \
        ASG_HEADLESS_DISPLAY=:99 \
        ASG_HEADLESS_SETUP=false \
        "$browser_script"
fi

command -v termux-x11 >/dev/null 2>&1 || {
    echo "Termux:X11 command is missing. Install the Termux:X11 app and package." >&2
    exit 1
}
termux-x11 :0 >/dev/null 2>&1 &
x11_pid=$!
cleanup() {
    kill "$x11_pid" 2>/dev/null || true
    wait "$x11_pid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM
am start -n com.termux.x11/.MainActivity >/dev/null 2>&1 || true
sleep 2

proot-distro "${common_args[@]}" \
    ASG_HEADLESS_DISPLAY=:0 \
    ASG_HEADLESS_SETUP=true \
    "$browser_script"
