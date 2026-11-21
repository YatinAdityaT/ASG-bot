#!/data/data/com.termux/files/usr/bin/bash
set -Eeuo pipefail

deploy_root=""
state_root="${ASG_BOT_DEPLOY_STATE:-$HOME/.local/share/asg-bot-deploy}"
destination="${ASG_BOT_MOBILE_EXTENSION_ROOT:-/sdcard/Download/ASG-Meet-Monitor}"

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

if [[ ! -f "$config_path" || ! -f "$mobile_manifest" ]]; then
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
source_commit="$(git -C "$deploy_root" rev-parse HEAD)"
source_id="$source_commit:$config_hash"

if [[ -f "$destination/.source-id" ]] &&
    [[ "$(cat "$destination/.source-id")" == "$source_id" ]]; then
    exit 0
fi

parent="$(dirname "$destination")"
name="$(basename "$destination")"
stage="$parent/.${name}.next.$$"
mkdir -p "$parent"
rm -rf "$stage"
mkdir -p "$stage"
cp -R "$deploy_root/meet-monitor/." "$stage/"
mv "$stage/manifest.mobile.json" "$stage/manifest.json"

if [[ "$api_enabled" == "true" && -n "$api_addr" && -n "$api_token" ]]; then
    base_url="${api_addr%/}"
    [[ "$base_url" =~ ^https?:// ]] || base_url="http://$base_url"
    api_url="$base_url/v1/meet-events"
    generation_hash="$(
        printf '%s\0%s' "$api_url" "$api_token" |
            sha256sum |
            cut -c1-16
    )"
    printf '{"url":"%s","token":"%s","generation":"mobile-%s"}\n' \
        "$api_url" "$api_token" "$generation_hash" \
        >"$stage/src/local-config.json"
else
    rm -f "$stage/src/local-config.json"
fi

printf '%s' "$source_id" >"$stage/.source-id"
rm -rf "$destination"
mv "$stage" "$destination"
echo "Mobile Meet Monitor synced to $destination"
