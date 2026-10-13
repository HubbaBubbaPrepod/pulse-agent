# Участие в разработке

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

Вклад распространяется под [MIT](LICENSE). Не публикуйте токены, чувствительные журналы и закрытые ключи в Issues. Для уязвимости используйте приватный GitHub Security канал, если он доступен.
