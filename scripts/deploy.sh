#!/bin/bash

# Blueberry 部署脚本
# 用法: ./deploy.sh <ip> <action> [config_file] [service_type]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_DIR="/opt/blueberry"
SERVICE_PREFIX="blueberry"
BIN_NAME="blueberry"
BIN_DIR="$PROJECT_DIR/bin"
BIN="$BIN_DIR/$BIN_NAME"

# 颜色输出
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 检查参数
if [ $# -lt 2 ]; then
    log_error "参数不足"
    echo "用法: $0 <ip> <action> [service_type] [--init]"
    echo ""
    echo "Actions: prepare, install, uninstall, start, stop, restart, status, enable, disable, logs"
    echo "  prepare  - 在远程服务器上安装依赖（install-deps-ubuntu.sh）"
    echo "  install  - 安装 systemd 服务（使用 config-<ip>.yaml）"
    echo "            --init: 安装完成后运行 'channel' 命令获取频道信息"
    echo "  uninstall - 卸载 systemd 服务"
    echo "  start    - 启动服务"
    echo "  stop     - 停止服务"
    echo "  restart  - 重启服务"
    echo "  status   - 查看服务状态"
    echo "  enable   - 启用服务自启动"
    echo "  disable  - 禁用服务自启动"
    echo "  logs     - 查看服务日志"
    echo ""
    echo "Service types: download, upload, both (默认: both)"
    echo ""
    echo "示例:"
    echo "  $0 66.42.63.131 prepare"
    echo "  $0 66.42.63.131 install"
    echo "  $0 66.42.63.131 install --init"
    echo "  $0 66.42.63.131 start download"
    echo "  $0 66.42.63.131 restart upload"
    exit 1
fi

IP=$1
ACTION=$2
# 统一使用 config-${IP}.yaml 作为配置文件
CONFIG_FILE="config-${IP}.yaml"

# 解析参数：检查是否有 --init 选项
INIT_AFTER_INSTALL=false
SERVICE_TYPE="both"

# 从参数中提取 --init 和 service_type
shift 2  # 移除 IP 和 ACTION
while [ $# -gt 0 ]; do
    case $1 in
        --init)
            INIT_AFTER_INSTALL=true
            shift
            ;;
        download|upload|both)
            SERVICE_TYPE=$1
            shift
            ;;
        *)
            log_error "未知参数: $1"
            exit 1
            ;;
    esac
done

