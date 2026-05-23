#!/bin/sh
set -eu

PHONE_HOST="${PHONE_HOST:?env var PHONE_HOST is required (phone WiFi IP)}"
PHONE_PORT="${PHONE_PORT:-5555}"
RECONNECT_INTERVAL="${RECONNECT_INTERVAL:-15}"

log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; }

log "Starting ADB server..."
adb start-server

log "Connecting to ${PHONE_HOST}:${PHONE_PORT}..."
adb connect "${PHONE_HOST}:${PHONE_PORT}" || true

# Keep the container alive; reconnect when the device goes offline.
while true; do
    sleep "${RECONNECT_INTERVAL}"
    state=$(adb -s "${PHONE_HOST}:${PHONE_PORT}" get-state 2>/dev/null || echo "offline")
    if [ "$state" != "device" ]; then
        log "Device offline — reconnecting to ${PHONE_HOST}:${PHONE_PORT}..."
        adb connect "${PHONE_HOST}:${PHONE_PORT}" 2>&1 || true
    fi
done
