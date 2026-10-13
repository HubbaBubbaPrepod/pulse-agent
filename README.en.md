# Pulse Agent

[Pulse website](https://pulse.hubba-cloud.ru/en/) · [Practical guides](https://pulse.hubba-cloud.ru/en/guides/)

[Русский](README.md) · **English**

Native Linux/Windows telemetry agent for [Pulse Monitor](https://github.com/HubbaBubbaPrepod/pulse-monitor). Sends host metrics over HTTPS and supports optional native collector modules.

The agent collects CPU/memory, processes, disks, connections, system logs, and available GPU metrics. It sends reports to the panel without opening an inbound port on the host.

[![CI](https://github.com/HubbaBubbaPrepod/pulse-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/HubbaBubbaPrepod/pulse-agent/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/HubbaBubbaPrepod/pulse-agent)](https://github.com/HubbaBubbaPrepod/pulse-agent/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[Quick start](#quick-start) · [Services](#services) · [Arguments](#arguments) · [Data availability](#data-availability) · [Support](SUPPORT.md)

## Pulse ecosystem

| Repository | Purpose |
| --- | --- |
| [Pulse Monitor](https://github.com/HubbaBubbaPrepod/pulse-monitor) | Panel, users, hosts, history storage, and module management |
| [Pulse Agent](https://github.com/HubbaBubbaPrepod/pulse-agent) | Native metric collection and outbound reports from each host |
| [Pulse Module Docker](https://github.com/HubbaBubbaPrepod/pulse-module-docker) | Separate Docker container statistics module |

## Quick start

1. Install [Pulse Monitor](https://github.com/HubbaBubbaPrepod/pulse-monitor/blob/main/README.en.md): create the owner on loopback, then enable HTTPS. Add a host and generate a token in its settings.
2. Download an archive and `checksums.txt` from [Releases](https://github.com/HubbaBubbaPrepod/pulse-agent/releases): `pulse-agent_v0.4.0_linux_amd64.tar.gz`, `pulse-agent_v0.4.0_linux_arm64.tar.gz`, or `pulse-agent_v0.4.0_windows_amd64.zip`. Verify SHA-256 (`sha256sum <archive>` or `Get-FileHash <archive> -Algorithm SHA256`) and extract it. Go and Node.js are not required to run it.
3. Save the token without quotes in `agent.token`. Linux: `chmod 600 agent.token`; Windows: restrict its ACL to the service account, SYSTEM, and administrators. Do not pass the token in a URL or command line.
4. Run from the program directory:

   ```bash
   ./pulse-agent -url https://monitor.example.com:8443 -server SERVER_ID -token-file agent.token -state agent-state
   ```

   On Windows:

   ```powershell
   ./pulse-agent.exe -url https://monitor.example.com:8443 -server SERVER_ID -token-file agent.token -state agent-state
   ```

   Give each host a separate ID, token, and state directory. Do not run two agents with the same token. Check the host's real data in the panel.

Local development allows `-url http://127.0.0.1:8080`. A remote URL must be an HTTPS origin without a path, query, or credentials. Certificates are verified; there is no option to disable verification. For an internal CA, configure operating system trust.

## Services

Linux, from the extracted archive:

```bash
sudo bash deployment/install-agent.sh https://monitor.example.com:8443 SERVER_ID ./agent.token .
sudo systemctl status pulse-agent
sudo journalctl -u pulse-agent -f
```

The installer creates the `pulse-agent` service, places the binary in `/opt/pulse-agent`, state in `/var/lib/pulse-agent`, and the token in `/etc/pulse-agent/agent.token`. The example runs as root for system log and process access. For fewer privileges, configure the service user and access to required resources; data visibility will decrease.

Windows, in an administrative PowerShell:

```powershell
./deployment/install-agent.ps1 -PanelURL https://monitor.example.com:8443 -ServerID SERVER_ID -TokenFile C:\Pulse\agent.token -AgentExe C:\Pulse\pulse-agent.exe
```

The `PulseAgent` service starts automatically as LocalSystem. Its default directory is `C:\ProgramData\PulseAgent`. The installer copies the binary and token there and restricts directory/token ACLs to SYSTEM and administrators. Protect the source token as well.

### Installer arguments

Linux: `install-agent.sh <panel-url> <server-id> <token-file> [package-dir]`. The optional package directory defaults to the install script's parent directory.

Windows: `-PanelURL`, `-ServerID`, and `-TokenFile` are required; `-AgentExe` defaults to `pulse-agent.exe` in the script's parent directory; `-StateDirectory` defaults to `C:\ProgramData\PulseAgent` and also holds the installed executable and token.

## Arguments

| Argument | Meaning |
| --- | --- |
| `-url` | Panel origin; default `http://127.0.0.1:8080` |
| `-server` | Required ID of a saved host |
| `-token-file` | Token file path; default `agent.token` |
| `-state` | Persistent state; default `agent-state` |
| `-interval` | Sampling interval from `500ms` to `5s`; default `500ms` |
| `-capture` | TShark interface; empty disables capture |
| `-check` | Local diagnostics without a token or connection |

## Data availability

| Platform | Release architecture | Service | Limits |
| --- | --- | --- | --- |
| Linux | amd64, arm64 | systemd | Process, connection, and log visibility depends on OS permissions |
| Windows | amd64 | Windows Service / LocalSystem by default | CIM/GPU Engine and capture depend on drivers and permissions |

GPU metrics depend on the OS, driver, and `nvidia-smi`; some Windows metrics are available through CIM/GPU Engine. Full Intel/AMD telemetry is not guaranteed. Missing metrics are marked unavailable, rather than zero.

Connections and aggregate transfer rates work without extra programs. Install TShark separately for packet metadata; Windows requires Npcap and capture permissions. List interfaces with `tshark -D`, then add `-capture 1` or an interface name. The agent does not install drivers. It does not save packet payloads, associate packets with PIDs, or calculate transfer rates for individual connections. Capture is a metadata sample limited to 256 packets per report; protocol summaries can still contain sensitive information.

OS permissions determine process, connection, and log visibility. Process command lines and system logs can contain sensitive information; account for this when granting panel access.

## Modules and compatibility

Use Pulse Monitor `v0.4.0` with Pulse Agent `v0.4.0` for the first public release. The report contract is documented as v1; reports do not include a version negotiation field. Modules explicitly use `protocol: 1`. Check the changelog before upgrading while versions remain below v1.0.

A module runs arbitrary native code with the agent's privileges, without a sandbox. `permissions` is descriptive metadata, not an OS restriction. Install only trusted packages through the panel. See the [package and process format](https://github.com/HubbaBubbaPrepod/pulse-monitor/blob/main/docs/MODULES.en.md).

The Docker collector is released separately as [Pulse Module Docker](https://github.com/HubbaBubbaPrepod/pulse-module-docker). Download `docker-module.json` v1.0.2 from its Releases and upload it in the panel's module settings. The package uses protocol v1 and contains builds for Linux amd64/arm64 and Windows amd64. The collector issues read requests, but the Docker socket grants privileged host access; read-only requests alone do not make that access safe.

## Update, removal, and diagnostics

Stop the service, replace the binary, and start it again; keep the token and state directory. State contains `pending.json` (one report awaiting delivery), `log-cursor`, and downloaded modules. If the panel is unavailable, the agent retries one report with a delay capped at 30 seconds; it is not an unlimited queue of offline metrics. After rotating a token, update the file and restart.

Linux: `sudo systemctl stop pulse-agent`, then `sudo systemctl start pulse-agent` after replacement. To remove it, run `sudo systemctl disable --now pulse-agent`, delete the unit and program directory, and run `sudo systemctl daemon-reload`. Windows: `Stop-Service PulseAgent`, then `Start-Service PulseAgent` after replacement; to remove it, run `sc.exe delete PulseAgent` after stopping. Delete state and tokens separately only when no longer needed.

For problems, run `./pulse-agent -check` (`./pulse-agent.exe -check` on Windows) and check permissions, ID, token file, URL, TLS, and outbound access. HTTP 401 means an invalid or revoked token. After 15 seconds without reports, the panel marks the host offline. Check OS capabilities and architecture for unavailable metrics or module `unsupported` statuses.

## Development and license

Go 1.23+: `go build .`, `go test ./...`, `go vet ./...`. To build platform archives, use PowerShell 7.4+ and `./scripts/build.ps1 -Version v0.4.0`; Go must be in PATH or supplied through `-GoExe`. See [contributing](CONTRIBUTING.md).

[Issues](https://github.com/HubbaBubbaPrepod/pulse-agent/issues) · [Changelog](CHANGELOG.md) · [MIT](LICENSE). Dependencies retain their own licenses.

[Security policy](SECURITY.md) · [Support](SUPPORT.md) · [Code of conduct](CODE_OF_CONDUCT.md)
