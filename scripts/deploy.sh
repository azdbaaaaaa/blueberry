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
if [ $# -lt 1 ]; then
    log_error "参数不足"
    echo "用法: $0 <action1> [action2] [action3] ... [ip1] [ip2] [ip3] ... [service_type] [--init]"
    echo ""
    echo "Actions: prepare, install, uninstall, start, stop, restart, status, enable, disable, logs, reset, sync, sync-meta, organize, subtitle"
    echo "  - 支持连续执行多个操作，用空格分隔（如: install reset restart）"
    echo "  - 操作将按顺序执行"
    echo "  prepare  - 在远程服务器上安装依赖（install-deps-ubuntu.sh）"
    echo "  install  - 安装 systemd 服务（使用 config-<ip>.yaml 或 <编号>.config-<ip>.yaml）"
    echo "            --init: 安装完成后运行 'channel' 命令获取频道信息"
    echo "  uninstall - 卸载 systemd 服务"
    echo "  start    - 启动服务"
    echo "  stop     - 停止服务"
    echo "  restart  - 重启服务"
    echo "  status   - 查看服务状态"
    echo "  enable   - 启用服务自启动"
    echo "  disable  - 禁用服务自启动"
    echo "  logs     - 查看服务日志（仅支持单个 IP）"
    echo "  reset    - 清除机器人检测和下载计数的持久化数据"
    echo "  sync     - 同步远程服务器的 output 目录到本地，并收集网卡流量详细信息"
    echo "  sync-meta - 同步远程服务器的 downloads 目录下的元数据文件（.description, .json, .srt）到本地"
    echo "  organize - 整理 output 目录：生成流量汇总统计，整理字幕文件夹到归档目录"
    echo "  subtitle - 在远程服务器上后台执行 subtitle 命令（如果已在执行则跳过）"
    echo ""
    echo "Service types: download, upload, both (默认: both)"
    echo ""
    echo "IP 参数:"
    echo "  - 可以指定 IP 地址（如: 66.42.63.131）"
    echo "  - 可以指定编号（如: 1, 2, 3），脚本会自动查找对应的配置文件（如: 1.config-IP.yaml）"
    echo "  - 如果未提供 IP，将自动查找所有编号配置文件（[0-9]*.config-*.yaml）并提取 IP"
    echo "  - 支持逗号分隔的多个 IP 或编号（如: 1,2,3 或 66.42.63.131,194.233.83.29）"
    echo ""
    echo "示例:"
    echo "  $0 prepare 66.42.63.131"
    echo "  $0 install 1,2,3                    # 使用编号 1, 2, 3"
    echo "  $0 install reset restart 1          # 连续执行多个操作"
    echo "  $0 install reset restart 1,2,3      # 对多个服务器连续执行操作"
    echo "  $0 install 1,194.233.83.29          # 混合使用编号和 IP"
    echo "  $0 install 66.42.63.131,194.233.83.29"
    echo "  $0 install 1,2,3 --init              # 使用编号并初始化"
    echo "  $0 start 1 download                 # 使用编号 1"
    echo "  $0 restart 1,2 upload                # 使用编号 1, 2"
    echo "  $0 start  # 自动查找所有配置文件"
    exit 1
fi

# 解析 ACTION 列表（支持多个操作）
ACTIONS=()
while [ $# -gt 0 ]; do
    case $1 in
        prepare|install|uninstall|start|stop|restart|status|enable|disable|logs|reset|sync|sync-meta|organize|subtitle)
            ACTIONS+=("$1")
            shift
            ;;
        --init|download|upload|both|*)
            # 遇到非 ACTION 参数，停止解析 ACTION
            break
            ;;
    esac
done

