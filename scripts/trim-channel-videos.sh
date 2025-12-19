#!/bin/bash
# 切割频道下的视频文件夹
# 用法: ./trim-channel-videos.sh <channel_path> <limit> <offset>
# 示例: ./trim-channel-videos.sh ./downloads/频道名 10 0
#       保留排序后的前10个视频文件夹，删除其他

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 检查参数
if [ $# -lt 3 ]; then
    echo "用法: $0 <channel_path> <limit> <offset>"
    echo ""
    echo "参数说明:"
    echo "  channel_path: 频道文件夹路径（例如: ./downloads/频道名）"
    echo "  limit:        要保留的视频文件夹数量"
    echo "  offset:       起始偏移量（从0开始）"
    echo ""
    echo "示例:"
    echo "  $0 ./downloads/频道名 10 0    # 保留前10个视频文件夹"
    echo "  $0 ./downloads/频道名 20 10   # 保留第11-30个视频文件夹"
    exit 1
fi

CHANNEL_PATH="$1"
LIMIT="$2"
OFFSET="$3"

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

# 获取所有视频文件夹（排除 channel_info.json 等文件）
echo -e "${YELLOW}正在扫描视频文件夹...${NC}"
VIDEO_DIRS=()
while IFS= read -r -d '' dir; do
    # 只处理目录，排除 channel_info.json 等文件
    if [ -d "$dir" ]; then
        VIDEO_DIRS+=("$dir")
    fi
done < <(find "$CHANNEL_PATH" -maxdepth 1 -mindepth 1 -print0 | sort -z)

TOTAL_COUNT=${#VIDEO_DIRS[@]}

if [ $TOTAL_COUNT -eq 0 ]; then
    echo -e "${YELLOW}警告: 在 $CHANNEL_PATH 中未找到视频文件夹${NC}"
    exit 0
fi

echo -e "${GREEN}找到 $TOTAL_COUNT 个视频文件夹${NC}"

# 计算要保留的范围
START_INDEX=$OFFSET
END_INDEX=$((OFFSET + LIMIT - 1))

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

# 确认操作
echo -e "${YELLOW}确认执行删除操作？(y/N)${NC}"
read -r confirm
if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
    echo "操作已取消"
    exit 0
fi

# 执行删除
DELETED=0
FAILED=0

echo ""
echo -e "${YELLOW}开始删除...${NC}"

# 删除 offset 之前的文件夹
for ((i=0; i<START_INDEX && i<TOTAL_COUNT; i++)); do
    dir="${VIDEO_DIRS[$i]}"
    dirname=$(basename "$dir")
    if rm -rf "$dir" 2>/dev/null; then
        echo -e "${GREEN}✓ 已删除: $dirname${NC}"
        ((DELETED++))
    else
        echo -e "${RED}✗ 删除失败: $dirname${NC}"
        ((FAILED++))
    fi
done

# 删除 offset+limit 之后的文件夹
for ((i=END_INDEX+1; i<TOTAL_COUNT; i++)); do
    dir="${VIDEO_DIRS[$i]}"
    dirname=$(basename "$dir")
    if rm -rf "$dir" 2>/dev/null; then
        echo -e "${GREEN}✓ 已删除: $dirname${NC}"
        ((DELETED++))
    else
        echo -e "${RED}✗ 删除失败: $dirname${NC}"
        ((FAILED++))
    fi
done

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
echo "=========================================="

