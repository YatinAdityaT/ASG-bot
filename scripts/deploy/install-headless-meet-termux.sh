#!/data/data/com.termux/files/usr/bin/bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
deploy_root="$(git -C "$script_dir" rev-parse --show-toplevel)"
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

deploy_root="$(cd "$deploy_root" && pwd -P)"
mkdir -p "$state_root"
state_root="$(cd "$state_root" && pwd -P)"
headless_root="$state_root/headless-meet"

if ! command -v pkg >/dev/null 2>&1; then
    echo "This installer must run inside Termux." >&2
    exit 1
fi
if [[ ! -f "$state_root/config.local.json" ]]; then
    echo "Bot configuration is missing: $state_root/config.local.json" >&2
    exit 1
fi

echo "Installing Termux headless-browser prerequisites..."
pkg install -y python curl jq util-linux x11-repo tur-repo
pkg update -y
pkg install -y \
    chromium \
    dbus \
    termux-x11-nightly \
    xorg-server-xvfb

mkdir -p \
    "$headless_root/extension" \
    "$headless_root/profile" \
    "$headless_root/runtime" \
    "$headless_root/logs"
chmod 700 "$headless_root" "$headless_root/profile"
chmod 700 \
    "$deploy_root/scripts/deploy/headless-meet-browser.sh" \
    "$deploy_root/scripts/deploy/run-headless-meet-termux.sh" \
    "$deploy_root/scripts/deploy/supervise-headless-meet-termux.sh" \
    "$deploy_root/scripts/deploy/activate-headless-meet-termux.sh" \
    "$deploy_root/scripts/deploy/check-headless-meet-termux.sh"

"$deploy_root/scripts/deploy/sync-meet-monitor-termux.sh" \
    --deploy-root "$deploy_root" \
    --state-root "$state_root"

chromium_version="$(chromium --version 2>/dev/null || printf 'unknown')"
{
    printf 'installed_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'runtime=termux-native\n'
    printf 'chromium=%s\n' "$chromium_version"
} >"$headless_root/installed"
chmod 600 "$headless_root/installed"

cat <<EOF
Headless Meet runtime installed.

Before activation:
1. Install/open the Termux:X11 Android app.
2. Run:
   $deploy_root/scripts/deploy/run-headless-meet-termux.sh \\
     --deploy-root "$deploy_root" \\
     --state-root "$state_root" \\
     --setup
3. Sign into Google, confirm Meet loads, then close Chromium.
4. Run:
   $deploy_root/scripts/deploy/activate-headless-meet-termux.sh \\
     --deploy-root "$deploy_root" \\
     --state-root "$state_root"
EOF
