#!/bin/bash

# Blueberry 部署脚本
# 用法: ./deploy.sh <action> <ip1> [ip2] [ip3] ... [service_type] [--init]

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
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_ip() { echo -e "${BLUE}[$1]${NC} $2"; }

# 检查参数
if [ $# -lt 2 ]; then
    log_error "参数不足"
    echo "用法: $0 <action> <ip1> [ip2] [ip3] ... [service_type] [--init]"
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
    echo "  logs     - 查看服务日志（仅支持单个 IP）"
    echo ""
    echo "Service types: download, upload, both (默认: both)"
    echo ""
    echo "示例:"
    echo "  $0 prepare 66.42.63.131"
    echo "  $0 install 66.42.63.131 194.233.83.29"
    echo "  $0 install 66.42.63.131 --init"
    echo "  $0 start 66.42.63.131 download"
    echo "  $0 restart 66.42.63.131 194.233.83.29 upload"
    exit 1
fi

ACTION=$1
shift  # 移除 ACTION

# 解析参数：提取 IP 列表、--init 选项和 service_type
IPS=()
INIT_AFTER_INSTALL=false
SERVICE_TYPE="both"

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
            # 验证 IP 格式
            if [[ $1 =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
                IPS+=("$1")
            else
                log_error "无效的 IP 地址: $1"
                exit 1
            fi
            shift
            ;;
    esac
done

# 检查是否有 IP
if [ ${#IPS[@]} -eq 0 ]; then
    log_error "未提供有效的 IP 地址"
    exit 1
fi

# 验证 IP 格式的函数
validate_ip() {
    local ip=$1
    if [[ ! $ip =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
        log_error "无效的 IP 地址: $ip"
        return 1
    fi
    return 0
}

# 为单个 IP 设置 SSH 选项和远程主机
setup_ssh_for_ip() {
    local ip=$1
    SSH_OPTS=""
    SSH_KEY_PATH="$HOME/.ssh/id_ed25519_blueberry_${REMOTE_USER}_${ip}"
    if [ -f "$SSH_KEY_PATH" ]; then
        SSH_OPTS="-i $SSH_KEY_PATH"
    fi
    REMOTE_HOST="${REMOTE_USER}@${ip}"
    CONFIG_FILE="config-${ip}.yaml"
}

# 远程执行函数（需要先调用 setup_ssh_for_ip）
remote_exec() { ssh $SSH_OPTS $REMOTE_HOST "$@"; }
remote_copy() { rsync -azP -e "ssh $SSH_OPTS" "$@"; }

# 准备服务器（安装依赖）
prepare_server_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "准备服务器，安装依赖..."
    
    # 检查 install-deps-ubuntu.sh 是否存在
    INSTALL_DEPS_SCRIPT="$SCRIPT_DIR/install-deps-ubuntu.sh"
    if [ ! -f "$INSTALL_DEPS_SCRIPT" ]; then
        log_error "[$ip] 依赖安装脚本不存在: $INSTALL_DEPS_SCRIPT"
        return 1
    fi
    
    # 复制脚本到远程服务器
    log_ip "$ip" "复制安装脚本到远程服务器..."
    remote_copy "$INSTALL_DEPS_SCRIPT" "$REMOTE_HOST:/tmp/install-deps-ubuntu.sh" || return 1
    
    # 在远程服务器上执行脚本
    log_ip "$ip" "在远程服务器上执行安装脚本..."
    remote_exec "chmod +x /tmp/install-deps-ubuntu.sh && /tmp/install-deps-ubuntu.sh" || return 1
    
    # 清理临时文件
    remote_exec "rm -f /tmp/install-deps-ubuntu.sh" || true
    
    log_ip "$ip" "服务器准备完成！"
    return 0
}

# 安装服务（针对单个 IP）
install_service_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "安装 systemd 服务"
    
    if [ ! -f "$CONFIG_FILE" ]; then
        log_error "[$ip] 配置文件不存在: $CONFIG_FILE"
        return 1
    fi
    
    # 步骤 1: 创建远程项目目录和日志目录
    log_ip "$ip" "步骤 1/5: 创建远程项目目录和日志目录..."
    remote_exec "mkdir -p $REMOTE_DIR && chmod 755 $REMOTE_DIR" || return 1
    remote_exec "mkdir -p /var/log/blueberry && chmod 755 /var/log/blueberry" || return 1
    
    # 步骤 2: 本地编译（已在主循环外完成）
    log_ip "$ip" "步骤 2/5: 使用已编译的二进制文件..."
    
    # 步骤 3: 复制可执行文件、配置文件、cookies 和 scripts 文件夹到远程服务器
    log_ip "$ip" "步骤 3/5: 复制文件到远程服务器..."
    
    log_ip "$ip" "复制二进制文件: $BIN -> $REMOTE_DIR/$BIN_NAME"
    remote_copy "$BIN" "$REMOTE_HOST:$REMOTE_DIR/$BIN_NAME" || return 1
    remote_exec "chmod +x $REMOTE_DIR/$BIN_NAME" || return 1
    
    log_ip "$ip" "复制配置文件: $CONFIG_FILE -> $REMOTE_DIR/config.yaml"
    remote_copy "$CONFIG_FILE" "$REMOTE_HOST:$REMOTE_DIR/config.yaml" || return 1
    
    # 复制 cookies 文件夹（如果存在）
    COOKIES_DIR="$PROJECT_DIR/cookies"
    if [ -d "$COOKIES_DIR" ]; then
        log_ip "$ip" "复制 cookies 文件夹: $COOKIES_DIR -> $REMOTE_DIR/cookies/"
        remote_copy "$COOKIES_DIR/" "$REMOTE_HOST:$REMOTE_DIR/cookies/" || return 1
    else
        log_warn "[$ip] Cookies 文件夹不存在: $COOKIES_DIR，跳过..."
    fi
    
    # 复制 scripts 文件夹
    log_ip "$ip" "复制 scripts 文件夹: $SCRIPT_DIR -> $REMOTE_DIR/scripts/"
    remote_copy "$SCRIPT_DIR/" "$REMOTE_HOST:$REMOTE_DIR/scripts/" || return 1
    remote_exec "chmod +x $REMOTE_DIR/scripts/*.sh 2>/dev/null || true" || return 1
    
    # 步骤 4: 复制 systemd 服务
    log_ip "$ip" "步骤 4/5: 复制 systemd 服务..."
    remote_copy "$SCRIPT_DIR/blueberry-download.service" "$REMOTE_HOST:/etc/systemd/system/" || return 1
    remote_copy "$SCRIPT_DIR/blueberry-upload.service" "$REMOTE_HOST:/etc/systemd/system/" || return 1
    
    # 步骤 5: 重新加载 systemd daemon
    log_ip "$ip" "步骤 5/5: 重新加载 systemd daemon..."
    remote_exec "systemctl daemon-reload" || return 1
    
    log_ip "$ip" "服务安装完成！"
    
    # 如果指定了 --init，执行 channel 命令获取频道信息
    if [ "$INIT_AFTER_INSTALL" = true ]; then
        log_ip "$ip" "执行初始化：获取频道信息..."
        if remote_exec "cd $REMOTE_DIR && $REMOTE_DIR/$BIN_NAME channel --config $REMOTE_DIR/config.yaml"; then
            log_ip "$ip" "频道信息获取完成！"
        else
            log_warn "[$ip] 频道信息获取失败，请检查配置和网络连接"
        fi
    fi
    
    return 0
}

# 卸载服务（针对单个 IP）
uninstall_service_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "卸载 systemd 服务..."
    remote_exec "systemctl stop ${SERVICE_PREFIX}-download.service 2>/dev/null || true"
    remote_exec "systemctl stop ${SERVICE_PREFIX}-upload.service 2>/dev/null || true"
    remote_exec "systemctl disable ${SERVICE_PREFIX}-download.service 2>/dev/null || true"
    remote_exec "systemctl disable ${SERVICE_PREFIX}-upload.service 2>/dev/null || true"
    remote_exec "rm -f /etc/systemd/system/${SERVICE_PREFIX}-download.service"
    remote_exec "rm -f /etc/systemd/system/${SERVICE_PREFIX}-upload.service"
    remote_exec "systemctl daemon-reload"
    log_ip "$ip" "服务已卸载"
}

# 服务操作（针对单个 IP）
service_action_for_ip() {
    local ip=$1
    local action=$2
    local service_name=$3
    
    setup_ssh_for_ip "$ip"
    
    case $service_name in
        download) remote_exec "systemctl $action ${SERVICE_PREFIX}-download.service" ;;
        upload)   remote_exec "systemctl $action ${SERVICE_PREFIX}-upload.service" ;;
        both)
            remote_exec "systemctl $action ${SERVICE_PREFIX}-download.service"
            remote_exec "systemctl $action ${SERVICE_PREFIX}-upload.service"
            ;;
        *)
            log_error "[$ip] 无效的服务类型: $service_name (应为 download/upload/both)"
            return 1
            ;;
    esac
}

