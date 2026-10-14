# Участие в разработке

> [English version](CONTRIBUTING.md)

Используйте Go 1.23+. Клонируйте репозиторий, затем:

```powershell
go build .
go test ./...
go test -tags integration ./...
go vet ./...
```

Integration-проверки используют локальную ОС: результат зависит от доступных ресурсов и прав. Race-проверка `go test -race ./...` требует CGO и C-компилятора. Для проверки Windows/Linux собирайте соответствующие платформы; runtime-проверка должна выполняться на целевой ОС.

Диагностика: `go run . -check`. Для подключения нужен [Pulse Monitor](https://github.com/HubbaBubbaPrepod/pulse-monitor), сохранённый узел и token-file. Не добавляйте credentials в тесты или commit. Docker-модуль разрабатывается и проверяется в [собственном репозитории](https://github.com/HubbaBubbaPrepod/pulse-module-docker).

Откройте issue с версиями обеих программ, ОС/архитектурой и воспроизводимым сценарием. PR должен содержать минимальное исправление, проверки, ограничения и обновлённый changelog для пользовательских изменений. Коммиты: `type(scope): summary`, до 72 символов. Изменение HTTP или module-протокола согласуйте с панелью и обновите её [документацию](https://github.com/HubbaBubbaPrepod/pulse-monitor/blob/main/docs/PROTOCOL.md).

Вклад распространяется под [MIT](LICENSE). Не публикуйте токены, чувствительные журналы и закрытые ключи в Issues. Для уязвимости используйте [приватное сообщение GitHub Security](https://github.com/HubbaBubbaPrepod/pulse-agent/security/advisories/new); подробнее в [политике безопасности](SECURITY.ru.md).

Публичная документация должна оставаться полной на русском и английском. CI запускает vet и unit-тесты на Linux/Windows, race-тесты — на Linux; это не проверка установки службы на каждом реальном узле.

Для платформенных архивов нужны PowerShell 7.4+ и `./scripts/build.ps1 -Version v0.4.0`. Параметры: `-Version` — версия архива, `-GoExe` — команда/путь Go, `-GoToolchain` — toolchain (по умолчанию `go1.27.2`). `-SkipFrontend` относится к панели и не нужен агенту. Сборка сохраняет архивы, `checksums.txt` и staging-каталог в `artifacts/releases`; staging-файлы автоматически не удаляются.

## Форматирование

Для форматирования при разработке нужен Node.js 22.13+. Закреплённая локальная версия устанавливается командой `npm ci`. Для запуска Go-бинарников Node.js не требуется.

```shell
npm run format
npm run format:check
```

Prettier форматирует поддерживаемые исходники, JSON, CSS, Markdown и YAML. Для Go используется `gofmt`. Генерируемые файлы, локальные данные и файлы подтверждения исключены через `.prettierignore`. CI проверяет форматирование изменений.
