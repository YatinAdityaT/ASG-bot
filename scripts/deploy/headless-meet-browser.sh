#!/usr/bin/env bash
set -Eeuo pipefail

state_root="${ASG_HEADLESS_STATE:-/opt/asg-headless}"
extension_root="${ASG_HEADLESS_EXTENSION:-$state_root/extension}"
profile_root="${ASG_HEADLESS_PROFILE:-$state_root/profile}"
meeting_code="${ASG_MEET_CODE:?ASG_MEET_CODE is required}"
display_number="${ASG_HEADLESS_DISPLAY:-:99}"
setup_mode="${ASG_HEADLESS_SETUP:-false}"
cdp_port="${ASG_HEADLESS_CDP_PORT:-9222}"
trace_mode="${ASG_HEADLESS_TRACE:-false}"

mkdir -p "$profile_root" "$state_root/runtime" "$state_root/logs"
chmod 700 "$profile_root"

if [[ ! -f "$extension_root/manifest.json" ]]; then
    echo "Headless Meet extension is missing: $extension_root/manifest.json" >&2
    exit 1
fi
chromium_command=""
for candidate in chromium chromium-browser; do
    if command -v "$candidate" >/dev/null 2>&1; then
        chromium_command="$candidate"
        break
    fi
done
if [[ -z "$chromium_command" ]]; then
    echo "Required browser command is missing: chromium" >&2
    exit 1
fi
for command_name in dbus-run-session python; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "Required browser command is missing: $command_name" >&2
        exit 1
    fi
done

display_pid=""
browser_pid=""
cleanup() {
    [[ -n "$browser_pid" ]] && kill "$browser_pid" 2>/dev/null || true
    [[ -n "$display_pid" ]] && kill "$display_pid" 2>/dev/null || true
    wait "$browser_pid" "$display_pid" 2>/dev/null || true
    rm -f "$state_root/runtime/chromium.pid" "$state_root/runtime/xvfb.pid"
}
trap cleanup EXIT INT TERM

export DISPLAY="$display_number"
if [[ "$setup_mode" != "true" ]]; then
    x11_socket_root="${TMPDIR:-/tmp}/.X11-unix"
    rm -f \
        "${TMPDIR:-/tmp}/.X${display_number#:}-lock" \
        "$x11_socket_root/X${display_number#:}"
    Xvfb "$display_number" \
        -screen 0 1280x720x24 \
        -nolisten tcp \
        -noreset \
        >"$state_root/logs/xvfb.log" 2>&1 &
    display_pid=$!
    printf '%s' "$display_pid" >"$state_root/runtime/xvfb.pid"
    for _ in {1..50}; do
        [[ -S "$x11_socket_root/X${display_number#:}" ]] && break
        kill -0 "$display_pid" 2>/dev/null || {
            echo "Xvfb exited during startup." >&2
            exit 1
        }
        sleep 0.1
    done
fi

preferences_path="$profile_root/Default/Preferences"
if [[ -f "$preferences_path" ]]; then
    python - "$preferences_path" <<'PY'
import json
import os
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as preferences_file:
    preferences = json.load(preferences_file)
preferences.setdefault("extensions", {}).setdefault("ui", {})[
    "developer_mode"
] = True
temporary = path + ".next"
with open(temporary, "w", encoding="utf-8") as preferences_file:
    json.dump(preferences, preferences_file, separators=(",", ":"))
os.replace(temporary, path)
PY
fi

rm -f \
    "$profile_root/SingletonCookie" \
    "$profile_root/SingletonLock" \
    "$profile_root/SingletonSocket"
# The supervisor supplies the only Meet tab. Restoring Chromium's unclean
# session creates duplicate Meet tabs that compete for extension ownership.
rm -rf "$profile_root/Default/Sessions"
chromium_flags=(
    "--user-data-dir=$profile_root"
    "--load-extension=$extension_root"
    "--disable-extensions-except=$extension_root"
    "--remote-debugging-address=127.0.0.1"
    "--remote-debugging-port=$cdp_port"
    "--no-sandbox"
    "--disable-dev-shm-usage"
    "--disable-crash-reporter"
    "--no-first-run"
    "--no-default-browser-check"
    "--disable-background-timer-throttling"
    "--disable-backgrounding-occluded-windows"
    "--disable-renderer-backgrounding"
    "--disable-features=CalculateNativeWinOcclusion,IntensiveWakeUpThrottling,MemorySaverMode"
    "--disable-gpu"
    "--disable-vulkan"
    "--in-process-gpu"
    "--single-process"
    "--enable-features=NetworkServiceInProcess"
    "--no-proxy-server"
    "--renderer-process-limit=3"
    "--window-size=1280,720"
)

browser_command=("$chromium_command")
if [[ "$trace_mode" == "true" ]]; then
    command -v strace >/dev/null 2>&1 || {
        echo "Browser tracing requested, but strace is missing." >&2
        exit 1
    }
    browser_command=(
        strace -f -s 256
        -o "$state_root/logs/chromium.strace"
        "$chromium_command"
    )
fi

# Google Play Termux injects libtermux-exec through LD_PRELOAD. Chromium's
# isolated utility processes cannot access that library namespace, so keep
# services in the browser process instead of removing the preload Chromium
# itself needs on Google Play Termux.
dbus-run-session -- "${browser_command[@]}" \
    "${chromium_flags[@]}" \
    "https://meet.google.com/$meeting_code?companion=1" \
    >"$state_root/logs/chromium.log" 2>&1 &
browser_pid=$!
printf '%s' "$browser_pid" >"$state_root/runtime/chromium.pid"
wait "$browser_pid"