# 验证 IP 格式
if [[ ! $IP =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
    log_error "无效的 IP 地址: $IP"
    exit 1
fi

# SSH 选项
SSH_OPTS=""
SSH_KEY_PATH="$HOME/.ssh/id_ed25519_blueberry_${REMOTE_USER}_${IP}"
if [ -f "$SSH_KEY_PATH" ]; then
    SSH_OPTS="-i $SSH_KEY_PATH"
fi

REMOTE_HOST="${REMOTE_USER}@${IP}"

remote_exec() { ssh $SSH_OPTS $REMOTE_HOST "$@"; }
remote_copy() { rsync -azP -e "ssh $SSH_OPTS" "$@"; }

# 准备服务器（安装依赖）
prepare_server() {
    log_info "准备服务器 $REMOTE_HOST，安装依赖..."
    
    # 检查 install-deps-ubuntu.sh 是否存在
    INSTALL_DEPS_SCRIPT="$SCRIPT_DIR/install-deps-ubuntu.sh"
    if [ ! -f "$INSTALL_DEPS_SCRIPT" ]; then
        log_error "依赖安装脚本不存在: $INSTALL_DEPS_SCRIPT"
        exit 1
    fi
    
    # 复制脚本到远程服务器
    log_info "复制安装脚本到远程服务器..."
    remote_copy "$INSTALL_DEPS_SCRIPT" "$REMOTE_HOST:/tmp/install-deps-ubuntu.sh"
    
    # 在远程服务器上执行脚本
    log_info "在远程服务器上执行安装脚本..."
    remote_exec "chmod +x /tmp/install-deps-ubuntu.sh && /tmp/install-deps-ubuntu.sh"
    
    # 清理临时文件
    remote_exec "rm -f /tmp/install-deps-ubuntu.sh"
    
    log_info "服务器准备完成！"
}

# 安装服务
install_service() {
    log_info "安装 systemd 服务到 $REMOTE_HOST"
    
    if [ ! -f "$CONFIG_FILE" ]; then
        log_error "配置文件不存在: $CONFIG_FILE"
        exit 1
    fi
    
    # 步骤 1: 创建远程项目目录和日志目录
    log_info "步骤 1/5: 创建远程项目目录和日志目录..."
    remote_exec "mkdir -p $REMOTE_DIR && chmod 755 $REMOTE_DIR"
    remote_exec "mkdir -p /var/log/blueberry && chmod 755 /var/log/blueberry"
    
    # 步骤 2: 本地编译
    log_info "步骤 2/5: 本地编译..."
    if ! command -v go >/dev/null 2>&1; then
        log_error "Go 未安装，无法编译。请先安装 Go 或使用已编译的二进制文件。"
        exit 1
    fi
    
    cd "$PROJECT_DIR"
    log_info "编译中..."
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BIN" .
    if [ ! -f "$BIN" ]; then
        log_error "编译失败，二进制文件不存在: $BIN"
        exit 1
    fi
    log_info "编译完成: $BIN"
    
    # 步骤 3: 复制可执行文件、配置文件、cookies 和 scripts 文件夹到远程服务器
    log_info "步骤 3/5: 复制文件到远程服务器..."
    
    log_info "复制二进制文件: $BIN -> $REMOTE_DIR/$BIN_NAME"
    remote_copy "$BIN" "$REMOTE_HOST:$REMOTE_DIR/$BIN_NAME"
    remote_exec "chmod +x $REMOTE_DIR/$BIN_NAME"
    
    log_info "复制配置文件: $CONFIG_FILE -> $REMOTE_DIR/config.yaml"
    remote_copy "$CONFIG_FILE" "$REMOTE_HOST:$REMOTE_DIR/config.yaml"
    
    # 复制 cookies 文件夹（如果存在）
    COOKIES_DIR="$PROJECT_DIR/cookies"
    if [ -d "$COOKIES_DIR" ]; then
        log_info "复制 cookies 文件夹: $COOKIES_DIR -> $REMOTE_DIR/cookies/"
        remote_copy "$COOKIES_DIR/" "$REMOTE_HOST:$REMOTE_DIR/cookies/"
    else
        log_warn "Cookies 文件夹不存在: $COOKIES_DIR，跳过..."
    fi
    
    # 复制 scripts 文件夹
    log_info "复制 scripts 文件夹: $SCRIPT_DIR -> $REMOTE_DIR/scripts/"
    remote_copy "$SCRIPT_DIR/" "$REMOTE_HOST:$REMOTE_DIR/scripts/"
    remote_exec "chmod +x $REMOTE_DIR/scripts/*.sh 2>/dev/null || true"
    
    # 步骤 4: 复制 systemd 服务
    log_info "步骤 4/5: 复制 systemd 服务..."
    remote_copy "$SCRIPT_DIR/blueberry-download.service" "$REMOTE_HOST:/etc/systemd/system/"
    remote_copy "$SCRIPT_DIR/blueberry-upload.service" "$REMOTE_HOST:/etc/systemd/system/"
    
    # 步骤 5: 重新加载 systemd daemon
    log_info "步骤 5/5: 重新加载 systemd daemon..."
    remote_exec "systemctl daemon-reload"
    
    log_info "服务安装完成！"
    
    # 如果指定了 --init，执行 channel 命令获取频道信息
    if [ "$INIT_AFTER_INSTALL" = true ]; then
        log_info "执行初始化：获取频道信息..."
        remote_exec "cd $REMOTE_DIR && $REMOTE_DIR/$BIN_NAME channel --config $REMOTE_DIR/config.yaml"
        if [ $? -eq 0 ]; then
            log_info "频道信息获取完成！"
        else
            log_warn "频道信息获取失败，请检查配置和网络连接"
        fi
    fi
    
    log_info "使用以下命令启动服务:"
    echo "  $0 $IP start download"
    echo "  $0 $IP start upload"
}

# 卸载服务
uninstall_service() {
    log_info "卸载 systemd 服务..."
    remote_exec "systemctl stop ${SERVICE_PREFIX}-download.service 2>/dev/null || true"
    remote_exec "systemctl stop ${SERVICE_PREFIX}-upload.service 2>/dev/null || true"
    remote_exec "systemctl disable ${SERVICE_PREFIX}-download.service 2>/dev/null || true"
    remote_exec "systemctl disable ${SERVICE_PREFIX}-upload.service 2>/dev/null || true"
    remote_exec "rm -f /etc/systemd/system/${SERVICE_PREFIX}-download.service"
    remote_exec "rm -f /etc/systemd/system/${SERVICE_PREFIX}-upload.service"
    remote_exec "systemctl daemon-reload"
    log_info "服务已卸载"
}

# 服务操作
service_action() {
    local action=$1
    local service_name=$2
    
    case $service_name in
        download) remote_exec "systemctl $action ${SERVICE_PREFIX}-download.service" ;;
        upload)   remote_exec "systemctl $action ${SERVICE_PREFIX}-upload.service" ;;
        both)
            remote_exec "systemctl $action ${SERVICE_PREFIX}-download.service"
            remote_exec "systemctl $action ${SERVICE_PREFIX}-upload.service"
            ;;
        *)
            log_error "无效的服务类型: $service_name (应为 download/upload/both)"
            exit 1
            ;;
    esac
}

