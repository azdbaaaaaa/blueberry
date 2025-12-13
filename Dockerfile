# ---- Build stage: compile Go binary ----
FROM golang:1.24 AS builder
WORKDIR /app

# Pre-copy mod files for better layer caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/blueberry .

# ---- Runtime stage: tools + runtime deps ----
FROM python:3.12-slim

ENV APP_USER=worker \
    APP_DIR=/home/worker/blueberry

# Install system deps, ffmpeg, and Node.js (for yt-dlp JS runtime)
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git ffmpeg \
    && rm -rf /var/lib/apt/lists/*
# Node.js 20.x
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get update && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# yt-dlp via pip
RUN pip install --no-cache-dir --upgrade pip \
    && pip install --no-cache-dir yt-dlp

# User and folders
RUN useradd -m -d /home/${APP_USER} ${APP_USER} \
    && mkdir -p ${APP_DIR}/downloads ${APP_DIR}/cookies /var/log/blueberry \
    && chown -R ${APP_USER}:${APP_USER} /home/${APP_USER} /var/log/blueberry

# Copy compiled binary
COPY --from=builder /out/blueberry /usr/local/bin/blueberry

WORKDIR ${APP_DIR}
USER ${APP_USER}

# Mount points for data and config
VOLUME ["${APP_DIR}/downloads", "${APP_DIR}/cookies"]

# Default entrypoint (can be overridden)
ENTRYPOINT ["blueberry"]
CMD ["--help"]


