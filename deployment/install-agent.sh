#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo 'Run as root' >&2; exit 1; }
[[ $# -ge 3 && $# -le 4 ]] || { echo 'Usage: install-agent.sh <panel-url> <server-id> <token-file> [package-dir]' >&2; exit 1; }
url=$1
server=$2
token_file=$3
package_dir=$(cd "${4:-$(dirname "$0")/..}" && pwd)
[[ -f "$package_dir/pulse-agent" && -f "$token_file" ]] || { echo 'Executable or token file missing' >&2; exit 1; }
for argument in "$url" "$server"; do
 [[ "$argument" != *'"'* && "$argument" != *$'\n'* && "$argument" != *$'\r'* && "$argument" != *'%'* && "$argument" != *'\'* ]] || { echo 'Invalid systemd argument' >&2; exit 1; }
done
token=$(tr -d '\r\n' < "$token_file")
[[ "$token" =~ ^[0-9a-fA-F]{64}$ ]] || { echo 'Token must contain 64 hexadecimal characters' >&2; exit 1; }
install -d -m 0755 /opt/pulse-agent
install -d -m 0700 /var/lib/pulse-agent /etc/pulse-agent
systemctl stop pulse-agent.service 2>/dev/null || true
install -m 0755 "$package_dir/pulse-agent" /opt/pulse-agent/pulse-agent
umask 077
printf '%s\n' "$token" > /etc/pulse-agent/agent.token
cat > /etc/systemd/system/pulse-agent.service <<UNIT
[Unit]
Description=Pulse telemetry agent
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
WorkingDirectory=/var/lib/pulse-agent
ExecStart=/opt/pulse-agent/pulse-agent -url "$url" -server "$server" -token-file /etc/pulse-agent/agent.token -state /var/lib/pulse-agent
Restart=on-failure
RestartSec=5
UMask=0077
NoNewPrivileges=true
TimeoutStopSec=20
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now pulse-agent.service
systemctl --no-pager status pulse-agent.service