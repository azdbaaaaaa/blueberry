# Blueberry Systemd 部署指南

本目录包含使用 systemd 部署 Blueberry 服务的相关文件。

## 文件说明

- `blueberry-download.service` - 下载服务的 systemd 文件
- `blueberry-upload.service` - 上传服务的 systemd 文件
- `deploy.sh` - 部署管理脚本

## 快速开始

### 1. 准备服务器（安装依赖）

在部署服务之前，需要先在远程服务器上安装必要的依赖：

```bash
# 在远程服务器上安装依赖（Go, Git, FFmpeg, yt-dlp, Node.js 等）
./scripts/deploy.sh 66.42.63.131 prepare
```

这个命令会：
- 将 `install-deps-ubuntu.sh` 复制到远程服务器
- 在远程服务器上执行安装脚本
- 安装所有必需的依赖

### 2. 准备配置文件

为每个服务器IP创建对应的配置文件，命名格式：`config-<IP>.yaml`

例如：
- `config-66.42.63.131.yaml` - 用于 IP 66.42.63.131 的配置
- `config-192.168.1.100.yaml` - 用于 IP 192.168.1.100 的配置

### 3. 推送文件到服务器

使用 Makefile 的 push 命令（会自动推送对应IP的配置文件）：

```bash
# 设置目标服务器IP
export REMOTE_PUSH_HOST=66.42.63.131

# 构建并推送
make push
```

或者手动指定配置文件：

```bash
make push CONFIG_FILE=config-66.42.63.131.yaml
```

### 4. 安装 systemd 服务

```bash
# 安装服务（需要指定配置文件）
# 此命令会自动：
# 1. 创建远程项目目录
# 2. 本地编译二进制文件
# 3. 复制以下文件到远程服务器：
#    - 可执行文件 (blueberry)
#    - 配置文件 (config.yaml)
#    - cookies 文件夹（如果存在）
#    - scripts 文件夹
# 4. 安装 systemd 服务文件
# 5. 重新加载 systemd daemon
./scripts/deploy.sh 66.42.63.131 install config-66.42.63.131.yaml
```

**注意**: 
- 安装服务需要本地已安装 Go 环境，脚本会自动编译 Linux 版本的二进制文件
- cookies 文件夹如果不存在会跳过，不会报错
- scripts 文件夹会被复制到远程服务器的 `/opt/blueberry/scripts/` 目录

### 5. 启动服务

```bash
# 启动下载服务
./scripts/deploy.sh 66.42.63.131 start download

# 启动上传服务
./scripts/deploy.sh 66.42.63.131 start upload

# 同时启动两个服务
./scripts/deploy.sh 66.42.63.131 start both
```

### 6. 启用自启动

```bash
# 启用下载服务自启动
./scripts/deploy.sh 66.42.63.131 enable download

# 启用上传服务自启动
./scripts/deploy.sh 66.42.63.131 enable upload

# 同时启用两个服务
./scripts/deploy.sh 66.42.63.131 enable both
```

## 部署脚本使用说明

### 基本语法

```bash
./scripts/deploy.sh <ip> <action> [config_file] [service_type]
```

### 参数说明

- `action`: 操作类型
  - `prepare` - 在远程服务器上安装依赖（install-deps-ubuntu.sh）
  - `install` - 安装 systemd 服务（需要 config_file）
  - `uninstall` - 卸载 systemd 服务
  - `start` - 启动服务
  - `stop` - 停止服务
  - `restart` - 重启服务
  - `status` - 查看服务状态
  - `enable` - 启用服务自启动
  - `disable` - 禁用服务自启动
  - `logs` - 查看服务日志

- `ip`: 远程服务器IP地址（用于标识不同的配置）

- `config_file`: 配置文件路径（可选，默认使用 `config-<ip>.yaml`）

- `service_type`: 服务类型（可选，默认 `both`）
  - `download` - 仅下载服务
  - `upload` - 仅上传服务
  - `both` - 两个服务

### 使用示例

```bash
# 准备服务器（安装依赖）
./scripts/deploy.sh 66.42.63.131 prepare

# 安装服务
./scripts/deploy.sh 66.42.63.131 install config-66.42.63.131.yaml

# 启动下载服务
./scripts/deploy.sh 66.42.63.131 start download

# 启动上传服务
./scripts/deploy.sh 66.42.63.131 start upload

# 查看服务状态
./scripts/deploy.sh 66.42.63.131 status both

# 查看日志
./scripts/deploy.sh 66.42.63.131 logs download

# 重启服务
./scripts/deploy.sh 66.42.63.131 restart both

# 停止服务
./scripts/deploy.sh 66.42.63.131 stop both

# 卸载服务
./scripts/deploy.sh 66.42.63.131 uninstall
```

