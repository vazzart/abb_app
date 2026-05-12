# abb — Android Bridge Bot

Reads SMS from an Android phone connected via USB and forwards them to Telegram (and optionally Email). All messages are buffered in a local SQLite database with automatic retry on failure.

---

## Features

- Reads inbox SMS via ADB (`content://sms/inbox`) — no root required
- Saves every message to SQLite before sending (no data loss on crash)
- Delivers to Telegram; optional Email as a second channel
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
│   └── sender/              # Sender interface, TelegramSender, EmailSender
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
