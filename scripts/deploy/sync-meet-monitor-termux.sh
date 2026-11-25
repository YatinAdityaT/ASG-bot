#!/data/data/com.termux/files/usr/bin/bash
set -Eeuo pipefail

deploy_root=""
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"
destination="${ASG_BOT_MOBILE_EXTENSION_ROOT:-/sdcard/Download/ASG-Meet-Monitor}"
headless_destination=""

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
        --destination)
            destination="${2:?missing --destination value}"
            shift 2
            ;;
        --headless-destination)
            headless_destination="${2:?missing --headless-destination value}"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [[ -z "$deploy_root" ]]; then
    echo "Usage: $0 --deploy-root PATH [--state-root PATH] [--destination PATH]" >&2
    exit 2
fi

deploy_root="$(cd "$deploy_root" && pwd -P)"
state_root="$(cd "$state_root" && pwd -P)"
config_path="$state_root/config.local.json"
mobile_manifest="$deploy_root/meet-monitor/manifest.mobile.json"
desktop_manifest="$deploy_root/meet-monitor/manifest.json"
headless_destination="${headless_destination:-$state_root/headless-meet/extension}"

if [[ ! -f "$config_path" || ! -f "$mobile_manifest" ||
      ! -f "$desktop_manifest" ]]; then
    echo "Missing bot config or mobile extension manifest." >&2
    exit 1
fi

compact_config="$(tr -d '\r\n' <"$config_path")"
json_string() {
    printf '%s' "$compact_config" |
        sed -nE "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"([^\"]*)\".*/\\1/p"
}
json_boolean() {
    printf '%s' "$compact_config" |
        sed -nE "s/.*\"$1\"[[:space:]]*:[[:space:]]*(true|false).*/\\1/p"
}

api_enabled="$(json_boolean apiEnabled)"
api_addr="$(json_string apiAddr)"
api_token="$(json_string apiToken)"
config_hash="$(sha256sum "$config_path" | cut -d' ' -f1)"
if [[ -n "${ASG_BOT_SOURCE_COMMIT:-}" ]]; then
    source_commit="$ASG_BOT_SOURCE_COMMIT"
else
    source_commit="$(git -C "$deploy_root" rev-parse HEAD)"
fi
source_id="$source_commit:$config_hash"

sync_bundle() {
    local bundle_destination="$1"
    local selected_manifest="$2"
    local generation_prefix="$3"
    local label="$4"
    if [[ -f "$bundle_destination/.source-id" ]] &&
        [[ "$(cat "$bundle_destination/.source-id")" == "$source_id" ]]; then
        return
    fi

    local parent name stage base_url api_url generation_hash
    parent="$(dirname "$bundle_destination")"
    name="$(basename "$bundle_destination")"
    stage="$parent/.${name}.next.$$"
    mkdir -p "$parent"
    rm -rf "$stage"
    mkdir -p "$stage"
    cp -R "$deploy_root/meet-monitor/." "$stage/"
    cp "$selected_manifest" "$stage/manifest.json"
    rm -f "$stage/manifest.mobile.json"
    if [[ "$generation_prefix" == "headless" ]]; then
        python - "$stage/manifest.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as manifest_file:
    manifest = json.load(manifest_file)
scripts = manifest["content_scripts"][0]["js"]
if "src/headless-autojoin.js" not in scripts:
    scripts.append("src/headless-autojoin.js")
with open(path, "w", encoding="utf-8") as manifest_file:
    json.dump(manifest, manifest_file, indent=2)
    manifest_file.write("\n")
PY
    fi

    if [[ "$api_enabled" == "true" && -n "$api_addr" && -n "$api_token" ]]; then
        base_url="${api_addr%/}"
        [[ "$base_url" =~ ^https?:// ]] || base_url="http://$base_url"
        api_url="$base_url/v1/meet-events"
        generation_hash="$(
            printf '%s\0%s' "$api_url" "$api_token" |
                sha256sum |
                cut -c1-16
        )"
        printf '{"url":"%s","token":"%s","generation":"%s-%s"}\n' \
            "$api_url" "$api_token" "$generation_prefix" "$generation_hash" \
            >"$stage/src/local-config.json"
        chmod 600 "$stage/src/local-config.json" 2>/dev/null || true
    else
        rm -f "$stage/src/local-config.json"
    fi

    printf '%s' "$source_id" >"$stage/.source-id"
    if [[ "$generation_prefix" == "headless" ]]; then
        local versioned_destination bundle_hash old_bundle
        bundle_hash="$(
            printf '%s' "$source_id" |
                sha256sum |
                cut -c1-16
        )"
        versioned_destination="$parent/${name}-${bundle_hash}"
        rm -rf "$versioned_destination" "$bundle_destination"
        mv "$stage" "$versioned_destination"
        ln -s "$versioned_destination" "$bundle_destination"
        for old_bundle in "$parent/${name}-"*; do
            [[ "$old_bundle" == "$versioned_destination" ]] ||
                rm -rf "$old_bundle"
        done
    else
        rm -rf "$bundle_destination"
        mv "$stage" "$bundle_destination"
    fi
    echo "$label Meet Monitor synced to $bundle_destination"
}

sync_bundle "$destination" "$mobile_manifest" "mobile" "Mobile"
sync_bundle "$headless_destination" "$desktop_manifest" "headless" "Headless"
chmod -R go-rwx "$headless_destination" 2>/dev/null || true
