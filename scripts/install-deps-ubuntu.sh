#!/bin/bash
# Blueberry 项目依赖安装脚本 - Ubuntu 22.04
# 安装所有必需的依赖：Go, Git, FFmpeg, FFprobe, yt-dlp, Node.js

set -e  # 遇到错误立即退出

echo "=========================================="
echo "Blueberry 项目依赖安装脚本 (Ubuntu 22.04)"
echo "=========================================="

# 检查是否为root用户或具有sudo权限
if [ "$EUID" -ne 0 ] && ! sudo -n true 2>/dev/null; then
    echo "错误: 此脚本需要sudo权限"
    exit 1
fi

# 使用sudo命令前缀
SUDO=""
if [ "$EUID" -ne 0 ]; then
    SUDO="sudo"
fi

echo ""
echo "步骤 1/6: 更新系统包列表..."
$SUDO apt-get update

echo ""
echo "步骤 2/6: 安装基础工具 (Git, curl, wget, tar, xz-utils)..."
$SUDO apt-get install -y git curl wget tar xz-utils ca-certificates

echo ""
echo "步骤 3/6: 安装 Go 1.24.0..."
GO_VERSION="1.24.0"
GO_INSTALL_DIR="/usr/local/go"

# 检查Go是否已安装且版本正确
if [ -d "$GO_INSTALL_DIR" ]; then
    if $GO_INSTALL_DIR/bin/go version 2>/dev/null | grep -q "go${GO_VERSION}"; then
        echo "Go ${GO_VERSION} 已安装，跳过..."
    else
        echo "检测到旧版本Go，正在更新到 ${GO_VERSION}..."
        $SUDO rm -rf $GO_INSTALL_DIR
    fi
else
    echo "正在安装 Go ${GO_VERSION}..."
fi

if [ ! -d "$GO_INSTALL_DIR" ]; then
    cd /tmp
    GO_TAR="go${GO_VERSION}.linux-amd64.tar.gz"
    echo "下载 Go ${GO_VERSION}..."
    curl -fsSL -o $GO_TAR "https://go.dev/dl/${GO_TAR}" || \
        curl -fsSL -o $GO_TAR "https://dl.google.com/go/${GO_TAR}"
    
    echo "解压并安装 Go..."
    $SUDO tar -C /usr/local -xzf $GO_TAR
    rm -f $GO_TAR
    
    # 创建符号链接（如果不存在）
    if [ ! -f /usr/local/bin/go ]; then
        $SUDO ln -sf $GO_INSTALL_DIR/bin/go /usr/local/bin/go
    fi
    if [ ! -f /usr/local/bin/gofmt ]; then
        $SUDO ln -sf $GO_INSTALL_DIR/bin/gofmt /usr/local/bin/gofmt
    fi
fi

# 验证Go安装
if [ -f $GO_INSTALL_DIR/bin/go ]; then
    $GO_INSTALL_DIR/bin/go version
    echo "✓ Go 安装成功"
else
    echo "✗ Go 安装失败"
    exit 1
fi

echo ""
echo "步骤 4/6: 安装 FFmpeg 和 FFprobe..."
if command -v ffmpeg >/dev/null 2>&1 && command -v ffprobe >/dev/null 2>&1; then
    echo "FFmpeg 和 FFprobe 已安装:"
    ffmpeg -version | head -n 1
else
    echo "正在安装 FFmpeg..."
    $SUDO apt-get install -y ffmpeg
    echo "✓ FFmpeg 安装成功"
fi

echo ""
echo "步骤 5/6: 安装 yt-dlp (最新版本，官方推荐方式)..."
# 使用官方推荐的方式：直接从 GitHub releases 下载最新二进制文件
YT_DLP_BIN="/usr/local/bin/yt-dlp"
if command -v yt-dlp >/dev/null 2>&1; then
    echo "yt-dlp 已安装，正在更新到最新版本..."
    $SUDO curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o $YT_DLP_BIN
    $SUDO chmod a+rx $YT_DLP_BIN
else
    echo "正在从 GitHub 下载最新版本的 yt-dlp..."
    $SUDO curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o $YT_DLP_BIN
    $SUDO chmod a+rx $YT_DLP_BIN
fi
yt-dlp --version
echo "✓ yt-dlp 安装成功"

echo ""
echo "步骤 6/6: 安装 Node.js 20.x (用于 yt-dlp JS runtime)..."
if command -v node >/dev/null 2>&1; then
    NODE_VERSION=$(node --version)
    echo "Node.js 已安装: $NODE_VERSION"
    # 检查是否为20.x版本
    if echo "$NODE_VERSION" | grep -q "^v20\."; then
        echo "Node.js 20.x 已安装，跳过..."
    else
        echo "检测到非20.x版本，正在安装 Node.js 20.x..."
        $SUDO apt-get remove -y nodejs npm 2>/dev/null || true
    fi
else
    echo "正在安装 Node.js 20.x..."
fi

if ! command -v node >/dev/null 2>&1 || ! node --version | grep -q "^v20\."; then
    curl -fsSL https://deb.nodesource.com/setup_20.x | $SUDO bash -
    $SUDO apt-get install -y nodejs
fi
node --version
npm --version
echo "✓ Node.js 安装成功"

echo ""
echo "=========================================="
echo "所有依赖安装完成！"
echo "=========================================="
echo ""
echo "已安装的软件版本："
echo "-------------------"
$GO_INSTALL_DIR/bin/go version
git --version
ffmpeg -version | head -n 1
yt-dlp --version
node --version
npm --version

echo ""
echo "下一步："
echo "1. 克隆项目: git clone <repository-url>"
echo "2. 进入项目目录: cd blueberry"
echo "3. 构建项目: go build -o blueberry ."
echo "4. 配置项目: cp config.yaml.example config.yaml"
echo ""

