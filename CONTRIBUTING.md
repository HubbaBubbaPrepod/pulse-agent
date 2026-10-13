# Contributing

> [Русская версия](CONTRIBUTING.ru.md)

Use Go 1.23+. Clone the repository, then run:

```powershell
go build .
go test ./...
go test -tags integration ./...
go vet ./...
```

Integration checks use the local OS: results depend on available resources and permissions. `go test -race ./...` requires CGO and a C compiler. Build for the relevant Windows/Linux platforms; runtime checks must run on the target OS.

Diagnostics: `go run . -check`. Connecting requires [Pulse Monitor](https://github.com/HubbaBubbaPrepod/pulse-monitor), a saved host, and a token file. Do not add credentials to tests or commits. The Docker module is developed and tested in its [own repository](https://github.com/HubbaBubbaPrepod/pulse-module-docker).

Open an issue with both program versions, OS/architecture, and reproducible steps. A PR should contain a minimal fix, validation, limitations, and an updated changelog for user-visible changes. Commit subjects: `type(scope): summary`, up to 72 characters. Coordinate HTTP or module protocol changes with the panel and update its [documentation](https://github.com/HubbaBubbaPrepod/pulse-monitor/blob/main/docs/PROTOCOL.en.md).

Contributions are licensed under [MIT](LICENSE). Do not publish tokens, sensitive logs, or private keys in Issues. Report vulnerabilities through [GitHub's private security channel](https://github.com/HubbaBubbaPrepod/pulse-agent/security/advisories/new); see the [security policy](SECURITY.md).

Keep public documentation complete in both Russian and English. CI runs vet and unit tests on Linux/Windows, and race tests on Linux; this does not establish service installation success on every real host.

Platform archives require PowerShell 7.4+ and `./scripts/build.ps1 -Version v0.4.0`. Parameters: `-Version` sets the archive version, `-GoExe` selects the Go command/path, and `-GoToolchain` selects the toolchain (default `go1.27.2`). `-SkipFrontend` applies to the panel and is unnecessary for the agent. The build retains archives, `checksums.txt`, and a staging directory under `artifacts/releases`; staging files are not automatically deleted.
