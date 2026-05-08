#!/usr/bin/env bash
set -euo pipefail

label="com.cartledger.backup"
hour="3"
minute="0"
allow_dev_data_dir="false"

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
repo_root="$(CDPATH= cd -- "${script_dir}/.." && pwd -P)"
bin_path="${HOME}/.local/bin/cartledger"
data_dir=""

usage() {
  cat <<'USAGE'
Usage: scripts/install-backup-launchd.sh [options]

Installs a macOS launchd job that runs `cartledger backup` daily.

Options:
  --bin PATH               Absolute path to the cartledger CLI binary.
                           Default: ~/.local/bin/cartledger
  --data-dir PATH          Absolute DATA_DIR to back up. Defaults to DATA_DIR
                           from .env in the repo root.
  --repo-root PATH         Repo root containing .env. Default: parent of scripts/
  --hour H                 Local hour for daily backup, 0-23. Default: 3
  --minute M               Local minute for daily backup, 0-59. Default: 0
  --allow-dev-data-dir     Allow DATA_DIR inside the repo working tree.
  -h, --help               Show this help.
USAGE
}

fail() {
  printf 'install-backup-launchd: %s\n' "$*" >&2
  exit 1
}

trim() {
  sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'
}

read_env_value() {
  local key="$1"
  local file="$2"
  local line value

  [[ -f "$file" ]] || return 1
  line="$(grep -E "^[[:space:]]*${key}=" "$file" | tail -n 1 || true)"
  [[ -n "$line" ]] || return 1

  value="${line#*=}"
  value="$(printf '%s' "$value" | sed -e 's/[[:space:]]*#.*$//' | trim)"
  case "$value" in
    \"*\") value="${value#\"}"; value="${value%\"}" ;;
    \'*\') value="${value#\'}"; value="${value%\'}" ;;
  esac
  printf '%s\n' "$value"
}

xml_escape() {
  printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bin)
      [[ $# -ge 2 ]] || fail "--bin requires a path"
      bin_path="$2"
      shift 2
      ;;
    --data-dir)
      [[ $# -ge 2 ]] || fail "--data-dir requires a path"
      data_dir="$2"
      shift 2
      ;;
    --repo-root)
      [[ $# -ge 2 ]] || fail "--repo-root requires a path"
      repo_root="$2"
      shift 2
      ;;
    --hour)
      [[ $# -ge 2 ]] || fail "--hour requires a value"
      hour="$2"
      shift 2
      ;;
    --minute)
      [[ $# -ge 2 ]] || fail "--minute requires a value"
      minute="$2"
      shift 2
      ;;
    --allow-dev-data-dir)
      allow_dev_data_dir="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

case "$bin_path" in
  /*) ;;
  *) fail "--bin must be absolute: $bin_path" ;;
esac
[[ -x "$bin_path" ]] || fail "cartledger binary not executable: $bin_path"

repo_root="$(CDPATH= cd -- "$repo_root" && pwd -P)"
env_file="${repo_root}/.env"

if [[ -z "$data_dir" ]]; then
  data_dir="$(read_env_value DATA_DIR "$env_file" || true)"
fi
[[ -n "$data_dir" ]] || fail "DATA_DIR not provided and not found in ${env_file}"

case "$data_dir" in
  /*) ;;
  *) fail "DATA_DIR must be absolute for launchd backups: $data_dir" ;;
esac

[[ "$hour" =~ ^[0-9]+$ ]] || fail "--hour must be numeric"
[[ "$minute" =~ ^[0-9]+$ ]] || fail "--minute must be numeric"
(( hour >= 0 && hour <= 23 )) || fail "--hour must be between 0 and 23"
(( minute >= 0 && minute <= 59 )) || fail "--minute must be between 0 and 59"

mkdir -p "$data_dir/backups"
data_dir="$(CDPATH= cd -- "$data_dir" && pwd -P)"

case "${data_dir}/" in
  "${repo_root}/"*)
    if [[ "$allow_dev_data_dir" != "true" ]]; then
      fail "DATA_DIR is inside the repo working tree: ${data_dir}. Pass --allow-dev-data-dir only for throwaway dev data."
    fi
    ;;
esac

launch_agents_dir="${HOME}/Library/LaunchAgents"
plist_path="${launch_agents_dir}/${label}.plist"
log_path="${data_dir}/backups/launchd.log"
domain="gui/$(id -u)"

mkdir -p "$launch_agents_dir"

cat > "$plist_path" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$(xml_escape "$label")</string>

  <key>ProgramArguments</key>
  <array>
    <string>$(xml_escape "$bin_path")</string>
    <string>backup</string>
  </array>

  <key>EnvironmentVariables</key>
  <dict>
    <key>DATA_DIR</key>
    <string>$(xml_escape "$data_dir")</string>
  </dict>

  <key>StartCalendarInterval</key>
  <dict>
    <key>Hour</key>
    <integer>${hour}</integer>
    <key>Minute</key>
    <integer>${minute}</integer>
  </dict>

  <key>StandardOutPath</key>
  <string>$(xml_escape "$log_path")</string>
  <key>StandardErrorPath</key>
  <string>$(xml_escape "$log_path")</string>
</dict>
</plist>
PLIST

if launchctl print "${domain}/${label}" >/dev/null 2>&1; then
  launchctl bootout "${domain}/${label}" >/dev/null
fi

launchctl bootstrap "$domain" "$plist_path"
launchctl enable "${domain}/${label}"

cat <<EOF
Installed ${label}
  plist:    ${plist_path}
  binary:   ${bin_path}
  DATA_DIR: ${data_dir}
  schedule: daily at $(printf '%02d:%02d' "$hour" "$minute") local time
  log:      ${log_path}

Run a backup now:
  launchctl kickstart -k ${domain}/${label}
EOF
