APP_NAME=blueberry
BIN_DIR=bin
BIN=$(BIN_DIR)/$(APP_NAME)
GO_VERSION?=1.24.0
GO_BIN?=$(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

.PHONY: build deps install run start stop logs

build:
	mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO_BIN) build -o $(BIN) .

deps:
	# 基础包管理器（Amazon Linux 2 用 yum，AL2023 用 dnf）
	if command -v dnf >/dev/null 2>&1; then PKG=dnf; else PKG=yum; fi; \
	sudo $$PKG -y update || true; \
	# 按需安装 pip3
	if ! command -v pip3 >/dev/null 2>&1; then sudo $$PKG -y install python3-pip || true; else echo "pip3 already installed"; fi; \
	# 按需安装基础工具
	for pkg in git tar xz curl; do \
	if ! command -v $$pkg >/dev/null 2>&1; then sudo $$PKG -y install $$pkg || true; else echo "$$pkg already installed"; fi; \
	done
	# 安装 Go 指定版本（$(GO_VERSION)）
	if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "go$(GO_VERSION)"; then \
	  echo "Installing Go $(GO_VERSION) ..."; \
	  sudo rm -rf /usr/local/go; \
	  curl -fsSL -o /tmp/go.tar.gz https://go.dev/dl/go$(GO_VERSION).linux-amd64.tar.gz || \
	    curl -fsSL -o /tmp/go.tar.gz https://dl.google.com/go/go$(GO_VERSION).linux-amd64.tar.gz; \
	  sudo tar -C /usr/local -xzf /tmp/go.tar.gz; \
	  sudo ln -sf /usr/local/go/bin/go /usr/local/bin/go; \
	  sudo ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt; \
	else echo "Go $(GO_VERSION) already installed"; fi
	# 安装/升级 yt-dlp
	if ! command -v yt-dlp >/dev/null 2>&1; then sudo pip3 install --upgrade yt-dlp; else echo "yt-dlp already installed"; fi
	# 安装 ffmpeg/ffprobe（静态版）
	if ! command -v ffmpeg >/dev/null 2>&1 || ! command -v ffprobe >/dev/null 2>&1; then \
	  sudo mkdir -p /usr/local/bin; \
	  cd /usr/local/bin && \
	    curl -L -o ffmpeg.tar.xz https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz && \
	    tar -xf ffmpeg.tar.xz && \
	    cd ffmpeg-*-static && \
	    sudo cp ffmpeg ffprobe /usr/local/bin/ && \
	    sudo chmod +x /usr/local/bin/ffmpeg /usr/local/bin/ffprobe && \
	    cd .. && rm -f ffmpeg.tar.xz; \
	else echo "ffmpeg/ffprobe already installed"; fi

install: build
	sudo mkdir -p /usr/local/$(APP_NAME)
	sudo cp $(BIN) /usr/local/$(APP_NAME)/$(APP_NAME)
	# 可选：复制配置文件
	@if [ -f config.yaml ]; then sudo cp config.yaml /usr/local/$(APP_NAME)/config.yaml; fi
	sudo mkdir -p /var/log/$(APP_NAME)

# 后台运行（使用配置文件中的 logging 路径，或用 nohup 重定向）
start:
	@if [ -f /usr/local/$(APP_NAME)/config.yaml ]; then \
		cd /usr/local/$(APP_NAME) && nohup ./$(APP_NAME) sync --all >/var/log/$(APP_NAME)/out.log 2>/var/log/$(APP_NAME)/err.log & echo $$! | sudo tee /var/run/$(APP_NAME).pid; \
	else \
		nohup $(BIN) sync --all >/var/log/$(APP_NAME)/out.log 2>/var/log/$(APP_NAME)/err.log & echo $$! | sudo tee /var/run/$(APP_NAME).pid; \
	fi

stop:
	@if [ -f /var/run/$(APP_NAME).pid ]; then \
		kill `cat /var/run/$(APP_NAME).pid` || true; \
		sudo rm -f /var/run/$(APP_NAME).pid; \
	else echo "No PID file"; fi

logs:
	tail -n 200 -f /var/log/$(APP_NAME)/out.log /var/log/$(APP_NAME)/err.log