# 启动服务
start_service() {
    log_info "启动服务: $SERVICE_TYPE"
    service_action "start" "$SERVICE_TYPE"
}

# 停止服务
stop_service() {
    log_info "停止服务: $SERVICE_TYPE"
    service_action "stop" "$SERVICE_TYPE"
}

# 重启服务
restart_service() {
    log_info "重启服务: $SERVICE_TYPE"
    service_action "restart" "$SERVICE_TYPE"
}

# 查看状态
status_service() {
    case $SERVICE_TYPE in
        download)
            remote_exec "systemctl status ${SERVICE_PREFIX}-download.service --no-pager -l"
            ;;
        upload)
            remote_exec "systemctl status ${SERVICE_PREFIX}-upload.service --no-pager -l"
            ;;
        both)
            log_info "下载服务状态:"
            remote_exec "systemctl status ${SERVICE_PREFIX}-download.service --no-pager -l"
            echo ""
            log_info "上传服务状态:"
            remote_exec "systemctl status ${SERVICE_PREFIX}-upload.service --no-pager -l"
            ;;
    esac
}

# 启用服务
enable_service() {
    log_info "启用服务自启动: $SERVICE_TYPE"
    service_action "enable" "$SERVICE_TYPE"
}

# 禁用服务
disable_service() {
    log_info "禁用服务自启动: $SERVICE_TYPE"
    service_action "disable" "$SERVICE_TYPE"
}

# 查看日志
logs_service() {
    case $SERVICE_TYPE in
        download)
            log_info "查看下载服务日志（按 Ctrl+C 退出）..."
            log_info "标准输出: /var/log/blueberry/download.log"
            log_info "错误输出: /var/log/blueberry/download.error.log"
            remote_exec "tail -f /var/log/blueberry/download.log /var/log/blueberry/download.error.log"
            ;;
        upload)
            log_info "查看上传服务日志（按 Ctrl+C 退出）..."
            log_info "标准输出: /var/log/blueberry/upload.log"
            log_info "错误输出: /var/log/blueberry/upload.error.log"
            remote_exec "tail -f /var/log/blueberry/upload.log /var/log/blueberry/upload.error.log"
            ;;
        both)
            log_info "同时追踪下载和上传服务日志（按 Ctrl+C 退出）..."
            log_info "下载服务 - 标准输出: /var/log/blueberry/download.log"
            log_info "下载服务 - 错误输出: /var/log/blueberry/download.error.log"
            log_info "上传服务 - 标准输出: /var/log/blueberry/upload.log"
            log_info "上传服务 - 错误输出: /var/log/blueberry/upload.error.log"
            remote_exec "tail -f /var/log/blueberry/download.log /var/log/blueberry/download.error.log /var/log/blueberry/upload.log /var/log/blueberry/upload.error.log"
            ;;
    esac
}

# 执行操作
case $ACTION in
    prepare)   prepare_server ;;
    install)   install_service ;;
    uninstall) uninstall_service ;;
    start)     start_service ;;
    stop)      stop_service ;;
    restart)   restart_service ;;
    status)    status_service ;;
    enable)    enable_service ;;
    disable)   disable_service ;;
    logs)      logs_service ;;
    *)
        log_error "未知操作: $ACTION"
        exit 1
        ;;
esac