# 启动服务（针对单个 IP）
start_service_for_ip() {
    local ip=$1
    log_ip "$ip" "启动服务: $SERVICE_TYPE"
    service_action_for_ip "$ip" "start" "$SERVICE_TYPE"
}

# 停止服务（针对单个 IP）
stop_service_for_ip() {
    local ip=$1
    log_ip "$ip" "停止服务: $SERVICE_TYPE"
    service_action_for_ip "$ip" "stop" "$SERVICE_TYPE"
}

# 重启服务（针对单个 IP）
restart_service_for_ip() {
    local ip=$1
    log_ip "$ip" "重启服务: $SERVICE_TYPE"
    service_action_for_ip "$ip" "restart" "$SERVICE_TYPE"
}

# 查看状态（针对单个 IP）
status_service_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "查看服务状态"
    case $SERVICE_TYPE in
        download)
            remote_exec "systemctl status ${SERVICE_PREFIX}-download.service --no-pager -l"
            ;;
        upload)
            remote_exec "systemctl status ${SERVICE_PREFIX}-upload.service --no-pager -l"
            ;;
        both)
            log_ip "$ip" "下载服务状态:"
            remote_exec "systemctl status ${SERVICE_PREFIX}-download.service --no-pager -l"
            echo ""
            log_ip "$ip" "上传服务状态:"
            remote_exec "systemctl status ${SERVICE_PREFIX}-upload.service --no-pager -l"
            ;;
    esac
}

