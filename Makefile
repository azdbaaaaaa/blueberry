APP_NAME=blueberry
BIN_DIR=bin
BIN=$(BIN_DIR)/$(APP_NAME)
GO_VERSION?=1.24.0
GO_BIN?=$(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
PY_VERSION?=3.12.11
PYENV_ROOT?=/usr/local/pyenv
COOKIES_DIR?=cookies
REMOTE_USER?=worker
REMOTE_HOST?=18.140.235.125
REMOTE_PATH?=/home/worker/blueberry/cookies
REMOTE_APP_DIR?=/home/worker/blueberry
CONFIG_FILE?=config.yaml
# Push command variables
REMOTE_PUSH_USER?=root
REMOTE_PUSH_HOST?=66.42.63.131
REMOTE_PUSH_DIR?=/opt/blueberry
SCRIPTS_DIR?=scripts

.PHONY: build install run start stop logs sync-cookies sync-config push ssh-keygen ssh-add-key

build:
	mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO_BIN) build -o $(BIN) .

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

sync-cookies:
	@test -d $(COOKIES_DIR) || (echo "Local cookies dir '$(COOKIES_DIR)' not found" && exit 1)
	ssh $(REMOTE_USER)@$(REMOTE_HOST) "mkdir -p $(REMOTE_PATH)"
	rsync -azP -e "ssh" --delete $(COOKIES_DIR)/ $(REMOTE_USER)@$(REMOTE_HOST):$(REMOTE_PATH)/

sync-config:
	@test -f $(CONFIG_FILE) || (echo "Config file '$(CONFIG_FILE)' not found" && exit 1)
	ssh $(REMOTE_USER)@$(REMOTE_HOST) "mkdir -p $(REMOTE_APP_DIR)"
	rsync -azP -e "ssh" $(CONFIG_FILE) $(REMOTE_USER)@$(REMOTE_HOST):$(REMOTE_APP_DIR)/config.yaml

push: build
	@echo "=========================================="
	@echo "Pushing files to $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST):$(REMOTE_PUSH_DIR)"
	@echo "=========================================="
	@test -f $(BIN) || (echo "Error: Binary file '$(BIN)' not found. Run 'make build' first." && exit 1)
	@test -d $(SCRIPTS_DIR) || (echo "Error: Scripts directory '$(SCRIPTS_DIR)' not found" && exit 1)
	@SSH_OPTS=""; \
	if [ -f $(SSH_KEY_PATH) ]; then \
		SSH_OPTS="-i $(SSH_KEY_PATH)"; \
		echo "Using SSH key: $(SSH_KEY_PATH)"; \
	fi; \
	echo "Creating remote directory..."; \
	ssh $$SSH_OPTS $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST) "mkdir -p $(REMOTE_PUSH_DIR) && chmod 755 $(REMOTE_PUSH_DIR)"; \
	echo "Pushing binary executable: $(BIN) -> $(REMOTE_PUSH_DIR)/$(APP_NAME)"; \
	rsync -azP -e "ssh $$SSH_OPTS" $(BIN) $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST):$(REMOTE_PUSH_DIR)/$(APP_NAME); \
	echo "Pushing scripts directory: $(SCRIPTS_DIR)/ -> $(REMOTE_PUSH_DIR)/$(SCRIPTS_DIR)/"; \
	rsync -azP -e "ssh $$SSH_OPTS" $(SCRIPTS_DIR)/ $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST):$(REMOTE_PUSH_DIR)/$(SCRIPTS_DIR)/; \
	if [ -d $(COOKIES_DIR) ]; then \
		echo "Pushing cookies directory: $(COOKIES_DIR)/ -> $(REMOTE_PUSH_DIR)/$(COOKIES_DIR)/"; \
		rsync -azP -e "ssh $$SSH_OPTS" $(COOKIES_DIR)/ $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST):$(REMOTE_PUSH_DIR)/$(COOKIES_DIR)/; \
	else \
		echo "Warning: Cookies directory '$(COOKIES_DIR)' not found, skipping..."; \
	fi; \
	echo "Creating downloads and output directories..."; \
	ssh $$SSH_OPTS $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST) "mkdir -p $(REMOTE_PUSH_DIR)/downloads $(REMOTE_PUSH_DIR)/output && chmod 755 $(REMOTE_PUSH_DIR)/downloads $(REMOTE_PUSH_DIR)/output"; \
	CONFIG_NAME="config-$(REMOTE_PUSH_HOST).yaml"; \
	if [ -f $$CONFIG_NAME ]; then \
		echo "Pushing IP-specific config file: $$CONFIG_NAME -> $(REMOTE_PUSH_DIR)/$$CONFIG_NAME"; \
		rsync -azP -e "ssh $$SSH_OPTS" $$CONFIG_NAME $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST):$(REMOTE_PUSH_DIR)/$$CONFIG_NAME; \
	elif [ -f $(CONFIG_FILE) ]; then \
		echo "Pushing default config file: $(CONFIG_FILE) -> $(REMOTE_PUSH_DIR)/config.yaml"; \
		rsync -azP -e "ssh $$SSH_OPTS" $(CONFIG_FILE) $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST):$(REMOTE_PUSH_DIR)/config.yaml; \
	else \
		echo "Warning: Config file '$(CONFIG_FILE)' or '$$CONFIG_NAME' not found, skipping..."; \
		echo "Tip: You can push config.yaml.example as a template if needed"; \
	fi; \
	if [ -f config.yaml.example ]; then \
		echo "Pushing config template: config.yaml.example -> $(REMOTE_PUSH_DIR)/config.yaml.example"; \
		rsync -azP -e "ssh $$SSH_OPTS" config.yaml.example $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST):$(REMOTE_PUSH_DIR)/config.yaml.example; \
	fi; \
	echo "Setting executable permissions..."; \
	ssh $$SSH_OPTS $(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST) "chmod +x $(REMOTE_PUSH_DIR)/$(APP_NAME) && chmod +x $(REMOTE_PUSH_DIR)/$(SCRIPTS_DIR)/*.sh 2>/dev/null || true"; \
	echo "=========================================="; \
	echo "Push completed successfully!"; \
	echo "Remote location: $(REMOTE_PUSH_DIR)"; \
	echo "=========================================="