## 多服务器部署

### 场景：多个服务器，每个服务器使用不同配置

1. **准备服务器（安装依赖）**

```bash
# 在每个服务器上安装依赖
./scripts/deploy.sh 66.42.63.131 prepare
./scripts/deploy.sh 192.168.1.100 prepare
./scripts/deploy.sh 10.0.0.50 prepare
```

2. **准备配置文件**

```bash
# 为每个服务器创建配置文件
config-66.42.63.131.yaml
config-192.168.1.100.yaml
config-10.0.0.50.yaml
```

3. **推送文件到各个服务器**

```bash
# 服务器1
export REMOTE_PUSH_HOST=66.42.63.131
make push

# 服务器2
export REMOTE_PUSH_HOST=192.168.1.100
make push

# 服务器3
export REMOTE_PUSH_HOST=10.0.0.50
make push
```

4. **安装服务**

```bash
# 服务器1
./scripts/deploy.sh 66.42.63.131 install config-66.42.63.131.yaml

# 服务器2
./scripts/deploy.sh 192.168.1.100 install config-192.168.1.100.yaml

# 服务器3
./scripts/deploy.sh 10.0.0.50 install config-10.0.0.50.yaml
```

5. **启动服务**

```bash
# 服务器1
./scripts/deploy.sh 66.42.63.131 start both
./scripts/deploy.sh 66.42.63.131 enable both

# 服务器2
./scripts/deploy.sh 192.168.1.100 start both
./scripts/deploy.sh 192.168.1.100 enable both

# 服务器3
./scripts/deploy.sh 10.0.0.50 start both
./scripts/deploy.sh 10.0.0.50 enable both
```

## SSH 密钥配置

脚本会自动查找 SSH 密钥：`~/.ssh/id_ed25519_blueberry_<user>_<ip>`

如果需要使用不同的密钥，可以设置环境变量或修改脚本中的 `SSH_KEY_PATH` 变量。

## 服务管理

### 查看服务状态

```bash
# 查看下载服务状态
./scripts/deploy.sh 66.42.63.131 status download

# 查看上传服务状态
./scripts/deploy.sh 66.42.63.131 status upload

# 查看两个服务状态
./scripts/deploy.sh 66.42.63.131 status both
```

### 查看日志

```bash
# 查看下载服务日志
./scripts/deploy.sh 66.42.63.131 logs download

# 查看上传服务日志
./scripts/deploy.sh 66.42.63.131 logs upload

# 实时查看日志（在服务器上执行）
journalctl -u blueberry-download.service -f
journalctl -u blueberry-upload.service -f
```

### 重启服务

```bash
# 重启下载服务
./scripts/deploy.sh 66.42.63.131 restart download

# 重启上传服务
./scripts/deploy.sh 66.42.63.131 restart upload
```

## 注意事项

1. **配置文件路径**: systemd 服务使用 `/opt/blueberry/config.yaml` 作为配置文件路径（部署时会从本地配置文件复制过去）
2. **工作目录**: 服务的工作目录是 `/opt/blueberry`
3. **日志**: 服务日志通过 systemd journal 管理，使用 `journalctl` 查看
4. **自动重启**: 服务配置了自动重启，如果崩溃会在 10 秒后自动重启
5. **权限**: 服务以 root 用户运行，确保有足够的权限访问文件和目录

## 故障排查

### 服务无法启动

1. 检查二进制文件是否存在：
   ```bash
   ssh root@66.42.63.131 "test -f /opt/blueberry/blueberry && echo 'OK' || echo 'NOT FOUND'"
   ```

2. 检查配置文件是否存在：
   ```bash
   ssh root@66.42.63.131 "test -f /opt/blueberry/config.yaml && echo 'OK' || echo 'NOT FOUND'"
   ```

3. 检查服务状态：
   ```bash
   ./scripts/deploy.sh 66.42.63.131 status download
   ```

4. 查看详细日志：
   ```bash
   ./scripts/deploy.sh 66.42.63.131 logs download
   ```

### 配置文件错误

如果配置文件有错误，服务会启动失败。检查日志：

```bash
journalctl -u blueberry-download.service -n 100
```

