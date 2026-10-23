# Changelog

> [Русская версия](CHANGELOG.ru.md)

## Unreleased

- Update the existing Windows support dependency to fix Unicode length validation; source builds now require Go 1.25+, release builds use Go 1.27.3.

- Clean up native module descendants on timeout, cancellation and normal exit using Linux process groups / Windows Jobs.
- Kill a suspended Windows root even when cancellation precedes job assignment.
- Build release archives with Go 1.27.3. These lifetime controls do not sandbox trusted native code.

## [0.5.0] — 2026-10-18

- Add optional `-connect-address IP:port` for direct Monitor routing while preserving the original HTTP Host, TLS SNI and certificate verification. Useful for an agent on the Monitor host behind a public proxy.

- Add independent native v2 invocation transport, exact artifact pinning, grant checks, cancellation and persistent at-most-once manual execution receipts. Bound/redact public results and preserve v1 telemetry.

### Changed

- Complete paired Russian and English documentation for installation, platform capabilities, contributing, support, and private vulnerability reports.
- Updated the separate Docker module reference to v1.0.2.

## [0.4.0] — first public release

### Added

- Standalone Linux amd64/arm64 and Windows amd64 application for Pulse Monitor.
- CPU/memory, processes, disks, connections, OS logs, and available GPU telemetry; optional packet metadata through an installed TShark.
- HTTPS reports using a separate host token, persistence of one report for retry, and a log cursor.
- Experimental module protocol v1: download of SHA-256-verified binaries, separate process execution, and JSON results.
- Separate pulse-module-docker collector, Linux/systemd and Windows services, platform archives, and SHA-256 checksums.

### Notes

- Compatible pair for the first public release: Pulse Monitor v0.4.0 / Pulse Agent v0.4.0.
- Modules run without a sandbox using the agent's privileges. GPU metrics and logs depend on permissions, the OS, and drivers.
- Future compatibility is not guaranteed before v1.0; incompatible changes will be documented here.
