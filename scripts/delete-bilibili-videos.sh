#!/bin/bash

# Bilibili 视频批量删除脚本
# 功能：
# 1. 从 cookies.txt 文件读取 cookies（Netscape 格式）
# 2. 翻页获取所有视频
# 3. 显示视频列表和数量，等待用户确认
# 4. 确认后批量删除所有视频

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
COOKIES_DIR="$PROJECT_DIR/cookies"

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

# 检查参数
if [ $# -lt 1 ]; then
    log_error "参数不足"
    echo "用法: $0 <cookies_file>"
    echo ""
    echo "参数说明:"
    echo "  cookies_file: Bilibili cookies 文件路径（Netscape 格式，例如: cookies/blbl_1.txt）"
    echo ""
    echo "示例:"
    echo "  $0 cookies/blbl_1.txt"
    exit 1
fi

COOKIES_FILE="$1"

# 检查 cookies 文件是否存在
if [ ! -f "$COOKIES_FILE" ]; then
    log_error "Cookies 文件不存在: $COOKIES_FILE"
    exit 1
fi

log_info "开始处理 cookies 文件: $COOKIES_FILE"

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

# 获取所有视频（翻页）
get_all_videos() {
    local cookies="$1"
    log_info "开始获取所有视频（翻页）..."
    
    # 提取 csrf token
    csrf_token=$(extract_csrf_token "$cookies")
    if [ -z "$csrf_token" ]; then
        log_error "无法从 cookies 中提取 csrf token (bili_jct)"
        return 1
    fi
    log_info "已提取 CSRF token: ${csrf_token:0:10}..."
    
    local page=1
    local page_size=20
    local all_videos=()
    local total=0
    
    while true; do
        log_debug "获取第 $page 页..."
        
        # Bilibili API: 获取视频列表
        # 使用正确的 API 端点: https://api.bilibili.tv/intl/videoup/web2/archives
        API_URL="https://api.bilibili.tv/intl/videoup/web2/archives?state=&pn=$page&ps=$page_size&lang_id=2&platform=web&lang=zh-Hant_HK&s_locale=zh-Hant_HK&timezone=GMT%2B08:00&csrf=$csrf_token"
        
        # 打印完整的 curl 命令
        if [ "$page" -eq 1 ]; then
            log_info "执行 curl 命令:"
            echo "curl --http1.1 -s -w '\n%{http_code}' -X GET \\"
            echo "  \"$API_URL\" \\"
            echo "  -H \"Cookie: $cookies\" \\"
            echo "  -H \"User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36\" \\"
            echo "  -H \"Referer: https://studio.bilibili.tv/\" \\"
            echo "  -H \"Origin: https://studio.bilibili.tv\""
            echo ""
        fi
        
        # 使用临时文件存储响应和 HTTP 头
        temp_response=$(mktemp)
        temp_headers=$(mktemp)
        
        # 执行 curl 并获取 HTTP 状态码和响应
        # 注意：curl 的 -w 参数会在响应体后追加状态码，所以需要分离
        # 使用 --http1.1 强制使用 HTTP/1.1，避免 HTTP/2 协议错误
        curl_output=$(curl --http1.1 -s -w "\nHTTP_STATUS_CODE:%{http_code}" -X GET \
            "$API_URL" \
            -H "Cookie: $cookies" \
            -H "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36" \
            -H "Referer: https://studio.bilibili.tv/" \
            -H "Origin: https://studio.bilibili.tv" \
            -D "$temp_headers" \
            -o "$temp_response" 2>&1)
        
        # 提取 HTTP 状态码（从 temp_response 文件读取，因为响应体可能很大）
        response_body=$(cat "$temp_response")
        http_status=$(echo "$curl_output" | grep "HTTP_STATUS_CODE:" | sed 's/HTTP_STATUS_CODE://' || echo "unknown")
        
        # 如果从 curl_output 中没找到，尝试从响应头中提取
        if [ "$http_status" = "unknown" ]; then
            http_status=$(grep -i "^HTTP/" "$temp_headers" | head -1 | awk '{print $2}' || echo "unknown")
        fi
        
        # 打印 HTTP 状态码、响应头和响应内容
        if [ "$page" -eq 1 ] || [ -z "$response_body" ]; then
            log_info "HTTP 返回码: ${http_status:-unknown}"
            log_info "HTTP 响应头:"
            cat "$temp_headers"
            echo ""
            log_info "API 响应内容:"
            if [ -n "$response_body" ]; then
                # 尝试格式化 JSON，如果失败则直接输出
                echo "$response_body" | python3 -m json.tool 2>/dev/null || echo "$response_body"
            else
                echo "(空响应)"
            fi
            echo ""
        fi
        
        # 清理临时文件
        rm -f "$temp_response" "$temp_headers"
        
        # 检查 HTTP 状态码
        if [ -z "$http_status" ] || [ "$http_status" != "200" ]; then
            log_error "HTTP 请求失败，状态码: ${http_status:-unknown}"
            log_error "可能的原因："
            log_error "  1. API 端点不正确"
            log_error "  2. Cookies 无效或过期"
            log_error "  3. 网络连接问题"
            log_error "  4. 需要认证或权限不足"
            break
        fi
        
        # 检查响应是否为空
        if [ -z "$response_body" ]; then
            log_error "API 返回空响应体"
            break
        fi
        
        # 检查响应
        # 注意：API 可能返回 code: 0 表示成功，或者 code: -101 表示未登录
        if echo "$response_body" | grep -q '"code":0'; then
            # 解析视频列表
            # API 响应格式: {"code":0,"data":{"archives":[...],"page":{"total":...}}}
            videos=$(echo "$response_body" | python3 -c "
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
            title = str(video.get('title', '')).replace('|', '_').replace('\n', ' ')
            # 尝试获取创建时间，可能字段名不同
            ctime = video.get('ctime', video.get('create_time', video.get('pubdate', 0)))
            if aid:
                print(f\"{aid}|{title}|{ctime}\")
        print(f\"TOTAL:{total}\", file=sys.stderr)
except Exception as e:
    print(f\"ERROR: {e}\", file=sys.stderr)
    sys.exit(1)
" 2>/dev/null)
            
            if [ -z "$videos" ]; then
                log_info "第 $page 页没有更多视频，停止翻页"
                break
            fi
            
            # 添加到总列表（只添加有效的视频行）
            while IFS= read -r line; do
                # 确保行格式正确（包含 | 分隔符）
                if [ -n "$line" ] && echo "$line" | grep -qE "^[0-9]+\|"; then
                    all_videos+=("$line")
                fi
            done <<< "$videos"
            
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
                current_count=${#all_videos[@]}
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
            
            log_error "API 响应内容:"
            echo "$response_body" | python3 -m json.tool 2>/dev/null || echo "$response_body"
            break
        fi
    done
    
    # 输出所有视频
    printf '%s\n' "${all_videos[@]}"
    echo "TOTAL_COUNT:${#all_videos[@]}" >&2
}

# 删除视频（使用 Python urllib 标准库处理 multipart/form-data）
delete_video() {
    local aid="$1"
    local cookies="$2"
    local csrf_token="$3"
    
    log_debug "删除视频: $aid"
    
    # 使用 Python urllib 标准库发送删除请求
    python3 <<EOF
import sys
import urllib.request
import urllib.parse
import json
import random
import string

aid = "$aid"
cookies_str = "$cookies"
csrf_token = "$csrf_token"

# API URL 和参数
base_url = "https://api.bilibili.tv/intl/videoup/web2/del"
params = {
    "lang_id": "2",
    "platform": "web",
    "lang": "zh-Hant_HK",
    "s_locale": "zh-Hant_HK",
    "timezone": "GMT+08:00",
    "csrf": csrf_token
}
api_url = f"{base_url}?{urllib.parse.urlencode(params)}"

# 解析 cookies 字符串
cookies_dict = {}
for item in cookies_str.split('; '):
    if '=' in item:
        name, value = item.split('=', 1)
        cookies_dict[name.strip()] = value.strip()

# 构建 Cookie header
cookie_header = '; '.join([f"{k}={v}" for k, v in cookies_dict.items()])

# 生成 multipart/form-data 边界
boundary_suffix = ''.join(random.choices(string.ascii_letters + string.digits, k=16))
boundary = f"----WebKitFormBoundary{boundary_suffix}"

# 构建 multipart/form-data 请求体
body_parts = []
body_parts.append(f"--{boundary}".encode('utf-8'))
body_parts.append(b'Content-Disposition: form-data; name="aid"')
body_parts.append(b'')
body_parts.append(aid.encode('utf-8'))
body_parts.append(f"--{boundary}--".encode('utf-8'))
body = b'\r\n'.join(body_parts)

# 构建请求
req = urllib.request.Request(api_url, data=body)
req.add_header('Content-Type', f'multipart/form-data; boundary={boundary}')
req.add_header('Cookie', cookie_header)
req.add_header('Accept', 'application/json, text/plain, */*')
req.add_header('User-Agent', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36')
req.add_header('Referer', 'https://studio.bilibili.tv/')
req.add_header('Origin', 'https://studio.bilibili.tv')

try:
    # 发送请求
    with urllib.request.urlopen(req, timeout=30) as response:
        status_code = response.getcode()
        response_data = response.read().decode('utf-8')
        
        if status_code == 200:
            try:
                data = json.loads(response_data)
                if data.get('code') == 0:
                    print("SUCCESS", file=sys.stderr)
                    sys.exit(0)
                else:
                    error_msg = data.get('message', 'Unknown error')
                    print(f"ERROR: {error_msg}", file=sys.stderr)
                    print(f"RESPONSE: {json.dumps(data, ensure_ascii=False)}", file=sys.stderr)
                    sys.exit(1)
            except json.JSONDecodeError:
                print(f"ERROR: Invalid JSON response", file=sys.stderr)
                print(f"RESPONSE: {response_data[:200]}", file=sys.stderr)
                sys.exit(1)
        else:
            print(f"ERROR: HTTP {status_code}", file=sys.stderr)
            print(f"RESPONSE: {response_data[:200]}", file=sys.stderr)
            sys.exit(1)
except urllib.error.HTTPError as e:
    response_data = e.read().decode('utf-8') if e.fp else ''
    print(f"ERROR: HTTP {e.code}", file=sys.stderr)
    print(f"RESPONSE: {response_data[:200]}", file=sys.stderr)
    sys.exit(1)
except Exception as e:
    print(f"ERROR: {str(e)}", file=sys.stderr)
    sys.exit(1)
EOF
    
    local exit_code=$?
    
    if [ $exit_code -eq 0 ]; then
        return 0
    else
        log_error "删除视频 $aid 失败"
        return 1
    fi
}

# 主流程
main() {
    # 1. 从 cookies 文件读取 cookies
    log_info "从 cookies 文件读取 cookies..."
    
    # 显示 cookies 信息（用于日志）
    show_cookies_info "$COOKIES_FILE"
    
    # 获取 cookies 字符串（只输出到 stdout，格式：name1=value1; name2=value2）
    COOKIES=$(extract_cookies_from_file "$COOKIES_FILE")
    
    if [ -z "$COOKIES" ]; then
        log_error "无法从 cookies 文件读取 cookies"
        exit 1
    fi
    
    log_info "Cookies 读取成功"
    
    # 2. 获取所有视频
    log_info "开始获取视频列表..."
    VIDEO_LIST=$(get_all_videos "$COOKIES" 2>&1)
    
    # 分离标准输出和错误输出
    VIDEO_LIST_STDOUT=$(echo "$VIDEO_LIST" | grep -v "^\[" || true)
    VIDEO_LIST_STDERR=$(echo "$VIDEO_LIST" | grep "^\[" || true)
    
    # 如果有错误输出，显示它
    if [ -n "$VIDEO_LIST_STDERR" ]; then
        echo "$VIDEO_LIST_STDERR"
    fi
    
    # 过滤出只包含 | 的行（视频数据格式：aid|title|ctime）
    VIDEO_LIST=$(echo "$VIDEO_LIST_STDOUT" | grep -E "^[0-9]+\|" || true)
    VIDEO_COUNT=$(echo "$VIDEO_LIST" | grep -c "|" 2>/dev/null || echo "0")
    # 确保 VIDEO_COUNT 是纯数字
    VIDEO_COUNT=$(echo "$VIDEO_COUNT" | tr -d '\n\r ' | grep -E '^[0-9]+$' || echo "0")
    
    if [ -z "$VIDEO_COUNT" ] || [ "$VIDEO_COUNT" = "0" ] || [ "$VIDEO_COUNT" -eq 0 ] 2>/dev/null; then
        log_warn "未找到任何视频"
        log_info "获取到的原始数据（前20行）:"
        echo "$VIDEO_LIST_STDOUT" | head -20
        exit 0
    fi
    
    log_info "找到 $VIDEO_COUNT 个视频"
    
    # 3. 显示视频列表（前10个作为预览）
    echo ""
    log_info "视频列表预览（前10个）:"
    echo "$VIDEO_LIST" | head -10 | while IFS='|' read -r aid title ctime; do
        if [ -n "$aid" ] && [ -n "$title" ]; then
            echo "  - [$aid] $title"
        fi
    done
    
    if [ "$VIDEO_COUNT" -gt 10 ] 2>/dev/null; then
        echo "  ... 还有 $((VIDEO_COUNT - 10)) 个视频"
    fi
    
    echo ""
    log_warn "准备删除 $VIDEO_COUNT 个视频"
    echo -n "确认删除？(输入 'yes' 确认): "
    read -r confirmation
    
    if [ "$confirmation" != "yes" ]; then
        log_info "已取消删除操作"
        exit 0
    fi
    
    # 4. 批量删除
    # 提取 csrf token（用于删除请求）
    CSRF_TOKEN=$(extract_csrf_token "$COOKIES")
    if [ -z "$CSRF_TOKEN" ]; then
        log_error "无法从 cookies 中提取 csrf token (bili_jct)"
        exit 1
    fi
    
    log_info "开始删除视频..."
    deleted=0
    failed=0
    
    echo "$VIDEO_LIST" | while IFS='|' read -r aid title ctime; do
        if [ -n "$aid" ]; then
            if delete_video "$aid" "$COOKIES" "$CSRF_TOKEN"; then
                deleted=$((deleted + 1))
                log_info "[$deleted/$VIDEO_COUNT] 已删除: $title"
            else
                failed=$((failed + 1))
            fi
            sleep 0.5  # 避免请求过快
        fi
    done
    
    echo ""
    log_info "删除完成！"
    log_info "成功: $deleted 个"
    if [ "$failed" -gt 0 ]; then
        log_warn "失败: $failed 个"
    fi
}

# 执行主流程
main

