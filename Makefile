APP_NAME=blueberry
BIN_DIR=bin
BIN=$(BIN_DIR)/$(APP_NAME)

.PHONY: build deps install run start stop logs

build:
	mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BIN) .

deps:
	sudo yum -y update || true
	sudo yum -y install python3-pip ffmpeg || true
	sudo pip3 install --upgrade yt-dlp

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


