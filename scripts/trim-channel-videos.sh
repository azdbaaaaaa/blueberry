#!/bin/bash
# 切割频道下的视频文件夹
# 用法: ./trim-channel-videos.sh <ip> <channel_path> <limit> <offset>
# 示例: ./trim-channel-videos.sh 194.233.83.29 ./downloads/频道名 10 0
#       保留排序后的前10个视频文件夹，删除其他

# 不使用 set -e，因为删除操作可能部分失败，需要继续执行

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 检查参数
if [ $# -lt 4 ]; then
    log_error "参数不足"
    echo "用法: $0 <ip> <channel_path> <limit> <offset>"
    echo ""
    echo "参数说明:"
    echo "  ip:           服务器 IP 地址（必需，用于确定配置文件）"
    echo "  channel_path: 频道文件夹路径（例如: ./downloads/频道名）"
    echo "  limit:        要保留的视频文件夹数量"
    echo "  offset:       起始偏移量（从0开始）"
    echo ""
    echo "示例:"
    echo "  $0 194.233.83.29 ./downloads/频道名 10 0    # 保留前10个视频文件夹"
    echo "  $0 194.233.83.29 ./downloads/频道名 20 10   # 保留第11-30个视频文件夹"
    exit 1
fi

IP="$1"
CHANNEL_PATH="$2"
LIMIT="$3"
OFFSET="$4"

# 验证 IP 格式
if [[ ! $IP =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
    log_error "无效的 IP 地址: $IP"
    exit 1
fi

# 验证参数
if [ ! -d "$CHANNEL_PATH" ]; then
    echo -e "${RED}错误: 频道路径不存在: $CHANNEL_PATH${NC}"
    exit 1
fi

if ! [[ "$LIMIT" =~ ^[0-9]+$ ]] || [ "$LIMIT" -le 0 ]; then
    echo -e "${RED}错误: limit 必须是正整数${NC}"
    exit 1
fi

if ! [[ "$OFFSET" =~ ^[0-9]+$ ]]; then
    echo -e "${RED}错误: offset 必须是非负整数${NC}"
    exit 1
fi

# 获取所有视频文件夹（按照 channel_info.json 的顺序，如果存在）
echo -e "${YELLOW}正在扫描视频文件夹...${NC}"

CHANNEL_INFO_FILE="$CHANNEL_PATH/channel_info.json"
VIDEO_DIRS=()

# 检查是否存在 channel_info.json
if [ -f "$CHANNEL_INFO_FILE" ]; then
    echo -e "${GREEN}找到 channel_info.json，将按照其中的顺序处理${NC}"
    
    # 使用 Python 按照 channel_info.json 的顺序获取视频文件夹
    while IFS= read -r video_id; do
        if [ -n "$video_id" ]; then
            video_dir="$CHANNEL_PATH/$video_id"
            if [ -d "$video_dir" ]; then
                VIDEO_DIRS+=("$video_dir")
            fi
        fi
    done < <(python3 <<EOF 2>/dev/null
import json
import sys

try:
    with open("$CHANNEL_INFO_FILE", 'r', encoding='utf-8') as f:
        videos = json.load(f)
    
    # 按照 channel_info.json 中的顺序提取 video_id
    for video in videos:
        video_id = video.get('id', '')
        if video_id:
            print(video_id)
except Exception as e:
    # 如果读取失败，输出到 stderr 但不退出
    print(f"警告: 无法读取 channel_info.json: {e}", file=sys.stderr)
    sys.exit(1)
EOF
    )
    
    # 如果从 channel_info.json 读取失败，回退到字母顺序
    if [ ${#VIDEO_DIRS[@]} -eq 0 ]; then
        echo -e "${YELLOW}警告: 从 channel_info.json 读取失败，回退到字母顺序${NC}"
        while IFS= read -r -d '' dir; do
            if [ -d "$dir" ]; then
                dirname=$(basename "$dir")
                if [ "$dirname" != "channel_info.json" ] && [ "$dirname" != ".global" ]; then
                    VIDEO_DIRS+=("$dir")
                fi
            fi
        done < <(find "$CHANNEL_PATH" -maxdepth 1 -mindepth 1 -print0 | sort -z)
    fi
else
    echo -e "${YELLOW}未找到 channel_info.json，使用字母顺序${NC}"
    # 如果没有 channel_info.json，使用字母顺序
    while IFS= read -r -d '' dir; do
        if [ -d "$dir" ]; then
            dirname=$(basename "$dir")
            if [ "$dirname" != "channel_info.json" ] && [ "$dirname" != ".global" ]; then
                VIDEO_DIRS+=("$dir")
            fi
        fi
    done < <(find "$CHANNEL_PATH" -maxdepth 1 -mindepth 1 -print0 | sort -z)
fi

TOTAL_COUNT=${#VIDEO_DIRS[@]}

if [ $TOTAL_COUNT -eq 0 ]; then
    echo -e "${YELLOW}警告: 在 $CHANNEL_PATH 中未找到视频文件夹${NC}"
    exit 0
fi

echo -e "${GREEN}找到 $TOTAL_COUNT 个视频文件夹${NC}"

# 调试：显示前几个文件夹名称
echo -e "${YELLOW}[DEBUG] 前5个文件夹:${NC}"
for ((i=0; i<5 && i<TOTAL_COUNT; i++)); do
    echo "  [$i] $(basename "${VIDEO_DIRS[$i]}")"
done

# 计算要保留的范围
START_INDEX=$OFFSET
END_INDEX=$((OFFSET + LIMIT - 1))
echo -e "${YELLOW}[DEBUG] 参数: LIMIT=$LIMIT, OFFSET=$OFFSET${NC}"
echo -e "${YELLOW}[DEBUG] 计算: START_INDEX=$START_INDEX, END_INDEX=$END_INDEX${NC}"

# 验证范围
if [ $START_INDEX -ge $TOTAL_COUNT ]; then
    echo -e "${YELLOW}警告: offset ($OFFSET) 超出范围，没有文件夹需要保留${NC}"
    echo -e "${YELLOW}是否要删除所有视频文件夹？(y/N)${NC}"
    read -r confirm
    if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
        echo "操作已取消"
        exit 0
    fi
    TO_DELETE=$TOTAL_COUNT
    TO_KEEP=0
else
    if [ $END_INDEX -ge $TOTAL_COUNT ]; then
        END_INDEX=$((TOTAL_COUNT - 1))
    fi
    TO_KEEP=$((END_INDEX - START_INDEX + 1))
    TO_DELETE=$((TOTAL_COUNT - TO_KEEP))
fi

# 显示将要执行的操作
echo ""
echo "=========================================="
echo "操作摘要"
echo "=========================================="
echo "频道路径: $CHANNEL_PATH"
echo "总文件夹数: $TOTAL_COUNT"
echo "保留范围: [$START_INDEX, $END_INDEX]"
echo "将保留: $TO_KEEP 个文件夹"
echo "将删除: $TO_DELETE 个文件夹"
echo "=========================================="
echo ""

# 显示将要保留的文件夹
if [ $TO_KEEP -gt 0 ]; then
    echo -e "${GREEN}将要保留的文件夹:${NC}"
    for ((i=START_INDEX; i<=END_INDEX && i<TOTAL_COUNT; i++)); do
        dirname=$(basename "${VIDEO_DIRS[$i]}")
        echo "  [$i] $dirname"
    done
    echo ""
fi

# 显示将要删除的文件夹
if [ $TO_DELETE -gt 0 ]; then
    echo -e "${RED}将要删除的文件夹:${NC}"
    for ((i=0; i<START_INDEX && i<TOTAL_COUNT; i++)); do
        dirname=$(basename "${VIDEO_DIRS[$i]}")
        echo "  [$i] $dirname"
    done
    for ((i=END_INDEX+1; i<TOTAL_COUNT; i++)); do
        dirname=$(basename "${VIDEO_DIRS[$i]}")
        echo "  [$i] $dirname"
    done
    echo ""
fi

# 检查将要删除的文件夹中是否有已下载的视频
log_info "检查将要删除的文件夹中是否有已下载的视频..."
HAS_DOWNLOADED=false
DOWNLOADED_VIDEOS=()

for ((i=0; i<START_INDEX && i<TOTAL_COUNT; i++)); do
    dir="${VIDEO_DIRS[$i]}"
    if [ -d "$dir" ]; then
        # 检查 download_status.json 是否存在且视频状态为 completed
        status_file="$dir/download_status.json"
        if [ -f "$status_file" ]; then
            # 使用 Python 检查视频下载状态
            is_downloaded=$(python3 <<EOF 2>/dev/null
import json
import sys

try:
    with open("$status_file", 'r', encoding='utf-8') as f:
        status = json.load(f)
    
    # 检查 video 字段的状态
    video = status.get('video', {})
    if isinstance(video, dict):
        video_status = video.get('status', '')
        downloaded = video.get('downloaded', False)
        if video_status == 'completed' or downloaded:
            print('true')
            sys.exit(0)
    print('false')
except:
    print('false')
EOF
            )
            
            if [ "$is_downloaded" = "true" ]; then
                HAS_DOWNLOADED=true
                dirname=$(basename "$dir")
                DOWNLOADED_VIDEOS+=("$dirname")
            fi
        fi
    fi
done

for ((i=END_INDEX+1; i<TOTAL_COUNT; i++)); do
    dir="${VIDEO_DIRS[$i]}"
    if [ -d "$dir" ]; then
        # 检查 download_status.json 是否存在且视频状态为 completed
        status_file="$dir/download_status.json"
        if [ -f "$status_file" ]; then
            # 使用 Python 检查视频下载状态
            is_downloaded=$(python3 <<EOF 2>/dev/null
import json
import sys

try:
    with open("$status_file", 'r', encoding='utf-8') as f:
        status = json.load(f)
    
    # 检查 video 字段的状态
    video = status.get('video', {})
    if isinstance(video, dict):
        video_status = video.get('status', '')
        downloaded = video.get('downloaded', False)
        if video_status == 'completed' or downloaded:
            print('true')
            sys.exit(0)
    print('false')
except:
    print('false')
EOF
            )
            
            if [ "$is_downloaded" = "true" ]; then
                HAS_DOWNLOADED=true
                dirname=$(basename "$dir")
                DOWNLOADED_VIDEOS+=("$dirname")
            fi
        fi
    fi
done

# 如果发现已下载的视频，退出
if [ "$HAS_DOWNLOADED" = "true" ]; then
    log_error "检测到将要删除的文件夹中包含已下载的视频，操作已取消"
    log_error "已下载的视频文件夹:"
    for video_dir in "${DOWNLOADED_VIDEOS[@]}"; do
        echo "  - $video_dir"
    done
    log_error "请先确认这些视频是否真的需要删除，或手动删除后再运行此脚本"
    exit 1
fi

log_info "检查完成：将要删除的文件夹中不包含已下载的视频"

# 确认操作
echo -e "${YELLOW}确认执行删除操作？(y/N)${NC}"
read -r confirm
if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
    echo "操作已取消"
    exit 0
fi

# 检查 channel_info.json 是否存在（已在上面检查过，这里只是标记）
HAS_CHANNEL_INFO=false
if [ -f "$CHANNEL_INFO_FILE" ]; then
    HAS_CHANNEL_INFO=true
fi

# 执行删除
DELETED=0
FAILED=0
DELETED_VIDEO_IDS=()

echo ""
echo -e "${YELLOW}开始删除...${NC}"

# 删除 offset 之前的文件夹
if [ $START_INDEX -gt 0 ]; then
    echo -e "${YELLOW}[DEBUG] 开始删除 offset 之前的文件夹 (0 到 $((START_INDEX-1)))...${NC}"
    for ((i=0; i<START_INDEX && i<TOTAL_COUNT; i++)); do
        dir="${VIDEO_DIRS[$i]}"
        if [ ! -d "$dir" ]; then
            # 目录已不存在，跳过
            echo -e "${YELLOW}[DEBUG] 跳过已不存在的目录: $dir${NC}"
            continue
        fi
        dirname=$(basename "$dir")
        echo -e "${YELLOW}[DEBUG] 尝试删除: $dir${NC}"
        if rm -rf "$dir" 2>&1; then
            echo -e "${GREEN}✓ 已删除: $dirname${NC}"
            ((DELETED++))
            DELETED_VIDEO_IDS+=("$dirname")
        else
            echo -e "${RED}✗ 删除失败: $dirname${NC}"
            ((FAILED++))
        fi
    done
else
    echo -e "${YELLOW}[DEBUG] offset=0，无需删除 offset 之前的文件夹${NC}"
fi

# 删除 offset+limit 之后的文件夹
if [ $END_INDEX -lt $((TOTAL_COUNT - 1)) ]; then
    echo -e "${YELLOW}[DEBUG] 开始删除 offset+limit 之后的文件夹 ($((END_INDEX+1)) 到 $((TOTAL_COUNT-1)))...${NC}"
    for ((i=END_INDEX+1; i<TOTAL_COUNT; i++)); do
        dir="${VIDEO_DIRS[$i]}"
        if [ ! -d "$dir" ]; then
            # 目录已不存在，跳过
            echo -e "${YELLOW}[DEBUG] 跳过已不存在的目录: $dir${NC}"
            continue
        fi
        dirname=$(basename "$dir")
        echo -e "${YELLOW}[DEBUG] 尝试删除: $dir${NC}"
        if rm -rf "$dir" 2>&1; then
            echo -e "${GREEN}✓ 已删除: $dirname${NC}"
            ((DELETED++))
            DELETED_VIDEO_IDS+=("$dirname")
        else
            echo -e "${RED}✗ 删除失败: $dirname${NC}"
            ((FAILED++))
        fi
    done
else
    echo -e "${YELLOW}[DEBUG] 无需删除 offset+limit 之后的文件夹（END_INDEX=$END_INDEX, TOTAL_COUNT=$TOTAL_COUNT）${NC}"
fi

# 更新 channel_info.json（如果存在）
if [ "$HAS_CHANNEL_INFO" = true ] && [ ${#DELETED_VIDEO_IDS[@]} -gt 0 ]; then
    echo ""
    echo -e "${YELLOW}更新 channel_info.json...${NC}"
    
    # 检查是否有 python3 或 jq 可用
    if command -v python3 >/dev/null 2>&1; then
        # 使用 Python 更新 JSON
        # 将删除的 video_id 列表转换为 JSON 数组并传递给 Python
        DELETED_IDS_JSON=$(printf '%s\n' "${DELETED_VIDEO_IDS[@]}" | python3 -c "import sys, json; print(json.dumps([line.strip() for line in sys.stdin if line.strip()]))")
        
        python3 <<PYTHON_SCRIPT
import json
import sys

try:
    with open("$CHANNEL_INFO_FILE", 'r', encoding='utf-8') as f:
        videos = json.load(f)
    
    deleted_ids = json.loads('$DELETED_IDS_JSON')
    deleted_set = set(deleted_ids)
    
    original_count = len(videos)
    videos = [v for v in videos if v.get('id', '') not in deleted_set]
    new_count = len(videos)
    
    with open("$CHANNEL_INFO_FILE", 'w', encoding='utf-8') as f:
        json.dump(videos, f, ensure_ascii=False, indent=2)
    
    print(f"✓ 已从 channel_info.json 删除 {original_count - new_count} 个条目")
    print(f"  原始条目数: {original_count}")
    print(f"  更新后条目数: {new_count}")
except Exception as e:
    print(f"✗ 更新 channel_info.json 失败: {e}", file=sys.stderr)
    sys.exit(1)
PYTHON_SCRIPT
        if [ $? -eq 0 ]; then
            echo -e "${GREEN}channel_info.json 更新成功${NC}"
        else
            echo -e "${RED}channel_info.json 更新失败${NC}"
        fi
    elif command -v jq >/dev/null 2>&1; then
        # 使用 jq 更新 JSON
        TEMP_FILE=$(mktemp)
        jq_input="$CHANNEL_INFO_FILE"
        for video_id in "${DELETED_VIDEO_IDS[@]}"; do
            jq "map(select(.id != \"$video_id\"))" "$jq_input" > "$TEMP_FILE" && mv "$TEMP_FILE" "$jq_input"
        done
        rm -f "$TEMP_FILE"
        echo -e "${GREEN}channel_info.json 更新成功（使用 jq）${NC}"
    else
        echo -e "${YELLOW}警告: 未找到 python3 或 jq，无法更新 channel_info.json${NC}"
        echo -e "${YELLOW}请手动从 channel_info.json 中删除以下 video_id:${NC}"
        for video_id in "${DELETED_VIDEO_IDS[@]}"; do
            echo "  - $video_id"
        done
    fi
fi

# 显示结果
echo ""
echo "=========================================="
echo "操作完成"
echo "=========================================="
echo "成功删除: $DELETED 个文件夹"
if [ $FAILED -gt 0 ]; then
    echo -e "${RED}删除失败: $FAILED 个文件夹${NC}"
fi
echo "保留: $TO_KEEP 个文件夹"

# 验证实际剩余数量
REMAINING_COUNT=$(find "$CHANNEL_PATH" -maxdepth 1 -mindepth 1 -type d ! -name "channel_info.json" ! -name ".global" | wc -l)
echo "实际剩余文件夹数: $REMAINING_COUNT"
if [ $REMAINING_COUNT -ne $TO_KEEP ]; then
    echo -e "${YELLOW}警告: 实际剩余数量 ($REMAINING_COUNT) 与预期保留数量 ($TO_KEEP) 不一致${NC}"
fi
echo "=========================================="

