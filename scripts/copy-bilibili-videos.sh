#!/bin/bash

# Bilibili 视频复制脚本
# 功能：
# 1. 从 cookies.txt 文件读取 cookies（Netscape 格式）
# 2. 获取所有视频的 aid 列表
# 3. 从 output 目录中找到与 aid 匹配的文件夹
# 4. 将匹配的文件夹复制到 dist 目录

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
COOKIES_DIR="$PROJECT_DIR/cookies"
OUTPUT_DIR="$PROJECT_DIR/output"
DIST_DIR="$PROJECT_DIR/dist"

# 颜色输出
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_debug() { echo -e "${BLUE}[DEBUG]${NC} $1"; }

# 显示用法（如果需要）
show_usage() {
    echo "用法: $0 [cookies_file] [output_dir] [dist_dir]"
    echo ""
    echo "参数说明:"
    echo "  cookies_file: Bilibili cookies 文件路径（Netscape 格式，可选）"
    echo "                如果不提供，将自动扫描 cookies/blbl_*.txt 文件并逐个处理"
    echo "  output_dir:   输出目录路径（默认: ./output）"
    echo "  dist_dir:     目标目录路径（默认: ./dist）"
    echo ""
    echo "示例:"
    echo "  $0                                    # 自动处理所有 cookies/blbl_*.txt 文件"
    echo "  $0 cookies/blbl_1.txt                # 处理指定的 cookies 文件"
    echo "  $0 cookies/blbl_1.txt ./output ./dist"
}

# 解析参数
COOKIES_FILE=""
OUTPUT_DIR="$OUTPUT_DIR"
DIST_DIR="$DIST_DIR"

