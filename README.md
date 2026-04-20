# scadt

Desktop-тулка для девопсов на чистом Go: health-мониторинг серверов,
live-поток ошибок, debug-запросы, SSH runner, алерты в Slack/Telegram.

Нативное окно (Fyne), один .exe ~47 MB. Без Electron, webview, HTML.

## Фичи

- Health-check серверов каждые N сек (HTTP или TCP), пинг в ms
- Драйверы для live-ивентов: `http_poll` (JSON + cursor), `tail_file`, `mock`, `none`
- Persistence в `data/events.jsonl` с ротацией по 50 MiB + retention в днях
- Группировка похожих ошибок по fingerprint (нормализация чисел/IP/UUID)
- Alert rules: regex + threshold в окне + cooldown → webhook (Slack/Telegram/custom)
- SSH runner с key-file auth и host-key pinning (SHA256 FP)
- Debug-запросы на активный сервер (method + path + headers, auth из конфига)
- Ctrl+K command palette
- Headless-режим с HTTP API (`-headless`)

## Требования

### Для запуска
Windows 10/11 x64. Никаких внешних зависимостей.

### Для сборки
- Go 1.22+
- gcc mingw-w64 (нужен Fyne для OpenGL/DirectX биндингов)

Поставить mingw:
```powershell
winget install --id BrechtSanders.WinLibs.POSIX.UCRT.LLVM
```

## Сборка

```powershell
$env:PATH = "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT.LLVM_Microsoft.Winget.Source_8wekyb3d8bbwe\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
go build -ldflags="-s -w -H=windowsgui" -o scadt.exe .
```

Из Linux/macOS:
```bash
CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
GOOS=windows GOARCH=amd64 \
go build -ldflags="-s -w -H=windowsgui" -o scadt.exe .
```

## Запуск

Клик по `scadt.exe`. При первом запуске создаёт рядом `scadt.json` и папку `data/`.

Флаги:
```
-config PATH     путь к конфигу (default scadt.json)
-headless        без GUI, только HTTP API
-addr HOST:PORT  listen address для headless
```

## Конфиг

`scadt.json` редактируется через Settings в UI либо руками (atomic rename).

```jsonc
{
  "listen": "127.0.0.1:0",
  "data_dir": "data",
  "retention_days": 7,
  "servers": [
    {
      "name": "prod-api",
      "url":  "https://api.example.com",
      "kind": "production",
      "auth":   { "type": "bearer", "token": "eyJ..." },
      "driver": { "type": "http_poll", "url": "https://api.example.com/internal/errors", "every": "5s" },
      "health": { "type": "http", "path": "/healthz", "every": "10s" },
      "ssh": {
        "host": "1.2.3.4", "port": 22, "user": "deploy",
        "key_file": "C:\\Users\\me\\.ssh\\id_ed25519",
        "host_key_fp": "SHA256:AAAAC3Nza..."
      }
    }
  ],
  "alert_rules": [
    { "id": "burst", "name": "Error burst", "enabled": true,
      "level": "error", "threshold": 10, "window": "1m", "cooldown": "5m",
      "channels": ["slack"] }
  ],
  "alert_channels": [
    { "name": "slack", "type": "slack", "url": "https://hooks.slack.com/services/..." }
  ]
}
```

## Драйверы ивентов

| type | параметры | что делает |
|------|-----------|-----------|
| `http_poll` | `url`, `every` | GET на JSON endpoint, поддерживает cursor pagination |
| `tail_file` | `path` | tail локального `.jsonl` |
| `mock` | — | генератор демо-ошибок |
| `none` | — | только health-check |

### Формат `http_poll` ответа

```json
{
  "events": [
    { "ts": "2026-04-21T10:00:00Z", "level": "error",
      "service": "auth-gateway",
      "message": "JWT signature verification failed",
      "trace": "...", "labels": { "req_id": "abc" } }
  ],
  "cursor": "opaque-string"
}
```

Если есть `cursor` — передаётся в следующий запрос как `?cursor=...`.
Иначе дедуп по `(service, message, ts)` + `?since=<RFC3339>`.

### Формат `tail_file`

Каждая строка — JSON того же формата без обёртки `events`.

## Алерт-каналы

### Slack
```json
{ "type": "slack", "url": "https://hooks.slack.com/services/..." }
```

### Telegram
```json
{ "type": "telegram", "token": "BOT_TOKEN", "chat_id": "-1001234" }
```

### Generic webhook
```json
{ "type": "webhook", "url": "https://endpoint", "headers": {"X-Auth": "..."} }
```
POST body: `{"rule":{...},"count":N,"event":{...},"title":"..."}`

## SSH

Auth: `key_file` (+ `key_pass`) либо `password` как fallback.

Host-key pinning через `host_key_fp` (SHA256:BASE64). Получить:
```bash
ssh-keyscan example.com 2>/dev/null | ssh-keygen -lf - -E sha256
```

## HTTP API (headless)

| метод | путь |
|-------|------|
| GET   | `/api/servers` |
| POST  | `/api/servers/update` |
| GET   | `/api/history?limit=200` |
| GET   | `/api/events/search?server=&service=&level=&regex=&limit=` |
| GET   | `/api/events` (SSE) |
| POST  | `/api/debug` |
| GET   | `/api/stats` |
| GET/POST | `/api/saved_requests` |
| GET/POST | `/api/alert_rules` |
| GET/POST | `/api/alert_channels` |
| GET   | `/api/alert_history` |
| POST  | `/api/runner/exec` |
| GET   | `/api/config` (secrets redacted) |

## Структура

```
scadt/
├── main.go              store, hub, health, drivers, alerts, HTTP API
├── gui.go               Fyne UI
├── internal/
│   ├── models/          доменные типы
│   ├── config/          atomic load/save scadt.json
│   └── runner/          SSH exec (x/crypto/ssh)
└── README.md
```

## Переменные окружения

```
FYNE_SCALE       UI scale factor (default 1.1)
SCADT_WINDOW     размер окна "WxH" (default 1600x1000)
```

## Troubleshooting

**Build падает на undefined OpenGL**: не добавлен mingw в PATH. `gcc --version` должен работать.

**Окно не открывается**: пересобрать без `-H=windowsgui`, запустить из консоли — увидишь ошибки.

**События не идут**: серверов нет или у всех `driver.type = none`. Или endpoint не отдаёт правильный JSON (см. выше).

**SSH no auth methods**: заполни `ssh.key_file` или `ssh.password`.
