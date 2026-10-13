# Pulse Agent

Native Linux/Windows telemetry agent for [Pulse Monitor](https://github.com/HubbaBubbaPrepod/pulse-monitor). Sends host metrics over HTTPS, with optional native collector modules. MIT licensed.

Агент собирает CPU/память, процессы, диски, соединения, системные журналы и доступные GPU-показатели. Он отправляет отчёты в панель и не открывает входящий порт на узле.

## Быстрый старт

1. Установите [Pulse Monitor](https://github.com/HubbaBubbaPrepod/pulse-monitor#быстрый-старт): создайте владельца на loopback, затем включите HTTPS. Добавьте узел и в его настройках создайте токен.
2. Скачайте архив и `checksums.txt` из [Releases](https://github.com/HubbaBubbaPrepod/pulse-agent/releases): `pulse-agent_v0.4.0_linux_amd64.tar.gz`, `pulse-agent_v0.4.0_linux_arm64.tar.gz` или `pulse-agent_v0.4.0_windows_amd64.zip`. Сверьте SHA-256 (`sha256sum <archive>` или `Get-FileHash <archive> -Algorithm SHA256`) и распакуйте. Go и Node.js для запуска не нужны.
3. Сохраните токен без кавычек в `agent.token`. Linux: `chmod 600 agent.token`; Windows: ограничьте ACL учётной записью службы, SYSTEM и администраторами. Не передавайте токен в URL или командной строке.
4. Запустите из каталога программы:

   ```text
   pulse-agent -url https://monitor.example.com:8443 -server SERVER_ID -token-file agent.token -state agent-state
   ```

   PowerShell: `./pulse-agent.exe`. Каждому узлу — отдельные ID, токен и состояние. Два агента с одним токеном запускать нельзя. Посмотрите реальные данные узла в панели.

Для локальной разработки разрешён `-url http://127.0.0.1:8080`. Удалённый URL должен быть HTTPS origin без пути, query или credentials; сертификат проверяется, режима отключения проверки нет. Для внутреннего CA настройте доверие ОС.

## Службы

Linux, из распакованного архива:

```bash
sudo bash deployment/install-agent.sh https://monitor.example.com:8443 SERVER_ID ./agent.token .
sudo systemctl status pulse-agent
sudo journalctl -u pulse-agent -f
```

Устанавливается служба `pulse-agent`, бинарник в `/opt/pulse-agent`, состояние в `/var/lib/pulse-agent`, токен в `/etc/pulse-agent/agent.token`. Пример работает от root для системных журналов и процессов. Для минимальных прав настройте пользователя службы и доступ к требуемым ресурсам; видимость данных уменьшится.

Windows, административный PowerShell:

```powershell
./deployment/install-agent.ps1 -PanelURL https://monitor.example.com:8443 -ServerID SERVER_ID -TokenFile C:\Pulse\agent.token -AgentExe C:\Pulse\pulse-agent.exe
```

Служба `PulseAgent` запускается автоматически от LocalSystem; состояние по умолчанию `C:\ProgramData\PulseAgent`. Ограничьте ACL токена и состояния до включения службы.

## Аргументы

| Аргумент | Значение |
| --- | --- |
| `-url` | Origin панели; по умолчанию `http://127.0.0.1:8080` |
| `-server` | Обязательный ID сохранённого узла |
| `-token-file` | Путь токена, по умолчанию `agent.token` |
| `-state` | Постоянное состояние, по умолчанию `agent-state` |
| `-interval` | Интервал сбора от `500ms` до `5s`, по умолчанию `500ms` |
| `-capture` | Интерфейс TShark, пустой — захват выключен |
| `-check` | Локальная диагностика без токена и подключения |

## Доступность данных

GPU зависит от ОС, драйвера и `nvidia-smi`; часть Windows-показателей доступна через CIM/GPU Engine. Intel/AMD не имеют гарантированной полной телеметрии. Отсутствующие показатели обозначаются как недоступные, а не нулевые.

Соединения и общие скорости работают без дополнительных программ. Для метаданных пакетов установите TShark отдельно; Windows требует Npcap и права захвата. Список интерфейсов: `tshark -D`; добавьте `-capture 1` или имя. Агент не устанавливает драйверы. Содержимое пакетов не сохраняется; привязка пакета к PID и скорость отдельного соединения не вычисляются.

Права ОС определяют видимость процессов, соединений и журналов. Командные строки процессов и системные журналы могут содержать чувствительные сведения — учитывайте это при предоставлении доступа к панели.

## Модули и совместимость

Используйте Pulse Monitor `v0.4.0` с агентом `v0.4.0` для первого публичного выпуска. Контракт отчётов описан как v1; отчёт не содержит поля согласования версии. Модули явно используют `protocol: 1`. До v1.0 проверяйте changelog перед обновлением.

Модуль запускает произвольный нативный код с правами агента, без песочницы. `permissions` — описание, а не ограничение ОС. Устанавливайте только доверенные пакеты через панель. [Формат пакета и процесса](https://github.com/HubbaBubbaPrepod/pulse-monitor/blob/main/docs/MODULES.md).

Docker-сборщик выпускается отдельно: [Pulse Module Docker](https://github.com/HubbaBubbaPrepod/pulse-module-docker). Скачайте `docker-module.json` v1.0.1 из его Releases и загрузите в настройки модулей панели. Пакет использует протокол v1 и содержит сборки для Linux amd64/arm64 и Windows amd64. Сборщик делает запросы чтения, но Docker socket даёт привилегированный доступ к хосту; не считайте его безопасным только из-за характера запросов.

## Обновление, удаление и диагностика

Остановите службу, замените бинарник и запустите; сохраните токен и каталог состояния. В состоянии хранятся `pending.json` (один ожидающий отправки отчёт), `log-cursor` и скачанные модули. При недоступной панели агент повторяет один отчёт с задержкой до 30 секунд; это не неограниченная очередь offline-метрик. После смены токена обновите файл и перезапустите.

Linux: `sudo systemctl stop pulse-agent`, после замены — `sudo systemctl start pulse-agent`. Для удаления `sudo systemctl disable --now pulse-agent`, удалите unit и каталог программы, выполните `sudo systemctl daemon-reload`. Windows: `Stop-Service PulseAgent`, после замены — `Start-Service PulseAgent`; для удаления `sc.exe delete PulseAgent` после остановки. Состояние и токен удаляйте отдельно только если больше не нужны.

При проблемах выполните `pulse-agent -check`, проверьте права, ID, token-file, URL, TLS и исходящий доступ. HTTP 401 означает неверный/отозванный токен. После 15 секунд без отчётов панель отмечает узел offline. Недоступные показатели и `unsupported` модулей проверяйте по архитектуре и возможностям ОС.

## Разработка и лицензия

Go 1.23+: `go build .`, `go test ./...`, `go vet ./...`. Платформенные архивы: PowerShell 7.4+ и `./scripts/build.ps1 -Version v0.4.0`; Go должен быть в PATH или передан через `-GoExe`. Подробнее: [CONTRIBUTING.md](CONTRIBUTING.md).

[Issues](https://github.com/HubbaBubbaPrepod/pulse-agent/issues) · [Changelog](CHANGELOG.md) · [MIT](LICENSE). Зависимости сохраняют собственные лицензии.
