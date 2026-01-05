#!/bin/bash

# 分割 ZIP 文件脚本
# 用法: ./scripts/split_zip.sh <zip_file> [split_count]
# 例如: ./scripts/split_zip.sh output-20260105.zip 2

set -e

if [ $# -lt 1 ]; then
    echo "用法: $0 <zip_file> [split_count]"
    echo "例如: $0 output-20260105.zip 2"
    exit 1
fi

ZIP_FILE="$1"
SPLIT_COUNT="${2:-2}"

if [ ! -f "$ZIP_FILE" ]; then
    echo "错误: 文件不存在: $ZIP_FILE"
    exit 1
fi

# 获取文件大小（字节）
FILE_SIZE=$(stat -f%z "$ZIP_FILE" 2>/dev/null || stat -c%s "$ZIP_FILE" 2>/dev/null)
if [ -z "$FILE_SIZE" ]; then
    echo "错误: 无法获取文件大小"
    exit 1
fi

# 计算每个分卷的大小（向上取整）
SPLIT_SIZE=$(( (FILE_SIZE + SPLIT_COUNT - 1) / SPLIT_COUNT ))

echo "文件: $ZIP_FILE"
echo "大小: $(numfmt --to=iec-i --suffix=B $FILE_SIZE 2>/dev/null || echo "${FILE_SIZE} bytes")"
echo "分割成: $SPLIT_COUNT 个文件"
echo "每个文件大小: $(numfmt --to=iec-i --suffix=B $SPLIT_SIZE 2>/dev/null || echo "${SPLIT_SIZE} bytes")"
echo ""

# 使用 split 命令分割文件
BASE_NAME="${ZIP_FILE%.zip}"
SPLIT_PREFIX="${BASE_NAME}.zip.part"

echo "正在分割文件..."

# macOS 的 split 不支持 -d 选项，使用 -a 指定后缀长度，然后手动重命名
# 计算需要的后缀长度（例如：2个文件需要1位，10个文件需要2位）
if [ $SPLIT_COUNT -le 10 ]; then
    SUFFIX_LEN=1
elif [ $SPLIT_COUNT -le 100 ]; then
    SUFFIX_LEN=2
else
    SUFFIX_LEN=3
fi

split -b "${SPLIT_SIZE}" -a "$SUFFIX_LEN" "$ZIP_FILE" "$SPLIT_PREFIX"

# 重命名分卷文件，使其更易识别（macOS split 使用字母后缀，需要转换为数字）
echo "重命名分卷文件..."
PART_INDEX=0
for part_file in "${SPLIT_PREFIX}"*; do
    if [ -f "$part_file" ]; then
        NEW_NAME="${BASE_NAME}.zip.part${PART_INDEX}"
        mv "$part_file" "$NEW_NAME"
        echo "  $(basename "$part_file") -> $(basename "$NEW_NAME")"
        PART_INDEX=$((PART_INDEX + 1))
    fi
done

# 创建合并脚本
MERGE_SCRIPT="${BASE_NAME}.merge.sh"
cat > "$MERGE_SCRIPT" <<EOF
#!/bin/bash
# 合并分卷 ZIP 文件并解压
# 用法: bash $MERGE_SCRIPT

set -e

ZIP_FILE="${ZIP_FILE}"
BASE_NAME="${BASE_NAME}"

echo "正在合并分卷文件..."
cat \${BASE_NAME}.zip.part* > "\${ZIP_FILE}"

echo "正在验证 ZIP 文件..."
if unzip -t "\${ZIP_FILE}" > /dev/null 2>&1; then
    echo "ZIP 文件验证成功"
    echo ""
    echo "正在解压..."
    unzip "\${ZIP_FILE}"
    echo ""
    echo "解压完成！"
    echo "是否删除合并后的 ZIP 文件？(y/N)"
    read -r response
    if [[ "\$response" =~ ^[Yy]$ ]]; then
        rm -f "\${ZIP_FILE}"
        echo "已删除合并后的 ZIP 文件"
    fi
else
    echo "错误: ZIP 文件验证失败，请检查分卷文件是否完整"
    exit 1
fi
EOF

chmod +x "$MERGE_SCRIPT"

echo ""
echo "分割完成！"
echo ""
echo "分卷文件:"
for i in $(seq 0 $((SPLIT_COUNT - 1))); do
    PART_FILE="${BASE_NAME}.zip.part${i}"
    if [ -f "$PART_FILE" ]; then
        PART_SIZE=$(stat -f%z "$PART_FILE" 2>/dev/null || stat -c%s "$PART_FILE" 2>/dev/null)
        echo "  $PART_FILE ($(numfmt --to=iec-i --suffix=B $PART_SIZE 2>/dev/null || echo "${PART_SIZE} bytes"))"
    fi
done

echo ""
echo "合并和解压脚本: $MERGE_SCRIPT"
echo ""
echo "使用方法:"
echo "  1. 将所有分卷文件 (.zip.part*) 和合并脚本 ($MERGE_SCRIPT) 放在同一目录"
echo "  2. 运行: bash $MERGE_SCRIPT"
echo "  3. 脚本会自动合并分卷并解压"