# 如果没有找到任何 ACTION，报错
if [ ${#ACTIONS[@]} -eq 0 ]; then
    log_error "未指定任何操作"
    exit 1
fi

# 解析参数：提取 IP 列表、--init 选项和 service_type
IPS=()
INIT_AFTER_INSTALL=false
SERVICE_TYPE="both"

# 根据编号查找配置文件并提取 IP
find_ip_by_number() {
    local number=$1
    local config_pattern="${number}.config-*.yaml"
    local found_configs=()
    
    # 查找匹配的配置文件
    for config in $config_pattern; do
        if [ -f "$config" ]; then
            found_configs+=("$config")
        fi
    done
    
    if [ ${#found_configs[@]} -eq 0 ]; then
        log_error "未找到编号为 $number 的配置文件（${number}.config-*.yaml）" >&2
        return 1
    fi
    
    if [ ${#found_configs[@]} -gt 1 ]; then
        log_warn "找到多个匹配的配置文件，使用第一个: ${found_configs[0]}" >&2
    fi
    
    # 从配置文件名中提取 IP
    # 格式: <编号>.config-<IP>.yaml
    local config_file="${found_configs[0]}"
    # 使用更兼容的 sed 正则表达式
    local ip=$(echo "$config_file" | sed -n 's/^[0-9][0-9]*\.config-\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)\.yaml$/\1/p')
    
    if [ -z "$ip" ]; then
        log_error "无法从配置文件名中提取 IP: $config_file" >&2
        return 1
    fi
    
    log_info "编号 $number 对应配置文件: $config_file (IP: $ip)" >&2
    echo "$ip"  # 只输出 IP 到 stdout
    return 0
}

# 解析单个 IP、编号或逗号分隔的 IP/编号列表
parse_ips() {
    local input=$1
    # 如果包含逗号，按逗号分割
    if [[ $input == *","* ]]; then
        IFS=',' read -ra ITEM_ARRAY <<< "$input"
        for item in "${ITEM_ARRAY[@]}"; do
            item=$(echo "$item" | xargs)  # 去除首尾空格
            
            # 检查是否是 IP 地址
            if [[ $item =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
                IPS+=("$item")
            # 检查是否是纯数字（编号）
            elif [[ $item =~ ^[0-9]+$ ]]; then
                local ip=$(find_ip_by_number "$item")
                if [ $? -eq 0 ] && [ -n "$ip" ]; then
                    IPS+=("$ip")
                else
                    return 1
                fi
            else
                log_error "无效的 IP 地址或编号: $item"
                return 1
            fi
        done
    else
        # 单个 IP 或编号
        # 检查是否是 IP 地址
        if [[ $input =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
            IPS+=("$input")
        # 检查是否是纯数字（编号）
        elif [[ $input =~ ^[0-9]+$ ]]; then
            local ip=$(find_ip_by_number "$input")
            if [ $? -eq 0 ] && [ -n "$ip" ]; then
                IPS+=("$ip")
            else
                return 1
            fi
        else
            log_error "无效的 IP 地址或编号: $input"
            return 1
        fi
    fi
    return 0
}

# 继续解析剩余参数（IP、service_type、--init）
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
            # 解析 IP（支持逗号分隔或单个 IP）
            if ! parse_ips "$1"; then
                exit 1
            fi
            shift
            ;;
    esac
done

# 如果没有提供 IP，自动查找所有编号配置文件
if [ ${#IPS[@]} -eq 0 ]; then
    log_info "未指定 IP，自动查找所有编号配置文件..."
    cd "$PROJECT_DIR"
    found_configs=()
    for config in [0-9]*.config-*.yaml; do
        if [ -f "$config" ]; then
            found_configs+=("$config")
        fi
    done
    
    if [ ${#found_configs[@]} -eq 0 ]; then
        log_error "未提供 IP 地址，且未找到任何编号配置文件（[0-9]*.config-*.yaml）"
        log_error "请执行以下操作之一："
        log_error "  1. 指定 IP 地址或编号（如: $0 install 1,2,3）"
        log_error "  2. 创建编号配置文件（如: 1.config-IP.yaml）"
        exit 1
    else
        # 从配置文件中提取 IP
        log_info "找到 ${#found_configs[@]} 个配置文件"
        for config_file in "${found_configs[@]}"; do
            # 从配置文件名中提取 IP（格式: <编号>.config-<IP>.yaml）
            ip=$(echo "$config_file" | sed -n 's/^[0-9][0-9]*\.config-\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)\.yaml$/\1/p')
            if [ -n "$ip" ]; then
                IPS+=("$ip")
            fi
        done
        # 去重并排序
        IFS=$'\n' IPS=($(printf '%s\n' "${IPS[@]}" | sort -u))
        log_info "从配置文件中提取到 ${#IPS[@]} 个 IP: ${IPS[*]}"
    fi
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
    
    # 查找配置文件，支持带编号的格式（如 1.config-IP.yaml）
    # 优先级：先查找带编号的配置文件（按数字排序），如果找不到则使用默认格式
    CONFIG_FILE=""
    local default_config="config-${ip}.yaml"
    
    # 查找所有匹配 *config-${ip}.yaml 的文件
    local found_configs=()
    for config in *config-${ip}.yaml; do
        if [ -f "$config" ]; then
            found_configs+=("$config")
        fi
    done
    
    if [ ${#found_configs[@]} -gt 0 ]; then
        # 按文件名排序（带编号的会排在前面）
        IFS=$'\n' found_configs=($(sort <<<"${found_configs[*]}"))
        CONFIG_FILE="${found_configs[0]}"
        log_info "[$ip] 找到配置文件: $CONFIG_FILE"
    elif [ -f "$default_config" ]; then
        CONFIG_FILE="$default_config"
    else
        CONFIG_FILE="$default_config"  # 即使不存在也设置，用于错误提示
    fi
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

# 清除持久化数据（针对单个 IP）
reset_counters_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "清除机器人检测和下载计数的持久化数据..."
    
    local counters_file="$REMOTE_DIR/downloads/.global/download_counters.json"
    
    # 检查文件是否存在
    if remote_exec "test -f $counters_file"; then
        # 备份原文件（可选）
        local backup_file="${counters_file}.backup.$(date +%Y%m%d_%H%M%S)"
        remote_exec "cp $counters_file $backup_file 2>/dev/null || true"
        log_ip "$ip" "已备份原文件: $backup_file"
        
        # 重置为初始值（使用远程服务器的当前时间）
        remote_exec "python3 -c \"
import json
from datetime import datetime
data = {
    'date': datetime.now().strftime('%Y-%m-%d %H:%M:%S'),
    'count': 0,
    'rest_until': '',
    'rest_duration': 0,
    'bot_detection_count': 0,
    'bot_detection_rest_start': ''
}
with open('$counters_file', 'w') as f:
    json.dump(data, f, indent=2)
\""
        
        if [ $? -eq 0 ]; then
            log_ip "$ip" "✓ 持久化数据已清除（下载计数、机器人检测计数、休息时间已重置）"
        else
            log_error "[$ip] 清除持久化数据失败"
            return 1
        fi
    else
        log_warn "[$ip] 持久化数据文件不存在: $counters_file，可能从未运行过下载服务"
        # 创建初始文件
        remote_exec "mkdir -p $REMOTE_DIR/downloads/.global" || return 1
        remote_exec "python3 -c \"
import json
from datetime import datetime
data = {
    'date': datetime.now().strftime('%Y-%m-%d %H:%M:%S'),
    'count': 0,
    'rest_until': '',
    'rest_duration': 0,
    'bot_detection_count': 0,
    'bot_detection_rest_start': ''
}
with open('$counters_file', 'w') as f:
    json.dump(data, f, indent=2)
\""
        if [ $? -eq 0 ]; then
            log_ip "$ip" "✓ 已创建初始持久化数据文件"
        else
            log_error "[$ip] 创建持久化数据文件失败"
            return 1
        fi
    fi
    
    return 0
}

# 收集网卡流量信息（针对单个 IP）
collect_network_stats_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "收集网卡流量信息..."
    
    # 使用 /proc/net/dev 获取网卡流量统计
    # 格式：interface | receive_bytes | receive_packets | ... | transmit_bytes | transmit_packets | ...
    local network_stats=$(remote_exec "cat /proc/net/dev 2>/dev/null" 2>&1)
    
    if [ -z "$network_stats" ]; then
        log_warn "[$ip] 无法获取网卡流量信息"
        echo "=== 服务器: $ip ===" >> "$NETWORK_STATS_DETAIL_FILE"
        echo "错误: 无法获取网卡流量信息" >> "$NETWORK_STATS_DETAIL_FILE"
        echo "" >> "$NETWORK_STATS_DETAIL_FILE"
        return 1
    fi
    
    # 保存原始输出到详细文件
    echo "=== 服务器: $ip ===" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "收集时间: $(date '+%Y-%m-%d %H:%M:%S')" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "原始输出 (/proc/net/dev):" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "$network_stats" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "" >> "$NETWORK_STATS_DETAIL_FILE"
    
    # 解析并计算总流量
    local total_rx_bytes=0
    local total_tx_bytes=0
    local interface_count=0
    
    while IFS= read -r line; do
        # 跳过标题行和空行
        if [[ "$line" =~ ^[[:space:]]*Inter- ]] || [[ "$line" =~ ^[[:space:]]*face ]] || [[ -z "$line" ]]; then
            continue
        fi
        
        # 解析行：interface | rx_bytes rx_packets ... | tx_bytes tx_packets ...
        # /proc/net/dev 格式：interface: rx_bytes rx_packets rx_errs rx_drop ... tx_bytes tx_packets ...
        if [[ "$line" =~ ^[[:space:]]*([^:]+):[[:space:]]+([0-9]+)[[:space:]]+[0-9]+[[:space:]]+[0-9]+[[:space:]]+[0-9]+[[:space:]]+[0-9]+[[:space:]]+[0-9]+[[:space:]]+[0-9]+[[:space:]]+([0-9]+) ]]; then
            local interface="${BASH_REMATCH[1]}"
            local rx_bytes="${BASH_REMATCH[2]}"
            local tx_bytes="${BASH_REMATCH[3]}"
            
            # 跳过 lo (loopback) 接口
            if [[ "$interface" == "lo" ]]; then
                continue
            fi
            
            total_rx_bytes=$((total_rx_bytes + rx_bytes))
            total_tx_bytes=$((total_tx_bytes + tx_bytes))
            ((interface_count++))
        fi
    done <<< "$network_stats"
    
    # 格式化输出
    local rx_gb=$(echo "scale=2; $total_rx_bytes / 1073741824" | bc 2>/dev/null || echo "0")
    local tx_gb=$(echo "scale=2; $total_tx_bytes / 1073741824" | bc 2>/dev/null || echo "0")
    local rx_tb=$(echo "scale=2; $total_rx_bytes / 1099511627776" | bc 2>/dev/null || echo "0")
    local tx_tb=$(echo "scale=2; $total_tx_bytes / 1099511627776" | bc 2>/dev/null || echo "0")
    
    echo "统计信息:" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "  网卡数量: $interface_count" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "  总接收流量: $(printf "%'d" $total_rx_bytes) 字节 ($rx_gb GB / $rx_tb TB)" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "  总发送流量: $(printf "%'d" $total_tx_bytes) 字节 ($tx_gb GB / $tx_tb TB)" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "  总流量: $(printf "%'d" $((total_rx_bytes + total_tx_bytes))) 字节 ($(echo "scale=2; ($total_rx_bytes + $total_tx_bytes) / 1073741824" | bc 2>/dev/null || echo "0") GB)" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "========================================" >> "$NETWORK_STATS_DETAIL_FILE"
    echo "" >> "$NETWORK_STATS_DETAIL_FILE"
    
    # 返回统计信息（用于汇总）- 输出到 stdout
    echo "$ip|$total_rx_bytes|$total_tx_bytes|$interface_count"
    
    log_ip "$ip" "✓ 网卡流量信息收集完成" >&2
    return 0
}

# 生成流量汇总统计
generate_network_stats_summary() {
    log_info "生成流量汇总统计..."
    
    local date_str=$(date +%Y%m%d)
    local detail_file="$PROJECT_DIR/output/1.network_stats_detail_${date_str}.txt"
    local summary_file="$PROJECT_DIR/output/1.network_stats_summary_${date_str}.txt"
    
    if [ ! -f "$detail_file" ]; then
        log_error "详细统计文件不存在: $detail_file"
        return 1
    fi
    
    # 使用 Python 解析详细文件，提取统计信息
    local stats_data=$(python3 <<EOF
import json
import re
import sys

detail_file = "$detail_file"
try:
    total_rx_bytes = 0
    total_tx_bytes = 0
    total_interfaces = 0
    server_count = 0
    
    with open(detail_file, 'r', encoding='utf-8') as f:
        content = f.read()
    
    # 查找所有 "总接收流量: X 字节" 模式
    rx_pattern = r'总接收流量:\s+([0-9,]+)\s+字节'
    tx_pattern = r'总发送流量:\s+([0-9,]+)\s+字节'
    interface_pattern = r'网卡数量:\s+([0-9]+)'
    server_pattern = r'^===.*服务器:'
    
    # 统计服务器数量
    server_matches = re.findall(server_pattern, content, re.MULTILINE)
    server_count = len(server_matches)
    
    # 提取所有接收流量
    rx_matches = re.findall(rx_pattern, content)
    for rx_str in rx_matches:
        rx_bytes = int(rx_str.replace(',', ''))
        total_rx_bytes += rx_bytes
    
    # 提取所有发送流量
    tx_matches = re.findall(tx_pattern, content)
    for tx_str in tx_matches:
        tx_bytes = int(tx_str.replace(',', ''))
        total_tx_bytes += tx_bytes
    
    # 提取所有网卡数量
    interface_matches = re.findall(interface_pattern, content)
    for if_count in interface_matches:
        total_interfaces += int(if_count)
    
    # 输出 JSON 格式
    result = {
        "server_count": server_count,
        "total_rx_bytes": total_rx_bytes,
        "total_tx_bytes": total_tx_bytes,
        "total_interfaces": total_interfaces
    }
    print(json.dumps(result))
    
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
EOF
    )
    
    if [ $? -ne 0 ] || [ -z "$stats_data" ]; then
        log_error "解析详细统计文件失败"
        return 1
    fi
    
    # 解析 JSON 数据
    local server_count=$(echo "$stats_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['server_count'])" 2>/dev/null)
    local total_rx_bytes=$(echo "$stats_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['total_rx_bytes'])" 2>/dev/null)
    local total_tx_bytes=$(echo "$stats_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['total_tx_bytes'])" 2>/dev/null)
    local total_interfaces=$(echo "$stats_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['total_interfaces'])" 2>/dev/null)
    
    if [ -z "$server_count" ] || [ "$server_count" -eq 0 ]; then
        log_warn "未找到服务器统计信息"
        return 1
    fi
    
    # 格式化汇总信息
    local total_rx_gb=$(echo "scale=2; $total_rx_bytes / 1073741824" | bc 2>/dev/null || echo "0")
    local total_tx_gb=$(echo "scale=2; $total_tx_bytes / 1073741824" | bc 2>/dev/null || echo "0")
    local total_rx_tb=$(echo "scale=2; $total_rx_bytes / 1099511627776" | bc 2>/dev/null || echo "0")
    local total_tx_tb=$(echo "scale=2; $total_tx_bytes / 1099511627776" | bc 2>/dev/null || echo "0")
    local total_bytes=$((total_rx_bytes + total_tx_bytes))
    local total_gb=$(echo "scale=2; $total_bytes / 1073741824" | bc 2>/dev/null || echo "0")
    local total_tb=$(echo "scale=2; $total_bytes / 1099511627776" | bc 2>/dev/null || echo "0")
    
    # 生成汇总文件
    local summary=$(cat <<EOF
========================================
网卡流量统计汇总
========================================
收集时间: $(date '+%Y-%m-%d %H:%M:%S')
服务器数量: $server_count
总网卡数量: $total_interfaces

总下行流量（接收）: $(printf "%'d" $total_rx_bytes) 字节 ($total_rx_gb GB / $total_rx_tb TB)
总上行流量（发送）: $(printf "%'d" $total_tx_bytes) 字节 ($total_tx_gb GB / $total_tx_tb TB)
总流量: $(printf "%'d" $total_bytes) 字节 ($total_gb GB / $total_tb TB)

EOF
    )
    
    # 写入汇总文件
    echo "$summary" > "$summary_file"
    log_info "网卡流量汇总统计已保存到: $summary_file"
    log_info "汇总: $server_count 台服务器，总下行流量 $total_rx_gb GB，总上行流量 $total_tx_gb GB，总流量 $total_gb GB"
    
    return 0
}

# 整理 output 目录
organize_output_directory() {
    log_info "整理 output 目录..."
    
    local date_str=$(date +%Y%m%d)
    local output_archive_dir="$PROJECT_DIR/output-${date_str}"
    local subtitles_dir="$output_archive_dir/subtitles"
    local downloads_dir="$PROJECT_DIR/downloads"
    
    # 创建归档目录
    mkdir -p "$subtitles_dir"
    
    # 移动所有流量统计相关文件
    if [ -d "$PROJECT_DIR/output" ]; then
        local moved_stats=0
        for file in "$PROJECT_DIR/output"/1.network_stats*.txt; do
            if [ -f "$file" ]; then
                local filename=$(basename "$file")
                mv "$file" "$output_archive_dir/$filename" 2>/dev/null || true
                log_info "已移动流量统计文件: $filename"
                ((moved_stats++))
            fi
        done
        
        if [ $moved_stats -eq 0 ]; then
            log_warn "未找到流量统计文件"
        fi
    fi
    
    # 检查 downloads 目录是否存在
    if [ ! -d "$downloads_dir" ]; then
        log_warn "downloads 目录不存在: $downloads_dir，跳过字幕整理"
        log_info "output 目录整理完成，归档目录: $output_archive_dir"
        return 0
    fi
    
    # 遍历 downloads 下的所有 channel 目录
    local processed_count=0
    local skipped_count=0
    local copied_count=0
    
    for channel_dir in "$downloads_dir"/*/; do
        if [ ! -d "$channel_dir" ]; then
            continue
        fi
        
        local channel_id=$(basename "$channel_dir")
        log_info "处理频道: $channel_id"
        
        # 遍历频道下的所有视频目录
        for video_dir in "$channel_dir"*/; do
            if [ ! -d "$video_dir" ]; then
                continue
            fi
            
            # 检查是否已经 organize 过
            local organize_marker="$video_dir/.organized"
            if [ -f "$organize_marker" ]; then
                ((skipped_count++))
                continue
            fi
            
            # 检查上传状态
            local upload_status_file="$video_dir/upload_status.json"
            if [ ! -f "$upload_status_file" ]; then
                continue
            fi
            
            # 使用 Python 检查上传状态
            local is_uploaded=$(python3 <<EOF
import json
import sys

try:
    with open("$upload_status_file", 'r', encoding='utf-8') as f:
        status = json.load(f)
    
    # 检查 status == "completed" 或 uploaded == True
    if status.get('status') == 'completed' or status.get('uploaded') is True:
        print('true')
    else:
        print('false')
except Exception as e:
    print('false', file=sys.stderr)
    sys.exit(1)
EOF
            )
            
            if [ "$is_uploaded" != "true" ]; then
                continue
            fi
            
            # 获取视频信息
            local video_info_file="$video_dir/video_info.json"
            if [ ! -f "$video_info_file" ]; then
                continue
            fi
            
            # 使用 Python 提取 video_id, title, aid
            local video_data=$(python3 <<EOF
import json
import sys

try:
    # 读取 video_info.json
    with open("$video_info_file", 'r', encoding='utf-8') as f:
        video_info = json.load(f)
    
    video_id = video_info.get('id', '')
    title = video_info.get('title', '')
    
    # 读取 upload_status.json 获取 aid
    with open("$upload_status_file", 'r', encoding='utf-8') as f:
        upload_status = json.load(f)
    
    aid = upload_status.get('bilibili_aid', '')
    
    # 清理标题（移除特殊字符，用于文件名）
    import re
    sanitized_title = re.sub(r'[<>:"/\\|?*]', '_', title)
    sanitized_title = sanitized_title.strip(' .')
    
    result = {
        'video_id': video_id,
        'title': title,
        'sanitized_title': sanitized_title,
        'aid': str(aid) if aid else ''
    }
    
    print(json.dumps(result))
except Exception as e:
    print(f'ERROR: {e}', file=sys.stderr)
    sys.exit(1)
EOF
            )
            
            if [ $? -ne 0 ] || [ -z "$video_data" ]; then
                log_warn "无法读取视频信息: $video_dir"
                continue
            fi
            
            local video_id=$(echo "$video_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['video_id'])" 2>/dev/null)
            local title=$(echo "$video_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['title'])" 2>/dev/null)
            local sanitized_title=$(echo "$video_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['sanitized_title'])" 2>/dev/null)
            local aid=$(echo "$video_data" | python3 -c "import sys, json; print(json.load(sys.stdin)['aid'])" 2>/dev/null)
            
            if [ -z "$video_id" ] || [ -z "$title" ]; then
                log_warn "视频信息不完整: $video_dir"
                continue
            fi
            
            # 查找字幕文件
            local subtitle_files=$(find "$video_dir" -maxdepth 1 -name "*.srt" -o -name "*.vtt" 2>/dev/null)
            
            if [ -z "$subtitle_files" ]; then
                # 没有字幕文件，标记为已处理
                touch "$organize_marker"
                ((processed_count++))
                continue
            fi
            
            # 使用 Python 处理字幕文件（避免使用关联数组，兼容性更好）
            # 通过环境变量传递参数，避免 heredoc 参数传递问题
            local python_result=$(PYTHON_VIDEO_DIR="$video_dir" \
                PYTHON_SUBTITLES_DIR="$subtitles_dir" \
                PYTHON_SANITIZED_TITLE="$sanitized_title" \
                PYTHON_VIDEO_ID="$video_id" \
                PYTHON_AID="$aid" \
                python3 <<'PYTHON_SCRIPT'
import json
import os
import re
import shutil
import sys

# 从环境变量读取参数
video_dir = os.environ.get('PYTHON_VIDEO_DIR', '')
subtitles_dir = os.environ.get('PYTHON_SUBTITLES_DIR', '')
sanitized_title = os.environ.get('PYTHON_SANITIZED_TITLE', '')
video_id = os.environ.get('PYTHON_VIDEO_ID', '')
aid = os.environ.get('PYTHON_AID', '')

# 验证参数
if not video_dir or not subtitles_dir or not video_id:
    print("ERROR: 必需参数为空", file=sys.stderr)
    print(f"ERROR: video_dir={video_dir}, subtitles_dir={subtitles_dir}, video_id={video_id}", file=sys.stderr)
    sys.exit(1)

# 收集所有字幕文件
subtitle_files = []
try:
    for f in os.listdir(video_dir):
        if f.endswith('.srt') or f.endswith('.vtt'):
            subtitle_files.append(f)
except Exception as e:
    print(f"ERROR: 读取目录失败: {e}", file=sys.stderr)
    sys.exit(1)

# 按语言组织字幕
subtitle_by_lang = {}  # lang -> {'new': file, 'old': file}

for subtitle_file in subtitle_files:
    # 检查是否是新格式：title[video_id].lang.ext
    # 转义 video_id 中的特殊字符
    escaped_video_id = re.escape(video_id)
    new_format_pattern = r'.*\[{}\]\.([a-zA-Z-]+)\.(srt|vtt)$'.format(escaped_video_id)
    match = re.match(new_format_pattern, subtitle_file)
    if match:
        lang = match.group(1)
        if lang not in subtitle_by_lang:
            subtitle_by_lang[lang] = {}
        subtitle_by_lang[lang]['new'] = subtitle_file
        continue
    
    # 检查是否是旧格式：aid_lang.ext
    if aid:
        escaped_aid = re.escape(aid)
        old_format_pattern = r'^{}_([a-zA-Z-]+)\.(srt|vtt)$'.format(escaped_aid)
        match = re.match(old_format_pattern, subtitle_file)
        if match:
            lang = match.group(1)
            if lang not in subtitle_by_lang:
                subtitle_by_lang[lang] = {}
            subtitle_by_lang[lang]['old'] = subtitle_file

# 处理每个语言的字幕
copied_count = 0
for lang, files in subtitle_by_lang.items():
    subtitle_file = None
    subtitle_ext = 'srt'
    
    # 优先使用新格式
    if 'new' in files:
        subtitle_file = files['new']
        subtitle_ext = os.path.splitext(subtitle_file)[1][1:]  # 去掉点
    # 如果新格式不存在，使用旧格式并创建新格式副本
    elif 'old' in files:
        subtitle_file = files['old']
        subtitle_ext = os.path.splitext(subtitle_file)[1][1:]  # 去掉点
        
        # 创建新格式的副本
        new_format_subtitle = "{sanitized_title}[{video_id}].{lang}.{ext}".format(
            sanitized_title=sanitized_title,
            video_id=video_id,
            lang=lang,
            ext=subtitle_ext
        )
        new_format_path = os.path.join(video_dir, new_format_subtitle)
        old_format_path = os.path.join(video_dir, subtitle_file)
        
        if not os.path.exists(new_format_path):
            try:
                shutil.copy2(old_format_path, new_format_path)
                print("已创建新格式字幕: {}".format(new_format_subtitle), file=sys.stderr)
            except Exception as e:
                print("创建新格式字幕失败: {}".format(e), file=sys.stderr)
    else:
        continue
    
    # 复制到 subtitles 目录（使用新格式命名）
    if subtitle_file:
        dest_subtitle = "{sanitized_title}[{video_id}].{lang}.{ext}".format(
            sanitized_title=sanitized_title,
            video_id=video_id,
            lang=lang,
            ext=subtitle_ext
        )
        dest_path = os.path.join(subtitles_dir, dest_subtitle)
        src_path = os.path.join(video_dir, subtitle_file)
        
        try:
            shutil.copy2(src_path, dest_path)
            copied_count += 1
        except Exception as e:
            print("复制字幕失败: {}".format(e), file=sys.stderr)

# 输出复制的数量
print("COPIED:{}".format(copied_count))
PYTHON_SCRIPT
            )
            
            local python_exit_code=$?
            
            # 检查是否有错误，如果有错误直接退出
            if [ $python_exit_code -ne 0 ] || echo "$python_result" | grep -q "^ERROR:"; then
                log_error "处理字幕文件失败: $python_result"
                exit 1
            fi
            
            # 提取复制的数量
            local python_copied=$(echo "$python_result" | grep "^COPIED:" | sed 's/^COPIED://')
            if [ -n "$python_copied" ] && [[ "$python_copied" =~ ^[0-9]+$ ]]; then
                copied_count=$((copied_count + python_copied))
            fi
            
            # 标记为已处理
            touch "$organize_marker"
            ((processed_count++))
        done
    done
    
    log_info "字幕整理完成: 处理 $processed_count 个视频，跳过 $skipped_count 个已处理视频，复制 $copied_count 个字幕文件"
    log_info "output 目录整理完成，归档目录: $output_archive_dir"
}

# 同步 output 目录（针对单个 IP）
sync_output_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "同步远程 output 目录到本地..."
    
    local remote_output_dir="$REMOTE_DIR/output"
    local local_output_dir="$PROJECT_DIR/output"
    
    # 创建本地目录（如果不存在）
    mkdir -p "$local_output_dir"
    
    # 检查远程目录是否存在
    if ! remote_exec "test -d $remote_output_dir"; then
        log_warn "[$ip] 远程 output 目录不存在: $remote_output_dir，跳过同步"
        return 0
    fi
    
    log_ip "$ip" "从 $REMOTE_HOST:$remote_output_dir/ 同步到 $local_output_dir/"
    
    # 使用 rsync 同步（从远程到本地）
    # 添加 --partial 和 --inplace 选项来处理部分传输和特殊字符
    # 注意：macOS 的 rsync 2.6.9 不支持 --protect-args，所以不使用该选项
    # 使用临时日志文件记录详细错误信息
    local rsync_log="/tmp/rsync_output_${ip}_$$.log"
    local rsync_exit_code=0
    
    rsync -azP --partial --inplace \
        -e "ssh $SSH_OPTS" \
        "$REMOTE_HOST:$remote_output_dir/" "$local_output_dir/" 2>&1 | tee "$rsync_log" || rsync_exit_code=${PIPESTATUS[0]}
    
    # 检查退出码
    if [ $rsync_exit_code -eq 0 ]; then
        log_ip "$ip" "✓ output 目录同步完成"
        rm -f "$rsync_log"
        return 0
    elif [ $rsync_exit_code -eq 23 ]; then
        # 错误代码 23：部分文件传输失败
        log_warn "[$ip] output 目录部分文件同步失败（错误代码 23），检查详细日志"
        
        # 提取失败的文件信息
        if [ -f "$rsync_log" ]; then
            local failed_files=$(grep -iE "error|failed|cannot|skipping" "$rsync_log" | head -20)
            if [ -n "$failed_files" ]; then
                log_warn "[$ip] 同步失败的文件:"
                echo "$failed_files" | while read -r line; do
                    log_warn "[$ip]   $line"
                done
            fi
        fi
        
        # 检查是否有文件成功传输
        if [ -d "$local_output_dir" ] && [ "$(ls -A $local_output_dir 2>/dev/null)" ]; then
            local synced_count=$(find "$local_output_dir" -type f 2>/dev/null | wc -l | tr -d ' ')
            log_ip "$ip" "部分文件已成功同步（约 $synced_count 个文件），继续..."
            rm -f "$rsync_log"
            return 0
        else
            log_error "[$ip] 没有文件成功同步"
            rm -f "$rsync_log"
            return 1
        fi
    else
        log_error "[$ip] output 目录同步失败（错误代码: $rsync_exit_code）"
        if [ -f "$rsync_log" ]; then
            log_error "[$ip] 错误详情:"
            tail -20 "$rsync_log" | while read -r line; do
                log_error "[$ip]   $line"
            done
        fi
        rm -f "$rsync_log"
        return 1
    fi
}

# 同步 downloads 目录下的元数据文件（针对单个 IP）
sync_meta_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "同步远程 downloads 目录下的元数据文件到本地..."
    
    local remote_downloads_dir="$REMOTE_DIR/downloads"
    local local_downloads_dir="$PROJECT_DIR/downloads"
    
    # 创建本地目录（如果不存在）
    mkdir -p "$local_downloads_dir"
    
    # 检查远程目录是否存在
    if ! remote_exec "test -d $remote_downloads_dir"; then
        log_warn "[$ip] 远程 downloads 目录不存在: $remote_downloads_dir，跳过同步"
        return 0
    fi
    
    log_ip "$ip" "从 $REMOTE_HOST:$remote_downloads_dir/ 同步元数据文件到 $local_downloads_dir/"
    
    # 使用 rsync 同步，只包含 .description, .json, .srt 文件
    # --include 和 --exclude 的顺序很重要，先 include 再 exclude
    # 使用 --prune-empty-dirs 删除空目录
    # 添加 --partial 和 --inplace 选项来处理部分传输和特殊字符
    # 注意：macOS 的 rsync 2.6.9 不支持 --protect-args，所以不使用该选项
    local rsync_log="/tmp/rsync_meta_${ip}_$$.log"
    local rsync_exit_code=0
    
    rsync -azP --partial --inplace \
        -e "ssh $SSH_OPTS" \
        --include="*/" \
        --include="*.description" \
        --include="*.json" \
        --include="*.srt" \
        --exclude="*" \
        --prune-empty-dirs \
        "$REMOTE_HOST:$remote_downloads_dir/" "$local_downloads_dir/" 2>&1 | tee "$rsync_log" || rsync_exit_code=${PIPESTATUS[0]}
    
    # 检查退出码
    if [ $rsync_exit_code -eq 0 ]; then
        log_ip "$ip" "✓ downloads 元数据文件同步完成"
        rm -f "$rsync_log"
        return 0
    elif [ $rsync_exit_code -eq 23 ]; then
        # 错误代码 23：部分文件传输失败
        log_warn "[$ip] downloads 元数据文件部分同步失败（错误代码 23），检查详细日志"
        
        # 提取失败的文件信息
        if [ -f "$rsync_log" ]; then
            local failed_files=$(grep -iE "error|failed|cannot|skipping" "$rsync_log" | head -20)
            if [ -n "$failed_files" ]; then
                log_warn "[$ip] 同步失败的文件:"
                echo "$failed_files" | while read -r line; do
                    log_warn "[$ip]   $line"
                done
            fi
        fi
        
        # 检查是否有文件成功传输
        if [ -d "$local_downloads_dir" ] && [ "$(ls -A $local_downloads_dir 2>/dev/null)" ]; then
            local synced_count=$(find "$local_downloads_dir" -type f \( -name "*.description" -o -name "*.json" -o -name "*.srt" \) 2>/dev/null | wc -l | tr -d ' ')
            log_ip "$ip" "部分文件已成功同步（约 $synced_count 个文件），继续..."
            rm -f "$rsync_log"
            return 0
        else
            log_error "[$ip] 没有文件成功同步"
            rm -f "$rsync_log"
            return 1
        fi
    else
        log_error "[$ip] downloads 元数据文件同步失败（错误代码: $rsync_exit_code）"
        if [ -f "$rsync_log" ]; then
            log_error "[$ip] 错误详情:"
            tail -20 "$rsync_log" | while read -r line; do
                log_error "[$ip]   $line"
            done
        fi
        rm -f "$rsync_log"
        return 1
    fi
}

# 在远程服务器上执行 subtitle 命令（针对单个 IP）
subtitle_for_ip() {
    local ip=$1
    setup_ssh_for_ip "$ip"
    
    log_ip "$ip" "检查 subtitle 命令是否已在执行..."
    
    # 检查是否已经有 subtitle 命令在执行
    # 检查 /opt/blueberry/blueberry 进程，且命令行参数包含 "subtitle"
    if remote_exec "ps aux | grep '$REMOTE_DIR/blueberry' | grep 'subtitle' | grep -v grep > /dev/null 2>&1"; then
        log_ip "$ip" "subtitle 命令已在执行，跳过"
        return 0
    fi
    
    log_ip "$ip" "在后台执行 subtitle 命令..."
    
    # 使用 nohup 在后台执行，输出重定向到日志文件
    local log_file="$REMOTE_DIR/subtitle.log"
    local error_log_file="$REMOTE_DIR/subtitle.error.log"
    
    # 在后台执行，不等待返回
    remote_exec "cd $REMOTE_DIR && nohup $REMOTE_DIR/blueberry subtitle > $log_file 2> $error_log_file < /dev/null &" || {
        log_error "[$ip] 启动 subtitle 命令失败"
        return 1
    }
    
    # 等待一小段时间，确认进程已启动
    sleep 1
    
    # 再次检查进程是否已启动
    if remote_exec "ps aux | grep '$REMOTE_DIR/blueberry' | grep 'subtitle' | grep -v grep > /dev/null 2>&1"; then
        log_ip "$ip" "✓ subtitle 命令已在后台启动（日志: $log_file, 错误日志: $error_log_file）"
        return 0
    else
        log_error "[$ip] subtitle 命令启动失败，请检查日志: $error_log_file"
        return 1
    fi
}

# 处理所有 IP 的主函数
process_all_ips() {
    local action=$1
    local success_count=0
    local fail_count=0
    local failed_ips=()
    
    # 对于 sync 操作，初始化网卡流量统计文件
    # 注意：这些变量需要是全局的，因为 collect_network_stats_for_ip 函数需要使用它们
    NETWORK_STATS_DETAIL_FILE=""
    NETWORK_STATS_SUMMARY_FILE=""
    NETWORK_STATS_TEMP=""
    if [ "$action" = "sync" ]; then
        local date_str=$(date +%Y%m%d)
        NETWORK_STATS_DETAIL_FILE="$PROJECT_DIR/output/1.network_stats_detail_${date_str}.txt"
        NETWORK_STATS_SUMMARY_FILE="$PROJECT_DIR/output/1.network_stats_summary_${date_str}.txt"
        NETWORK_STATS_TEMP=$(mktemp)
        mkdir -p "$PROJECT_DIR/output"
        # 创建详细文件并写入标题
        cat > "$NETWORK_STATS_DETAIL_FILE" <<EOF
========================================
网卡流量统计详细明细
========================================
收集时间: $(date '+%Y-%m-%d %H:%M:%S')
服务器数量: ${#IPS[@]}
服务器列表: ${IPS[*]}

EOF
        log_info "网卡流量详细统计将保存到: $NETWORK_STATS_DETAIL_FILE"
        log_info "网卡流量汇总统计将保存到: $NETWORK_STATS_SUMMARY_FILE"
    fi
    
    log_info "处理 ${#IPS[@]} 个服务器: ${IPS[*]}"
    echo ""
    
    # 对于 install 操作，先编译一次（所有服务器共享）
    if [ "$action" = "install" ]; then
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
                    return 1
                fi
                logs_service_for_ip "$ip"
                # logs 命令会持续运行，不会返回
                return 0
                ;;
            reset)
                if reset_counters_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            sync)
                # sync 操作包括：同步 output 目录 + 收集网卡流量详细信息
                local sync_success=true
                local stats_success=true
                
                # 同步 output 目录
                if ! sync_output_for_ip "$ip"; then
                    sync_success=false
                fi
                
                # 收集网卡流量详细信息（只收集详细信息，不生成汇总）
                if ! collect_network_stats_for_ip "$ip" >/dev/null 2>&1; then
                    stats_success=false
                    log_warn "[$ip] 收集网卡流量信息失败"
                fi
                
                if [ "$sync_success" = true ] && [ "$stats_success" = true ]; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            sync-meta)
                # sync-meta 操作：同步 downloads 目录下的元数据文件（.description, .json, .srt）
                if sync_meta_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            subtitle)
                # subtitle 操作：在远程服务器上后台执行 subtitle 命令
                if subtitle_for_ip "$ip"; then
                    ((success_count++))
                else
                    ((fail_count++))
                    failed_ips+=("$ip")
                fi
                ;;
            organize)
                # organize 操作：生成汇总统计 + 整理目录
                # 这个操作不需要 IP，在本地执行
                if [ ${#IPS[@]} -gt 0 ]; then
                    log_warn "organize 操作不需要 IP 参数，将忽略 IP 列表"
                fi
                
                # 生成流量汇总统计
                if generate_network_stats_summary; then
                    ((success_count++))
                else
                    ((fail_count++))
                fi
                
                # 整理 output 目录
                if organize_output_directory; then
                    ((success_count++))
                else
                    ((fail_count++))
                fi
                
                # organize 操作只执行一次，不需要循环
                break
                ;;
            *)
                log_error "未知操作: $action"
                exit 1
                ;;
        esac
        
        echo ""
    done
    
    # 对于 sync 操作，不需要额外处理（只收集详细信息）
    # 对于 organize 操作，已经在循环中处理，这里不需要额外操作
    
    # 输出总结
    echo "========================================"
    log_info "处理完成"
    echo "========================================"
    log_info "成功: $success_count 个服务器"
    if [ $fail_count -gt 0 ]; then
        log_error "失败: $fail_count 个服务器"
        log_error "失败的服务器: ${failed_ips[*]}"
        return 1
    fi
    return 0
}

# 执行操作（支持多个 ACTION）
log_info "将执行以下操作: ${ACTIONS[*]}"
echo ""

failed_actions=()
for action_index in "${!ACTIONS[@]}"; do
    ACTION="${ACTIONS[$action_index]}"
    action_num=$((action_index + 1))
    total_actions=${#ACTIONS[@]}
    
    echo "========================================"
    log_info "执行操作 [$action_num/$total_actions]: $ACTION"
    echo "========================================"
    echo ""
    
    # 对于 logs 命令，如果是在多个操作中，给出警告
    if [ "$ACTION" = "logs" ] && [ ${#ACTIONS[@]} -gt 1 ]; then
        log_warn "logs 命令在多个操作中可能不会按预期工作，建议单独执行"
    fi
    
    # 执行操作并捕获退出码
    if process_all_ips "$ACTION"; then
        log_info "操作 '$ACTION' 执行成功"
    else
        log_error "操作 '$ACTION' 执行失败"
        failed_actions+=("$ACTION")
    fi
    
    echo ""
done

# 输出最终总结
echo "========================================"
log_info "所有操作执行完成"
echo "========================================"
if [ ${#failed_actions[@]} -gt 0 ]; then
    log_error "失败的操作: ${failed_actions[*]}"
    exit 1
else
    log_info "所有操作执行成功"
    exit 0
fi