# 启用服务（针对单个 IP）
enable_service_for_ip() {
    local ip=$1
    log_ip "$ip" "启用服务自启动: $SERVICE_TYPE"
    service_action_for_ip "$ip" "enable" "$SERVICE_TYPE"
}

# 禁用服务（针对单个 IP）
disable_service_for_ip() {
    local ip=$1
    log_ip "$ip" "禁用服务自启动: $SERVICE_TYPE"
    service_action_for_ip "$ip" "disable" "$SERVICE_TYPE"
}

# 查看日志（仅支持单个 IP）
logs_service_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    case $SERVICE_TYPE in
        download)
            log_ip "$ip" "查看下载服务日志（按 Ctrl+C 退出）..."
            log_ip "$ip" "标准输出: /var/log/blueberry/download.log"
            log_ip "$ip" "错误输出: /var/log/blueberry/download.error.log"
            remote_exec "tail -f /var/log/blueberry/download.log /var/log/blueberry/download.error.log"
            ;;
        upload)
            log_ip "$ip" "查看上传服务日志（按 Ctrl+C 退出）..."
            log_ip "$ip" "标准输出: /var/log/blueberry/upload.log"
            log_ip "$ip" "错误输出: /var/log/blueberry/upload.error.log"
            remote_exec "tail -f /var/log/blueberry/upload.log /var/log/blueberry/upload.error.log"
            ;;
        both)
            log_ip "$ip" "同时追踪下载和上传服务日志（按 Ctrl+C 退出）..."
            log_ip "$ip" "下载服务 - 标准输出: /var/log/blueberry/download.log"
            log_ip "$ip" "下载服务 - 错误输出: /var/log/blueberry/download.error.log"
            log_ip "$ip" "上传服务 - 标准输出: /var/log/blueberry/upload.log"
            log_ip "$ip" "上传服务 - 错误输出: /var/log/blueberry/upload.error.log"
            remote_exec "tail -f /var/log/blueberry/download.log /var/log/blueberry/download.error.log /var/log/blueberry/upload.log /var/log/blueberry/upload.error.log"
            ;;
    esac
}

# 处理所有 IP 的主函数
process_all_ips() {
    local action=$1
    local success_count=0
    local fail_count=0
    local failed_ips=()
    
    log_info "处理 ${#IPS[@]} 个服务器: ${IPS[*]}"
    echo ""
    
    # 对于 install 操作，先编译一次（所有服务器共享）
    if [ "$action" = "install" ]; then
        if [ ! -f "$BIN" ]; then
            log_info "本地编译（所有服务器共享）..."
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
        else
            log_info "使用已编译的二进制文件: $BIN"
        fi
        echo ""
    fi
    
    for ip in "${IPS[@]}"; do
        echo "========================================"
        log_info "处理服务器: $ip"
        echo "========================================"
        
        case $action in
            prepare)
                if prepare_server_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            install)
                if install_service_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            uninstall)
                if uninstall_service_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            start)
                if start_service_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            stop)
                if stop_service_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            restart)
                if restart_service_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            status)
                status_service_for_ip "$ip"
                ((success_count++))
                ;;
            enable)
                if enable_service_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            disable)
                if disable_service_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            logs)
                # logs 命令只支持单个 IP
                if [ ${#IPS[@]} -gt 1 ]; then
                    log_error "logs 命令仅支持单个 IP，请指定一个 IP 地址"
                    exit 1
                fi
                logs_service_for_ip "$ip"
                exit 0
                ;;
            *)
                log_error "未知操作: $action"
                exit 1
                ;;
        esac
        
        echo ""
    done
    
    # 输出总结
    echo "========================================"
    log_info "处理完成"
    echo "========================================"
    log_info "成功: $success_count 个服务器"
    if [ $fail_count -gt 0 ]; then
        log_error "失败: $fail_count 个服务器"
        log_error "失败的服务器: ${failed_ips[*]}"
        exit 1
    fi
}

# 执行操作
process_all_ips "$ACTION"