# 解析参数（支持 --help）
if [ $# -ge 1 ]; then
    if [ "$1" = "--help" ] || [ "$1" = "-h" ]; then
        show_usage
        exit 0
    fi
    
    first_arg="$1"
    # 检查是否是 cookies 文件（以 cookies/ 开头或以 .txt 结尾，或者是存在的文件）
    if [[ "$first_arg" == cookies/* ]] || [[ "$first_arg" == *.txt ]] || [ -f "$first_arg" ]; then
        COOKIES_FILE="$first_arg"
        shift
        if [ $# -ge 1 ]; then
            OUTPUT_DIR="$1"
            shift
        fi
        if [ $# -ge 1 ]; then
            DIST_DIR="$1"
        fi
    else
        # 第一个参数可能是 output_dir
        OUTPUT_DIR="$first_arg"
        shift
        if [ $# -ge 1 ]; then
            DIST_DIR="$1"
        fi
    fi
fi

# 如果没有指定 cookies 文件，扫描 cookies/blbl_*.txt
COOKIES_FILES=()
if [ -z "$COOKIES_FILE" ]; then
    log_info "未指定 cookies 文件，扫描 cookies/blbl_*.txt 文件..."
    while IFS= read -r -d '' file; do
        if [ -f "$file" ]; then
            COOKIES_FILES+=("$file")
        fi
    done < <(find "$COOKIES_DIR" -maxdepth 1 -name "blbl_*.txt" -type f -print0 2>/dev/null | sort -z)
    
    if [ ${#COOKIES_FILES[@]} -eq 0 ]; then
        log_error "未找到任何 cookies/blbl_*.txt 文件"
        exit 1
    fi
    
    log_info "找到 ${#COOKIES_FILES[@]} 个 cookies 文件"
    for file in "${COOKIES_FILES[@]}"; do
        log_info "  - $file"
    done
else
    # 检查指定的 cookies 文件是否存在
    if [ ! -f "$COOKIES_FILE" ]; then
        log_error "Cookies 文件不存在: $COOKIES_FILE"
        exit 1
    fi
    COOKIES_FILES=("$COOKIES_FILE")
fi

# 检查 output 目录是否存在
if [ ! -d "$OUTPUT_DIR" ]; then
    log_error "Output 目录不存在: $OUTPUT_DIR"
    exit 1
fi

log_info "开始处理 cookies 文件: $COOKIES_FILE"
log_info "Output 目录: $OUTPUT_DIR"
log_info "Dist 目录: $DIST_DIR"

# 从 cookies.txt 文件读取 cookies（Netscape 格式）
# 返回格式：name1=value1; name2=value2（与 HTTP Cookie header 格式一致）
extract_cookies_from_file() {
    local cookies_file="$1"
    
    # 使用 Python 解析 Netscape 格式的 cookies 文件
    # 只输出 cookies 字符串到 stdout，格式与 HTTP Cookie header 一致
    python3 <<EOF 2>/dev/null
import sys

try:
    cookies_dict = {}
    
    with open("$cookies_file", 'r', encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            # 跳过注释和空行
            if not line or line.startswith('#'):
                continue
            
            # Netscape 格式: domain, flag, path, secure, expiration, name, value
            parts = line.split('\t')
            if len(parts) >= 7:
                name = parts[5]
                value = parts[6]
                if name and value:
                    cookies_dict[name] = value
    
    if not cookies_dict:
        sys.exit(1)
    
    # 输出 cookies 字符串到 stdout（格式：name1=value1; name2=value2）
    # 与 HTTP Cookie header 格式一致
    cookie_str = '; '.join([f"{k}={v}" for k, v in cookies_dict.items()])
    print(cookie_str)
    
except Exception as e:
    sys.exit(1)
EOF
}

# 显示 cookies 信息（用于日志）
show_cookies_info() {
    local cookies_file="$1"
    
    # 使用 Python 解析并显示关键 cookies 信息（输出到 stderr）
    python3 <<EOF 2>&1
import sys

try:
    cookies_dict = {}
    
    with open("$cookies_file", 'r', encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            # 跳过注释和空行
            if not line or line.startswith('#'):
                continue
            
            # Netscape 格式: domain, flag, path, secure, expiration, name, value
            parts = line.split('\t')
            if len(parts) >= 7:
                name = parts[5]
                value = parts[6]
                if name and value:
                    cookies_dict[name] = value
    
    # 输出关键 cookies 信息到 stderr（用于日志）
    important_cookies = ['SESSDATA', 'bili_jct', 'DedeUserID', 'DedeUserID__ckMd5']
    found_important = {k: cookies_dict.get(k, 'NOT_FOUND') for k in important_cookies}
    print(f"重要 cookies:", file=sys.stderr)
    for k, v in found_important.items():
        if v != 'NOT_FOUND':
            print(f"  {k}: {v[:20]}..." if len(v) > 20 else f"  {k}: {v}", file=sys.stderr)
        else:
            print(f"  {k}: NOT_FOUND", file=sys.stderr)
    
    if not cookies_dict:
        print("ERROR: 未找到任何 cookies", file=sys.stderr)
        sys.exit(1)
    
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
EOF
}

# 从 cookies 字符串中提取 csrf token (bili_jct)
extract_csrf_token() {
    local cookies="$1"
    # 从 cookies 字符串中提取 bili_jct 的值
    echo "$cookies" | python3 -c "
import sys
cookies_str = sys.stdin.read().strip()
cookies_dict = {}
for item in cookies_str.split('; '):
    if '=' in item:
        name, value = item.split('=', 1)
        cookies_dict[name.strip()] = value.strip()
# 优先使用 bili_jct，如果没有则尝试 csrf
csrf = cookies_dict.get('bili_jct') or cookies_dict.get('csrf', '')
print(csrf)
" 2>/dev/null
}

# 获取所有视频的 aid 列表（翻页）
get_all_video_aids() {
    local cookies="$1"
    log_info "开始获取所有视频的 aid 列表（翻页）..."
    
    # 提取 csrf token
    csrf_token=$(extract_csrf_token "$cookies")
    if [ -z "$csrf_token" ]; then
        log_error "无法从 cookies 中提取 csrf token (bili_jct)"
        return 1
    fi
    log_info "已提取 CSRF token: ${csrf_token:0:10}..."
    
    local page=1
    local page_size=20
    local all_aids=()
    local total=0
    
    while true; do
        log_debug "获取第 $page 页..."
        
        # Bilibili API: 获取视频列表
        # 使用正确的 API 端点: https://api.bilibili.tv/intl/videoup/web2/archives
        API_URL="https://api.bilibili.tv/intl/videoup/web2/archives?state=&pn=$page&ps=$page_size&lang_id=2&platform=web&lang=zh-Hant_HK&s_locale=zh-Hant_HK&timezone=GMT%2B08:00&csrf=$csrf_token"
        
        # 使用临时文件存储响应和 HTTP 头
        temp_response=$(mktemp)
        temp_headers=$(mktemp)
        
        # 执行 curl 并获取 HTTP 状态码和响应
        # 使用 --http1.1 强制使用 HTTP/1.1，避免 HTTP/2 协议错误
        curl_output=$(curl --http1.1 -s -w "\nHTTP_STATUS_CODE:%{http_code}" -X GET \
            "$API_URL" \
            -H "Cookie: $cookies" \
            -H "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36" \
            -H "Referer: https://studio.bilibili.tv/" \
            -H "Origin: https://studio.bilibili.tv" \
            -D "$temp_headers" \
            -o "$temp_response" 2>&1)
        
        # 提取 HTTP 状态码
        response_body=$(cat "$temp_response")
        http_status=$(echo "$curl_output" | grep "HTTP_STATUS_CODE:" | sed 's/HTTP_STATUS_CODE://' || echo "unknown")
        
        # 如果从 curl_output 中没找到，尝试从响应头中提取
        if [ "$http_status" = "unknown" ]; then
            http_status=$(grep -i "^HTTP/" "$temp_headers" | head -1 | awk '{print $2}' || echo "unknown")
        fi
        
        # 清理临时文件
        rm -f "$temp_response" "$temp_headers"
        
        # 检查 HTTP 状态码
        if [ -z "$http_status" ] || [ "$http_status" != "200" ]; then
            log_error "HTTP 请求失败，状态码: ${http_status:-unknown}"
            break
        fi
        
        # 检查响应是否为空
        if [ -z "$response_body" ]; then
            log_error "API 返回空响应体"
            break
        fi
        
        # 检查响应
        if echo "$response_body" | grep -q '"code":0'; then
            # 解析视频列表，只提取 aid
            aids=$(echo "$response_body" | python3 -c "
import json
import sys
try:
    data = json.load(sys.stdin)
    if data.get('code') == 0 and 'data' in data:
        data_obj = data['data']
        # 从 data.archives 数组中获取视频列表
        archives = data_obj.get('archives', [])
        # 从 data.page.total 获取总数
        page_info = data_obj.get('page', {})
        total = page_info.get('total', len(archives))
        
        for video in archives:
            # aid 是字符串格式
            aid = str(video.get('aid', ''))
            if aid:
                print(aid)
        print(f\"TOTAL:{total}\", file=sys.stderr)
except Exception as e:
    print(f\"ERROR: {e}\", file=sys.stderr)
    sys.exit(1)
" 2>/dev/null)
            
            if [ -z "$aids" ]; then
                log_info "第 $page 页没有更多视频，停止翻页"
                break
            fi
            
            # 添加到总列表（只添加有效的 aid）
            while IFS= read -r aid; do
                # 确保 aid 是纯数字
                if [ -n "$aid" ] && echo "$aid" | grep -qE '^[0-9]+$'; then
                    all_aids+=("$aid")
                fi
            done <<< "$aids"
            
            # 检查是否还有更多页
            total_from_api=$(echo "$response_body" | python3 -c "
import json
import sys
try:
    data = json.load(sys.stdin)
    if data.get('code') == 0 and 'data' in data:
        data_obj = data['data']
        # 从 data.page.total 获取总数
        page_info = data_obj.get('page', {})
        total = page_info.get('total', 0)
        print(int(total))
except:
    print(0)
" 2>/dev/null)
            
            # 确保 total_from_api 是纯数字
            total_from_api=$(echo "$total_from_api" | tr -d '\n\r ' | grep -E '^[0-9]+$' || echo "0")
            
            if [ -n "$total_from_api" ] && [ "$total_from_api" -gt 0 ] 2>/dev/null; then
                total=$total_from_api
                current_count=${#all_aids[@]}
                if [ "$current_count" -ge "$total" ] 2>/dev/null; then
                    log_info "已获取所有视频（共 $total 个）"
                    break
                fi
            fi
            
            page=$((page + 1))
            sleep 1  # 避免请求过快
        else
            # API 返回 code != 0，可能是未登录或其他错误
            log_error "获取视频列表失败（code != 0）"
            log_error "HTTP 状态码: ${http_status:-unknown}"
            
            # 尝试解析错误信息
            error_msg=$(echo "$response_body" | python3 -c "
import json
import sys
try:
    data = json.load(sys.stdin)
    code = data.get('code', 'unknown')
    message = data.get('message', 'No error message')
    print(f\"错误代码: {code}, 错误信息: {message}\")
except:
    pass
" 2>/dev/null)
            
            if [ -n "$error_msg" ]; then
                log_error "$error_msg"
            fi
            
            # 如果是未登录错误，提示用户
            if echo "$response_body" | grep -q '"code":-101'; then
                log_error "账户未登录，请检查 cookies 是否有效"
            fi
            
            break
        fi
    done
    
    # 输出所有 aid
    printf '%s\n' "${all_aids[@]}"
    echo "TOTAL_COUNT:${#all_aids[@]}" >&2
}

# 处理单个 cookies 文件
process_cookies_file() {
    local cookies_file="$1"
    
    echo ""
    log_info "=========================================="
    log_info "处理 cookies 文件: $cookies_file"
    log_info "=========================================="
    
    # 1. 从 cookies 文件读取 cookies
    log_info "从 cookies 文件读取 cookies..."
    
    # 显示 cookies 信息（用于日志）
    show_cookies_info "$cookies_file"
    
    # 获取 cookies 字符串（只输出到 stdout，格式：name1=value1; name2=value2）
    COOKIES=$(extract_cookies_from_file "$cookies_file")
    
    if [ -z "$COOKIES" ]; then
        log_error "无法从 cookies 文件读取 cookies: $cookies_file"
        return 1
    fi
    
    log_info "Cookies 读取成功"
    
    # 2. 获取所有视频的 aid 列表
    log_info "开始获取视频 aid 列表..."
    AID_LIST=$(get_all_video_aids "$COOKIES" 2>&1)
    
    # 分离标准输出和错误输出
    AID_LIST_STDOUT=$(echo "$AID_LIST" | grep -v "^\[" | grep -v "^TOTAL_COUNT:" || true)
    AID_LIST_STDERR=$(echo "$AID_LIST" | grep "^\[" || true)
    
    # 如果有错误输出，显示它
    if [ -n "$AID_LIST_STDERR" ]; then
        echo "$AID_LIST_STDERR"
    fi
    
    # 过滤出只包含数字的行（aid）
    AID_LIST=$(echo "$AID_LIST_STDOUT" | grep -E '^[0-9]+$' || true)
    AID_COUNT=$(echo "$AID_LIST" | grep -cE '^[0-9]+$' 2>/dev/null || echo "0")
    # 确保 AID_COUNT 是纯数字
    AID_COUNT=$(echo "$AID_COUNT" | tr -d '\n\r ' | grep -E '^[0-9]+$' || echo "0")
    
    if [ -z "$AID_COUNT" ] || [ "$AID_COUNT" = "0" ] || [ "$AID_COUNT" -eq 0 ] 2>/dev/null; then
        log_warn "未找到任何视频 aid"
        log_info "获取到的原始数据（前20行）:"
        echo "$AID_LIST_STDOUT" | head -20
        return 0
    fi
    
    log_info "找到 $AID_COUNT 个视频 aid"
    
    # 3. 创建 dist 目录（如果不存在）
    mkdir -p "$DIST_DIR"
    log_info "目标目录: $DIST_DIR"
    
    # 4. 在 output 目录中查找匹配的文件夹并复制
    log_info "开始在 output 目录中查找匹配的文件夹..."
    copied=0
    not_found=0
    
    while IFS= read -r aid; do
        if [ -z "$aid" ]; then
            continue
        fi
        
        # 在 output 目录中查找与 aid 匹配的文件夹
        source_dir="$OUTPUT_DIR/$aid"
        
        if [ -d "$source_dir" ]; then
            # 找到匹配的文件夹，复制到 dist 目录
            dest_dir="$DIST_DIR/$aid"
            
            if [ -d "$dest_dir" ]; then
                log_warn "目标文件夹已存在，跳过: $aid"
            else
                log_info "复制: $aid"
                if cp -r "$source_dir" "$dest_dir" 2>/dev/null; then
                    copied=$((copied + 1))
                else
                    log_error "复制失败: $aid"
                fi
            fi
        else
            not_found=$((not_found + 1))
            log_debug "未找到文件夹: $aid"
        fi
    done <<< "$AID_LIST"
    
    echo ""
    log_info "处理完成: $cookies_file"
    log_info "成功复制: $copied 个文件夹"
    if [ "$not_found" -gt 0 ]; then
        log_warn "未找到: $not_found 个文件夹（在 output 目录中不存在）"
    fi
    
    return 0
}

# 主流程
main() {
    # 创建 dist 目录（如果不存在）
    mkdir -p "$DIST_DIR"
    
    # 遍历所有 cookies 文件
    total_files=${#COOKIES_FILES[@]}
    current_file=0
    
    for cookies_file in "${COOKIES_FILES[@]}"; do
        current_file=$((current_file + 1))
        log_info "[$current_file/$total_files] 处理 cookies 文件: $cookies_file"
        
        if ! process_cookies_file "$cookies_file"; then
            log_error "处理 cookies 文件失败: $cookies_file，继续处理下一个"
            continue
        fi
        
        # 在处理多个文件时，添加分隔
        if [ $current_file -lt $total_files ]; then
            echo ""
            log_info "等待 2 秒后处理下一个文件..."
            sleep 2
        fi
    done
    
    echo ""
    log_info "=========================================="
    log_info "所有 cookies 文件处理完成！"
    log_info "=========================================="
}

# 执行主流程
main

