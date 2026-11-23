#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
if [[ -n "${ASG_TEST_DEPLOY_ROOT:-}" ]]; then
    deploy_root="$ASG_TEST_DEPLOY_ROOT"
else
    deploy_root="$(git -C "$script_dir" rev-parse --show-toplevel)"
fi
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
mkdir -p "$temporary/fake-bin"
if ! command -v python >/dev/null 2>&1; then
    cat >"$temporary/fake-bin/python" <<'SH'
#!/usr/bin/env bash
exec python3 "$@"
SH
    chmod +x "$temporary/fake-bin/python"
    export PATH="$temporary/fake-bin:$PATH"
fi

scripts=(
    activate-headless-meet-termux.sh
    auto-deploy-termux.sh
    check-headless-meet-termux.sh
    headless-meet-browser.sh
    install-auto-deploy-termux.sh
    install-headless-meet-termux.sh
    run-headless-meet-termux.sh
    supervise-headless-meet-termux.sh
    supervise-termux.sh
    sync-meet-monitor-termux.sh
)
for script in "${scripts[@]}"; do
    bash -n "$script_dir/$script"
done

cat >"$temporary/config.local.json" <<'JSON'
{
  "adminNumbers": ["916355442207"],
  "allowedGroups": ["120363418911552290@g.us"],
  "dbPath": "whatsmeow.db",
  "logLevel": "INFO",
  "apiEnabled": true,
  "apiAddr": "127.0.0.1:8080",
  "meetCode": "jpy-ymgz-tdo",
  "apiToken": "0123456789abcdef0123456789abcdef",
  "apiTargetGroup": "120363418911552290@g.us",
  "meetNotificationGroup": "120363418911552290@g.us"
}
JSON

ASG_BOT_SOURCE_COMMIT="test-commit" \
    bash "$script_dir/sync-meet-monitor-termux.sh" \
    --deploy-root "$deploy_root" \
    --state-root "$temporary" \
    --destination "$temporary/mobile" \
    --headless-destination "$temporary/headless"

python - "$temporary" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
mobile = json.loads((root / "mobile" / "manifest.json").read_text())
headless = json.loads((root / "headless" / "manifest.json").read_text())
mobile_config = json.loads((root / "mobile" / "src" / "local-config.json").read_text())
headless_config = json.loads((root / "headless" / "src" / "local-config.json").read_text())

assert "sidePanel" not in mobile.get("permissions", [])
assert "sidePanel" in headless.get("permissions", [])
assert mobile_config["generation"].startswith("mobile-")
assert headless_config["generation"].startswith("headless-")
assert mobile_config["token"] == headless_config["token"]
assert (root / "mobile" / ".source-id").read_text()
assert (root / "headless" / ".source-id").read_text()
PY

mkdir -p "$temporary/headless-meet/extension"
cp -R "$temporary/headless/." "$temporary/headless-meet/extension/"
touch "$temporary/headless-meet/installed"
cat >"$temporary/fake-bin/proot-distro" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$temporary/fake-bin/proot-distro"
PATH="$temporary/fake-bin:$PATH" \
    bash "$script_dir/run-headless-meet-termux.sh" \
        --deploy-root "$deploy_root" \
        --state-root "$temporary" \
        --setup \
        --check |
    grep -Fq "Headless Meet prerequisites are ready for jpy-ymgz-tdo."

[[ "$(tr -d '[:space:]' <"$script_dir/active-meet-monitor-target.txt")" == "windows" ]]
echo "Headless Meet Termux script tests passed."
