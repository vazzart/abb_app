ARG BUILD_FROM=ghcr.io/home-assistant/aarch64-base-debian:bookworm

# Stage 1: cross-compile Go binary
FROM golang:1.25-bookworm AS builder
ARG TARGETARCH=arm64
WORKDIR /build
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o /abb ./cmd/abb

# Stage 2: HA runtime
FROM ${BUILD_FROM}
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        android-tools-adb \
        jq \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /abb /usr/local/bin/abb
COPY rootfs/ /

RUN chmod a+x \
    /etc/s6-overlay/scripts/generate-config.sh \
    /etc/s6-overlay/s6-rc.d/generate-config/up \
    /etc/s6-overlay/s6-rc.d/adb-server/run \
    /etc/s6-overlay/s6-rc.d/adb-server/finish \
    /etc/s6-overlay/s6-rc.d/abb/run \
    /etc/s6-overlay/s6-rc.d/abb/finish
