#!/bin/sh
# Reads /data/options.json (written by HA Supervisor) and produces /data/config.yaml for abb.
set -e

OPTIONS=/data/options.json
OUT=/data/config.yaml

mkdir -p /data/.android
chmod 700 /data/.android

opt() {
    val=$(jq -r "$1 // empty" "$OPTIONS" 2>/dev/null)
    if [ -z "$val" ]; then echo "$2"; else echo "$val"; fi
}

opt_bool() {
    val=$(jq -r "$1" "$OPTIONS" 2>/dev/null)
    case "$val" in true|True|1) echo "true" ;; *) echo "false" ;; esac
}

opt_int() {
    val=$(jq -r "$1 // empty" "$OPTIONS" 2>/dev/null)
    if [ -z "$val" ] || [ "$val" = "null" ]; then echo "$2"; else echo "$val"; fi
}

channels_yaml() {
    jq -r '.channels[]? | "  - " + .' "$OPTIONS" 2>/dev/null || echo "  - telegram"
}

cat > "$OUT" <<EOF
adb:
  server_host: "localhost"
  server_port: 5037
  poll_interval: "$(opt .adb_poll_interval "30s")"
  watch_interval: "$(opt .adb_watch_interval "5s")"
  device_serial: "$(opt .adb_device_serial "")"

db:
  path: "/data/abb.db"

telegram:
  token: "$(opt .telegram_token "")"
  chat_id: $(opt_int .telegram_chat_id 0)
  proxy:
    enabled: $(opt_bool .telegram_proxy_enabled)
    address: "$(opt .telegram_proxy_address "")"
    username: "$(opt .telegram_proxy_username "")"
    password: "$(opt .telegram_proxy_password "")"

email:
  enabled: $(opt_bool .email_enabled)
  host: "$(opt .email_host "smtp.gmail.com")"
  port: $(opt_int .email_port 587)
  username: "$(opt .email_username "")"
  password: "$(opt .email_password "")"
  from: "$(opt .email_from "")"
  to: "$(opt .email_to "")"

ntfy:
  enabled: $(opt_bool .ntfy_enabled)
  server_url: "$(opt .ntfy_server_url "https://ntfy.sh")"
  topic: "$(opt .ntfy_topic "sms")"
  token: "$(opt .ntfy_token "")"
  priority: "$(opt .ntfy_priority "default")"

metrics:
  enabled: $(opt_bool .metrics_enabled)
  addr: ":9090"

log:
  file: ""
  max_size_mb: 100
  max_age_days: 7
  compress: true

dispatcher:
  max_attempts: $(opt_int .dispatcher_max_attempts 5)
  retry_base_interval: "$(opt .dispatcher_retry_base_interval "1m")"
  dispatch_interval: "$(opt .dispatcher_dispatch_interval "10s")"

translate:
  enabled: $(opt_bool .translate_enabled)
  api_key: "$(opt .translate_api_key "")"
  folder_id: "$(opt .translate_folder_id "")"
  target_lang: "$(opt .translate_target_lang "ru")"

channels:
$(channels_yaml)
EOF

echo "[generate-config] /data/config.yaml written"
