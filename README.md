# Blueberry - YouTube视频抓取与B站上传工具

一个用于从YouTube下载视频和字幕，并上传到B站海外版的CLI工具。

## 功能特性

- 📥 从YouTube频道页面批量下载视频
- 🌐 支持多语言字幕下载（可配置语言列表或下载全部）
- 📤 自动上传视频到B站海外版（bilibili.tv）
- 🔄 支持一键同步（下载+上传），可逐个视频顺序执行
- ⚙️ 基于YAML的灵活配置
- 🎯 支持为不同频道指定不同的B站账号
 - 🧰 AWS EC2（Amazon Linux）一键部署脚本（Makefile）

## 前置要求

1. **Go 1.24+**
2. **yt-dlp**: 用于下载YouTube视频和字幕
   ```bash
   # macOS
   brew install yt-dlp
   
   # 或使用pip
   pip install yt-dlp
   ```

3. **Chrome/Chromium**: 用于B站上传（chromedp需要）

## 安装

```bash
git clone <repository-url>
cd blueberry
go build -o blueberry .
```

## 配置

1. 复制配置文件示例：
```bash
cp config.yaml.example config.yaml
```

2. 编辑 `config.yaml`：

```yaml
bilibili:
  base_url: "https://www.bilibili.tv/en/"
  # 上传成功后是否删除本地原视频文件（仅删除视频，不删除字幕/元数据）
  delete_original_after_upload: false

youtube_channels:
  - url: "https://www.youtube.com/@example/videos"
    languages: ["en", "id", "my", "th"]  # 该频道需要下载的字幕语言，为空则使用全局配置
  - url: "https://www.youtube.com/@another/videos"
    languages: ["en", "zh"]  # 不同频道可以配置不同的字幕语言

bilibili_accounts:
  account1:
    username: "user1"
    password: "pass1"
  account2:
    username: "user2"
    password: "pass2"

subtitles:
  languages: []  # 全局默认字幕语言，为空则使用频道配置或下载全部

output:
  directory: "./downloads"

logging:
  # 可选：debug | info | warn | error
  level: "info"
  # 可选：所有级别写入同一个文件（与下方 stdout/stderr 二选一）
  file_path: ""
  # 可选：输出文件路径（Linux 推荐使用 /var/log/blueberry/）
  stdout_path: "/var/log/blueberry/out.log"
  stderr_path: "/var/log/blueberry/err.log"
  # 滚动策略（lumberjack）
  rotate:
    max_size_mb: 100
    max_backups: 7
    max_age_days: 30
    compress: true

channel:
  # 是否在解析后生成 pending_downloads.json（扫描本地状态，可能较慢）
  generate_pending_downloads: false
```

### 配置说明

- `youtube_channels`: YouTube频道列表，每个频道需要指定：
  - `url`: 频道URL（支持 `/videos` 后缀）
  - `languages`: 该频道需要下载的字幕语言列表（可选，为空则使用全局配置）
- `bilibili_accounts`: B站账号信息（程序会在这些账号中随机选择一个未达当日上传上限的账号）
- `subtitles.languages`: 全局默认字幕语言列表（可选，为空则使用频道配置或下载全部）
- `output.directory`: 视频和字幕文件的保存目录

**字幕语言配置优先级：**
1. 频道级别的 `languages` 配置（如果存在）
2. 全局的 `subtitles.languages` 配置（如果存在）
3. 下载所有可用字幕（如果都为空）

## 使用方法

### 列出配置信息

```bash
./blueberry list
```

### 下载视频

下载配置中的所有频道：
```bash
./blueberry download
```

下载指定频道：
```bash
./blueberry download --channel "https://www.youtube.com/@example/videos"
```

下载单个视频：
```bash
./blueberry download --video "https://www.youtube.com/watch?v=VIDEO_ID"
```

### 上传视频

```bash
./blueberry upload --video "/path/to/video.mp4" --account "account1"
```

### 同步（下载+上传）

逐个视频“下载→上传”的顺序同步（推荐）：
```bash
./blueberry sync --channel "https://www.youtube.com/@example/videos"
# 或全量：
./blueberry sync --all
```

## 命令说明

### `download`
从YouTube下载视频和字幕文件。

**选项：**
- `--channel`: 指定要下载的频道URL
- `--video`: 指定要下载的单个视频URL
- 无选项：下载配置中所有频道的视频

### `upload`
上传视频到B站海外版。

**选项：**
- `--video`: 要上传的视频文件路径（必需）
- `--account`: B站账号名称（必需）

### `sync`
逐个视频执行“下载→上传”的顺序同步，避免批量下载后统一上传导致的空间/状态不一致问题。

### `channel`
解析/同步频道信息。
支持跳过生成 pending（适合超大频道）：
```bash
./blueberry channel --no-pending
```
### `list`
列出配置中的频道、账号等信息。

## 部署（AWS EC2 - Amazon Linux）

### 1) 准备实例
- 选择 Amazon Linux（推荐 2023 或 Amazon Linux 2）
- 安全组开放 SSH，出站网络允许 HTTP/HTTPS

### 2) 克隆与构建
```bash
git clone <repository-url>
cd blueberry
make deps       # 安装 yt-dlp / ffmpeg 等依赖
make build      # 构建 Linux 可执行文件
```

### 3) 安装与后台运行
```bash
sudo mkdir -p /var/log/blueberry
# 可选：在 config.yaml 中设置 logging.stdout_path / logging.stderr_path
make install
make start      # 后台运行（nohup），日志写 /var/log/blueberry/{out,err}.log
```

### 4) 日志与停止
```bash
make logs       # 跟随日志
make stop       # 停止运行
```

提示：
- 若希望上传成功后删除本地视频，请在 `config.yaml` 设置：
  ```yaml
  bilibili:
    delete_original_after_upload: true
  ```
- 程序将按 `logging.level` 设置日志级别，并按路径将 Info/Debug 输出到 stdout_path，Warn/Error 输出到 stderr_path。

## 文件结构

下载的文件会按以下结构组织：

```
downloads/
├── VIDEO_ID_1/
│   ├── video_title.mp4
│   ├── video_title.en.vtt
│   └── video_title.zh.vtt
├── VIDEO_ID_2/
│   └── ...
```

## 注意事项

1. **B站上传**: 由于B站可能没有公开的API，当前实现使用浏览器自动化。上传过程需要：
   - 浏览器会自动打开（非headless模式）
   - 首次使用时需要手动登录
   - 上传过程中可能需要手动填写视频信息

2. **yt-dlp**: 确保已正确安装yt-dlp，否则下载功能无法使用

3. **网络**: 下载和上传过程需要稳定的网络连接

4. **版权**: 请确保您有权限下载和上传相关视频内容

## 开发

项目结构：

```
blueberry/
├── cmd/              # CLI命令
├── internal/
│   ├── config/      # 配置管理
│   ├── downloader/  # YouTube下载
│   ├── parser/      # 频道解析
│   └── uploader/    # B站上传
├── pkg/
│   └── utils/       # 工具函数
└── main.go          # 程序入口
```

## 许可证

[添加许可证信息]

