# Support

> [Русская версия](SUPPORT.ru.md)

Start with [installation and diagnostics](README.en.md#update-removal-and-diagnostics). Run `./pulse-agent -check` (`./pulse-agent.exe -check` on Windows); this collects a local summary without a token or panel connection. Review the output before sharing it because hardware information may identify a host.

Report agent collection, service, and delivery problems in [Pulse Agent Issues](https://github.com/HubbaBubbaPrepod/pulse-agent/issues). Panel/account/history problems belong in [Pulse Monitor Issues](https://github.com/HubbaBubbaPrepod/pulse-monitor/issues); Docker module problems belong in [Pulse Module Docker Issues](https://github.com/HubbaBubbaPrepod/pulse-module-docker/issues).

Include versions, OS/architecture, installation method, exact reproduction, expected/actual behavior, and sanitized diagnostic output. Check host ID, token file permissions, TLS trust, outbound access, and service status. HTTP 401 means an invalid/revoked token. Restricted OS permissions, missing drivers, and unsupported hardware may explain unavailable metrics; packet capture is optional.

Do not include tokens, private keys, private addresses, account data, or unredacted screenshots/logs. Preserve local state and relevant logs while investigating; removing state can lose a pending report or the log cursor. Report vulnerabilities [privately](SECURITY.md). Support is provided through repository issues without a published response-time SLA.
