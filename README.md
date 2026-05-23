# abb — Android Bridge Bot

Reads SMS from an Android phone connected via USB and forwards them to Telegram, Ntfy, and/or Email. All messages are buffered in a local SQLite database with automatic retry on failure.

---

## Features

- Reads inbox SMS via ADB (`content://sms/inbox`) — no root required
- Saves every message to SQLite before sending (no data loss on crash)
- Delivers to **Telegram**, **Ntfy**, and/or **Email** (any combination)
- Optional SOCKS5 proxy for Telegram
- Exponential backoff retry (configurable attempts and base interval)
- Wraps OTP digit sequences (3+ digits) in backticks for better readability in Telegram
- Prometheus metrics endpoint (`/metrics`)
- Log rotation via lumberjack; or use journald under systemd
- Graceful shutdown on SIGTERM / Ctrl-C

---

## Requirements

| Tool | Version | Notes |
|---|---|---|
| Go | 1.25+ | `modernc.org/sqlite` requires 1.25 |
| ADB server | any | from [platform-tools](https://developer.android.com/tools/releases/platform-tools); must be running before `abb` starts |
| Android | any | USB debugging must be enabled |
| systemd | optional | for autostart on Linux |

`abb` communicates with Android via the **ADB server socket** (`localhost:5037`) using the [gadb](https://github.com/electricbubble/gadb) library — it does **not** call the `adb` binary directly. The ADB server must already be running when `abb` starts.

Start the ADB server once (it stays running in the background):

```bash
adb start-server   # or just: adb devices
```

---

## Phone setup

1. **Enable Developer Options**: Settings → About phone → tap *Build number* 7 times.
2. **Enable USB Debugging**: Settings → Developer options → USB debugging → ON.
3. Connect the phone via USB. On first connect, approve the RSA fingerprint prompt on the phone screen.
4. Verify: `adb devices` should show the serial with status `device` (not `unauthorized`).

> **Troubleshooting "unauthorized"**: the RSA prompt may have auto-dismissed. Run `adb kill-server && adb devices` and re-approve on the phone screen.

---

## Build

```bash
git clone <repo>
cd abb_app
go build -o abb ./cmd/abb
```

Cross-compile for Linux ARM64 (e.g. Raspberry Pi):

```bash
GOOS=linux GOARCH=arm64 go build -o abb-arm64 ./cmd/abb
```

---

## Configuration

Edit `config.yaml` (all fields have sensible defaults):

```yaml
adb:
  server_host: "localhost"  # ADB server host (default: localhost)
  server_port: 5037         # ADB server port (default: 5037)
  poll_interval: 30s        # how often to poll SMS inbox
  watch_interval: 5s        # how often to probe for device connect/disconnect
  device_serial: ""         # empty = use first available device

db:
  path: "./abb.db"

telegram:
  token: "BOT_TOKEN"        # @BotFather token
  chat_id: 0                # numeric chat / user ID
  proxy:
    enabled: false
    address: "host:port"    # SOCKS5 proxy
    username: ""
    password: ""

email:
  enabled: false
  host: "smtp.gmail.com"
  port: 587
  username: ""
  password: ""
  from: "bot@example.com"
  to: "you@example.com"

ntfy:
  enabled: false
  server_url: "https://ntfy.example.com"  # URL вашего сервера
  topic: "sms"                             # уникальное имя топика
  token: ""                                # Bearer-токен (если включена auth)
  priority: "default"                      # low | default | high | urgent | max

dispatcher:
  max_attempts: 5           # give up after this many failures
  retry_base_interval: 1m  # backoff: 1m, 2m, 4m, 8m, 16m …
  dispatch_interval: 10s   # periodic sweep of pending outbox

metrics:
  enabled: false
  addr: ":9090"             # Prometheus scrape target: GET /metrics

log:
  file: ""                  # empty = stderr; set a path to write to a file
  max_size_mb: 100          # rotate after this many MB
  max_age_days: 7           # delete rotated logs older than N days
  compress: true            # gzip rotated files

channels:
  - telegram
  # - ntfy
  # - email
```

**Never commit your bot token or proxy credentials.** Use a separate `config.local.yaml` that is gitignored.

---

## Running

```bash
./abb config.yaml
```

```
2026-05-13T12:00:00.000+0300  INFO  waiting for device...  {"adb_server": "localhost:5037"}
2026-05-13T12:00:05.000+0300  INFO  starting SMS poller  {"serial": "emulator-5554", "last_android_id": 42}
```

---

## Running as a systemd service

```bash
# 1. Build and install
go build -o /opt/abb/abb ./cmd/abb
cp config.yaml /opt/abb/config.yaml   # fill in token and chat_id

# 2. Create a dedicated user
useradd --system --no-create-home --shell /usr/sbin/nologin abb
chown -R abb:abb /opt/abb

# 3. Allow the abb user to access USB devices via ADB
usermod -aG plugdev abb

# 4. Ensure the ADB server starts before the service
# Add this to /etc/rc.local or a separate adb-server.service, OR add to the unit file:
# ExecStartPre=/usr/bin/adb start-server

# 4. Install and start the service
cp deploy/abb.service /etc/systemd/system/abb.service
systemctl daemon-reload
systemctl enable --now abb

# 5. Check status
systemctl status abb
journalctl -u abb -f
```

To stop gracefully (flushes the current poll, closes the database):

```bash
systemctl stop abb
```

---

## Prometheus metrics

Enable in `config.yaml`:

```yaml
metrics:
  enabled: true
  addr: ":9090"
```

Scrape target: `http://<host>:9090/metrics`

| Metric | Type | Description |
|---|---|---|
| `abb_sms_received_total` | Counter | New SMS saved to DB |
| `abb_sms_sent_total{channel}` | Counter | Successful deliveries per channel |
| `abb_sms_failed_total{channel}` | Counter | Failed delivery attempts per channel |

Add to `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: abb
    static_configs:
      - targets: ["localhost:9090"]
```

---

## Ntfy setup (self-hosted, Docker + VPS)

[Ntfy](https://ntfy.sh) is a simple pub/sub notification service. `abb` POSTs each SMS to your Ntfy server; the Ntfy app on your phone subscribes to the topic and shows a push notification.

### 1. Deploy Ntfy on your VPS

```bash
# Create a directory for data and config
mkdir -p /opt/ntfy

# docker-compose.yml
cat > /opt/ntfy/docker-compose.yml <<'EOF'
services:
  ntfy:
    image: binwiederhier/ntfy
    container_name: ntfy
    command: serve
    restart: unless-stopped
    ports:
      - "80:80"
    volumes:
      - /opt/ntfy/data:/var/lib/ntfy
      - /opt/ntfy/server.yml:/etc/ntfy/server.yml:ro
    environment:
      TZ: Europe/Moscow
EOF

# Minimal config
cat > /opt/ntfy/server.yml <<'EOF'
base-url: "https://ntfy.example.com"   # your domain / VPS IP
listen-http: ":80"
cache-file: "/var/lib/ntfy/cache.db"
auth-file: "/var/lib/ntfy/users.db"
auth-default-access: "deny-all"         # require authentication
EOF

docker compose -f /opt/ntfy/docker-compose.yml up -d
```

> If you have a domain, put nginx/Caddy in front with TLS. For a quick start with a bare IP, use `http://` and skip TLS.

### 2. Create a user and topic access

```bash
# Create a user for abb (publisher)
docker exec -it ntfy ntfy user add --role=user abb_publisher

# Grant publish access to the topic
docker exec -it ntfy ntfy access abb_publisher sms write

# Create a read-only user for the phone app (optional but recommended)
docker exec -it ntfy ntfy user add --role=user phone_reader
docker exec -it ntfy ntfy access phone_reader sms read
```

Generate a token (instead of password in Bearer header):

```bash
docker exec -it ntfy ntfy token add abb_publisher
# → tk_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

### 3. Configure abb

```yaml
ntfy:
  enabled: true
  server_url: "https://ntfy.example.com"
  topic: "sms"           # must match the topic you granted access to
  token: "tk_xxxxx..."   # token from step 2
  priority: "high"       # SMS are important — use high or urgent

channels:
  - ntfy
  - telegram             # you can send to multiple channels simultaneously
```

### 4. Install Ntfy app on your phone

- [Android (F-Droid / Play Store)](https://ntfy.sh/#subscribe)
- iOS App Store: search **ntfy**

Add a subscription:
1. Open the app → **+** → **Custom server**
2. Server URL: `https://ntfy.example.com`
3. Topic: `sms`
4. Enter username `phone_reader` and password (or use Access Token)

### Ntfy priorities

| Value | When to use |
|---|---|
| `low` | Background info, not time-sensitive |
| `default` | Normal notifications |
| `high` | SMS — recommended |
| `urgent` | OTP codes, banking alerts |
| `max` | Breaks through Do Not Disturb on Android |

---

## Docker и Kubernetes (ADB по WiFi)

### Структура образов

| Образ | Файл | Назначение |
|---|---|---|
| `abb` | `Dockerfile` | само приложение (Go, pure-CGO-free) |
| `abb-adb` | `deploy/adb/Dockerfile` | ADB-сервер + авто-переподключение к телефону |

В Kubernetes оба контейнера запускаются в одном **Pod** и разделяют сетевой namespace → `abb` видит ADB-сервер на `localhost:5037`.

---

### 1. Подготовка телефона (один раз)

**Android 11+**
```bash
# На телефоне: Настройки → Для разработчиков → Беспроводная отладка → Сопряжение по коду
adb pair <ip>:<pair-port>   # ввести код с экрана
adb connect <ip>:5555
adb devices                  # должен показать <ip>:5555  device
```

**Android ≤ 10** (USB нужен один раз для переключения в TCP-режим)
```bash
adb tcpip 5555
# отключить USB
adb connect <ip телефона>:5555
```

После этого USB больше не нужен. Телефон должен быть в той же сети, что и кластер.

---

### 2. Сборка образов

```bash
# из корня репозитория

# образ приложения
docker build -t your-registry/abb:latest .

# образ ADB-сайдкара
docker build -t your-registry/abb-adb:latest deploy/adb/

# запушить в registry
docker push your-registry/abb:latest
docker push your-registry/abb-adb:latest
```

---

### 3. Настройка конфига

Откройте `deploy/k8s/secret.yaml` и впишите реальные значения:

```yaml
stringData:
  config.yaml: |
    telegram:
      token: "1234567890:AAH..."   # токен от @BotFather
      chat_id: 123456789           # ваш числовой chat_id
```

И IP телефона в `deploy/k8s/deployment.yaml`:
```yaml
env:
  - name: PHONE_HOST
    value: "192.168.1.42"          # статический IP телефона
```

> **Совет:** назначьте телефону статический IP в роутере (DHCP-резервация по MAC), иначе IP может меняться.

---

### 4. Деплой в Kubernetes

```bash
# PVC для базы данных
kubectl apply -f deploy/k8s/pvc.yaml

# Secret с config.yaml (содержит токен — не коммитьте в git!)
kubectl apply -f deploy/k8s/secret.yaml

# Deployment
kubectl apply -f deploy/k8s/deployment.yaml

# Проверка
kubectl get pods -l app=abb
kubectl logs -l app=abb -c adb-server --follow   # лог ADB-сайдкара
kubectl logs -l app=abb -c abb --follow           # лог приложения
```

Ожидаемый вывод в логах `adb-server`:
```
[2026-05-23T10:00:01Z] Starting ADB server...
[2026-05-23T10:00:02Z] Connecting to 192.168.1.42:5555...
connected to 192.168.1.42:5555
```

---

### 5. Обновление приложения

```bash
docker build -t your-registry/abb:v2 . && docker push your-registry/abb:v2
kubectl set image deployment/abb abb=your-registry/abb:v2
# Deployment использует strategy: Recreate — старый Pod остановится до запуска нового
```

---

### Важные ограничения

| Ограничение | Причина |
|---|---|
| `replicas: 1` | SQLite не поддерживает параллельную запись из нескольких процессов |
| `strategy: Recreate` | Исключает ситуацию двух одновременно работающих Pod во время обновления |
| Телефон в одной сети с кластером | ADB-сервер сам подключается к телефону (не наоборот) |
| Статический IP телефона | Если IP меняется, сайдкар не найдёт телефон до перезапуска Pod |

---

## Adding a new delivery channel

1. Implement `sender.Sender`:

```go
type Sender interface {
    Name() string
    Send(ctx context.Context, msg model.Message) error
}
```

2. Register in `buildSenders()` in `cmd/abb/main.go`.
3. Add the channel name to `channels:` in `config.yaml`.

The dispatcher, database schema, retry logic, and metrics are all channel-agnostic.

---

## Project structure

```
abb_app/
├── cmd/abb/main.go          # entry point, wire-up
├── internal/
│   ├── adb/                 # USB watcher + SMS poller + ADB output parser
│   ├── db/                  # SQLite schema, messages, outbox, delivery_log CRUD
│   ├── dispatcher/          # fan-out delivery + exponential backoff retry worker
│   ├── metrics/             # Prometheus counters + HTTP server
│   ├── model/               # shared types: Message, OutboxItem, RetryItem
│   └── sender/              # Sender interface, TelegramSender, NtfySender, EmailSender
├── config/config.go         # viper config loader + all config structs
├── deploy/abb.service       # systemd unit file
├── config.yaml              # runtime configuration
└── ARCHITECTURE.md          # design document and test-case catalogue
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `connect to ADB server: connection refused` | ADB server not running | Run `adb start-server` before starting `abb` |
| `device not found in list` | Phone disconnected or wrong serial | Check USB cable; run `adb devices` to verify |
| `telegram getMe: unauthorized` | Wrong bot token | Re-check token from @BotFather |
| `socks5 dialer: connection refused` | Proxy not reachable | Disable proxy or fix address in config |
| SMS read but not sent | `channels` list empty or token is placeholder | Check `config.yaml`, set a real token |
| `permission denied: content://sms` | USB debugging not enabled or prompt not approved | Re-approve RSA fingerprint on the phone |
| High memory / large log file | Log rotation not configured | Set `log.file` and tune `max_size_mb` / `max_age_days` |
| `ntfy: server returned 401` | Wrong token or user has no publish access | Re-check token; run `ntfy access abb_publisher sms write` |
| `ntfy: server returned 403` | Topic access denied | Check `auth-default-access` in `server.yml` and user permissions |
| No Ntfy push on phone | Wrong topic name or server URL | Verify topic matches in both `config.yaml` and the Ntfy app subscription |
