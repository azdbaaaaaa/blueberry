#!/bin/bash
# 同步本地目录到远程服务器
# 用法: ./sync_dir.sh <本地目录> <远程IP> <远程目录> [SSH用户]

if [ $# -lt 3 ]; then
    echo "用法: $0 <本地目录> <远程IP> <远程目录> [SSH用户]"
    echo ""
    echo "示例:"
    echo "  $0 ./downloads 192.168.1.100 /opt/blueberry/downloads"
    echo "  $0 ./output 192.168.1.100 /opt/blueberry/output root"
    echo "  $0 ./local_dir 192.168.1.100 /remote/path"
    exit 1
fi

LOCAL_DIR="$1"
REMOTE_HOST="$2"
REMOTE_DIR="$3"
REMOTE_USER="${4:-root}"

# 检查本地目录是否存在
if [ ! -d "$LOCAL_DIR" ]; then
    echo "错误: 本地目录不存在: $LOCAL_DIR"
    exit 1
fi

# 确保本地目录路径以 / 结尾（rsync 要求）
if [[ "$LOCAL_DIR" != */ ]]; then
    LOCAL_DIR="${LOCAL_DIR}/"
fi

# 确保远程目录路径不以 / 结尾（rsync 要求）
REMOTE_DIR="${REMOTE_DIR%/}"

echo "同步: $LOCAL_DIR -> $REMOTE_USER@$REMOTE_HOST:$REMOTE_DIR/"

# 使用 rsync 同步
# -a: 归档模式（保留权限、时间戳等）
# -z: 压缩传输
# -P: 显示进度
# --partial: 保留部分传输的文件
# --inplace: 原地更新（节省空间）
rsync -azP --partial --inplace \
    -e "ssh -o StrictHostKeyChecking=no" \
    "$LOCAL_DIR" \
    "$REMOTE_USER@$REMOTE_HOST:$REMOTE_DIR/"

if [ $? -eq 0 ]; then
    echo "✓ 同步完成"
else
    echo "✗ 同步失败"
    exit 1
fi