# -------- SSH Key Management --------
SSH_KEY_NAME?=id_rsa_blueberry
SSH_KEY_PATH?=~/.ssh/$(SSH_KEY_NAME)
SSH_KEY_PUB?=$(SSH_KEY_PATH).pub

# Generate SSH key for server access (print command only)
ssh-keygen:
	@echo "ssh-keygen -t ed25519 -f $(SSH_KEY_PATH) -N \"\" -C \"blueberry-$(REMOTE_PUSH_USER)@$(REMOTE_PUSH_HOST)\""

# Add SSH public key to server authorized_keys (print command only)
ssh-add-key:
	@echo "echo 'ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCjUDQAgCK43S5PCPxyrZZWf9sozcUKWsgygguwmCfzU6YyENhYEDx07hzXyNYlxMrBuAk3pva0AQRPFk4TwWEkVvukMpLfwxFVxiGpF4Dq1F7cfevhPR91XpU4K7kjSLB/KML0b8LMtP7gK5b9+oge0F6r5UYEgMSjlWLsIU1mEmpnutR5B1sSXWKgUQoW957IvaGyb0buW7uH35Ndbl8dIDEdB7eTReCi8m13MdhM5MLbqrccnrCh+gsVSV/I35W9qlRIuJvWv0JkobnDmiTR1QuovctnDa5zxhZsfIqvZN+ItuymONHy8d1qPlfrCt5EE0EGUqk2yf04cbrR4eXKJadok0QZ5fpRjy0nBU5WvsVcj9jUPVX23sGCjurt2pqXsO/cKoRrwIaAAAKW4Ych48xKhDrgSvaZGpRzf1cOuZmUXNVLlT/jSvFDVWurITOFqNX8nx4Hti9/QCvB2u48uKfBvSnAvCMMhEcQF/yigPzTBYVwNT+hjUZY2aLErPE= jimmy@jimmydeMacBook-Pro.local' | cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"

# -------- Docker helpers --------
IMAGE?=blueberry:latest
DOCKER_APP_DIR?=/home/worker/blueberry

docker-build:
	docker build -t $(IMAGE) .

# ARGS allows overriding default command, e.g. ARGS="sync --all"
ARGS?=--help
docker-run:
	docker run --rm -it \
		-v $(PWD)/downloads:$(DOCKER_APP_DIR)/downloads \
		-v $(PWD)/cookies:$(DOCKER_APP_DIR)/cookies \
		-v $(PWD)/config.yaml:$(DOCKER_APP_DIR)/config.yaml \
		$(IMAGE) $(ARGS)



