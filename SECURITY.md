# Security policy

> [Русская версия](SECURITY.ru.md)

## Reporting a vulnerability

Use [GitHub Private Vulnerability Reporting](https://github.com/HubbaBubbaPrepod/pulse-agent/security/advisories/new). Do not put exploits, tokens, private keys, private host addresses, or unredacted logs in public issues. Include the agent and panel versions, affected OS/architecture, minimal reproduction, impact, and any mitigation. Test only systems you own or are authorized to administer. There is no published response-time or maintenance SLA.

## Versions and trust model

The first public compatible pair is Pulse Agent v0.4.0 / Pulse Monitor v0.4.0. Use the latest applicable fixes and read [the changelog](CHANGELOG.md) before upgrading; compatibility before v1.0 is not guaranteed. This policy does not promise security backports to older releases.

- Download official release archives and verify `checksums.txt`. Checksums detect corruption or mismatch; they are not an independent signature of the publisher.
- Remote panel connections require HTTPS with certificate verification. Loopback HTTP is allowed for development. Restrict access to the panel and trust internal CAs through the OS.
- Each host needs its own token and state directory. Protect their permissions, revoke a compromised token in the panel, replace the token file, and restart the agent.
- Modules execute native code without a sandbox using the agent's privileges. SHA-256 checks verify downloaded bytes; module `permissions` do not enforce OS isolation. Install only trusted packages.
- Docker socket access is privileged host access, even when the module issues read requests. TShark/Npcap require separate installation and capture permissions.
- Process command lines, logs, connection addresses, and packet protocol summaries can expose sensitive data. Review panel access and redact reports before sharing them.

If a host or module is compromised, stop the affected agent/module, restrict access, preserve relevant logs and state for investigation, and rotate affected credentials. Do not delete history or retry state as an automatic incident response step.
