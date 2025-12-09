# 下载流程分析

## 整体流程图

```
用户执行命令
    ↓
cmd/download.go (命令行入口)
    ↓
判断参数: --video / --channel / 无参数
    ↓
DownloadService (业务逻辑层)
    ├── DownloadSingleVideo()   # 单个视频
    ├── DownloadChannel()       # 单个频道
    └── DownloadAllChannels()   # 所有频道
    ↓
判断 dryRun 配置
    ├── true  → 预览模式（只列出信息）
    └── false → 实际下载
    ↓
Repository 层 (数据访问层)
    ├── Parser.ExtractVideosFromChannel()      # 解析频道
    ├── SubtitleManager.ListSubtitles()        # 列出字幕
    └── Downloader.DownloadVideo()             # 下载视频
    ↓
yt-dlp 命令执行
    ↓
文件系统操作
    └── FileRepository (查找文件、提取信息)
```

## 详细流程分析

### 1. 命令行入口流程 (cmd/download.go)

```go
用户输入: ./blueberry download --video "https://youtube.com/watch?v=xxx"
    ↓
1. 加载配置 config.Get()
    ↓
2. 处理命令行参数
   - 如果 --dry-run，覆盖配置中的 dryRun
    ↓
3. 创建应用 app.NewApp(cfg)
   - 初始化所有 Repository
   - 初始化所有 Service
    ↓
4. 根据参数选择执行路径
   - videoURL != ""  → DownloadSingleVideo()
   - channelURL != "" → DownloadChannel()
   - 都为空          → DownloadAllChannels()
```

### 2. DownloadSingleVideo 流程

```
输入: videoURL (单个视频URL)
    ↓
判断: cfg.DryRun?
    ├── true (预览模式)
    │   ├── 输出视频URL
    │   ├── 调用 SubtitleManager.ListSubtitles()
    │   │   └── 使用全局配置: cfg.Subtitles.Languages
    │   └── 输出字幕链接列表
    │
    └── false (实际下载)
        ├── 获取语言配置: cfg.Subtitles.Languages
        ├── 调用 Downloader.DownloadVideo(videoURL, languages)
        │   ├── 提取视频ID
        │   ├── 创建视频目录
        │   ├── 构建 yt-dlp 命令参数
        │   │   ├── 如果 languages 不为空: --sub-lang 指定语言
        │   │   └── 如果 languages 为空: --all-subs 下载全部
        │   ├── 执行 yt-dlp 命令
        │   ├── 查找下载的视频文件
        │   ├── 查找下载的字幕文件
        │   └── 返回 DownloadResult
        └── 输出下载结果
```

**问题点：**
- ✅ 已修复：dry-run 和实际下载都使用 `cfg.Subtitles.Languages`
- ✅ 行为一致：预览和实际下载使用相同的语言配置

### 3. DownloadChannel 流程

```
输入: channelURL (频道URL)
    ↓
1. 获取频道语言配置
   getChannelLanguages(channelURL)
   ├── 查找配置中的频道
   ├── 如果频道配置了 languages → 返回频道配置
   └── 否则 → 返回全局配置 cfg.Subtitles.Languages
    ↓
2. 判断: cfg.DryRun?
    ├── true (预览模式)
    │   ├── 输出频道信息
    │   ├── 调用 Parser.ExtractVideosFromChannel()
    │   │   └── 执行 yt-dlp --flat-playlist --dump-json
    │   ├── 遍历每个视频
    │   │   ├── 输出视频信息
    │   │   └── 调用 SubtitleManager.ListSubtitles()
    │   │       └── 输出字幕链接
    │   └── 不实际下载
    │
    └── false (实际下载)
        ├── 调用 Parser.ExtractVideosFromChannel()
        │   └── 获取频道下所有视频列表
        ├── 遍历每个视频
        │   ├── 调用 Downloader.DownloadVideo(video.URL, languages)
        │   │   └── 使用频道配置的语言（或全局配置）
        │   ├── 如果失败 → 记录错误，继续下一个
        │   └── 如果成功 → 输出结果
        └── 返回 nil（即使部分失败也继续）
```

**语言配置优先级：**
1. 频道级别的 `languages` 配置（如果存在）
2. 全局的 `subtitles.languages` 配置（如果存在）
3. 空数组 → 下载所有可用字幕

### 4. DownloadAllChannels 流程

```
输入: 无（使用配置中的所有频道）
    ↓
遍历配置中的每个频道: cfg.YouTubeChannels
    ↓
对每个频道:
    ├── 输出频道信息（dry-run 模式有额外格式）
    └── 调用 DownloadChannel(channel.URL)
        └── 复用 DownloadChannel 的完整流程
    ↓
如果某个频道失败 → 记录错误，继续下一个
    ↓
返回 nil（即使部分失败也继续）
```

**注意：**
- `DownloadAllChannels` 中会重复计算语言配置（在 `DownloadChannel` 中也会计算）
- 但这是合理的，因为 `DownloadChannel` 是独立的方法，可以被单独调用

### 5. Repository 层详细流程

#### Downloader.DownloadVideo()

```
输入: videoURL, languages
    ↓
1. 提取视频ID
   fileRepo.ExtractVideoID(videoURL)
   └── 从 URL 中提取视频ID（如 "dQw4w9WgXcQ"）
    ↓
2. 确保视频目录存在
   fileRepo.EnsureVideoDir(videoID)
   └── 创建目录: {output.directory}/{videoID}/
    ↓
3. 构建 yt-dlp 命令参数
   buildDownloadArgs(videoDir, videoURL, languages)
   ├── 输出路径: {videoDir}/%(title)s.%(ext)s
   ├── 视频格式: bestvideo+bestaudio/best
   └── 字幕参数:
       ├── 如果 languages 不为空:
       │   ├── --write-sub
       │   ├── --write-auto-sub
       │   └── --sub-lang {lang1} --sub-lang {lang2} ...
       └── 如果 languages 为空:
           ├── --write-sub
           ├── --write-auto-sub
           └── --all-subs
    ↓
4. 执行 yt-dlp 命令
   exec.CommandContext("yt-dlp", args...)
   └── 实际下载视频和字幕到指定目录
    ↓
5. 查找下载的文件
   ├── fileRepo.FindVideoFile(videoDir)
   │   └── 查找目录下的视频文件（.mp4, .mkv 等）
   └── fileRepo.FindSubtitleFiles(videoDir)
       └── 查找目录下的字幕文件（.vtt, .srt 等）
    ↓
6. 提取视频标题
   fileRepo.ExtractVideoTitleFromFile(videoFile)
   └── 从文件名中提取标题
    ↓
7. 返回 DownloadResult
   {
       VideoPath:     视频文件路径
       VideoTitle:    视频标题
       SubtitlePaths: 字幕文件路径列表
   }
```

## 关键业务逻辑点

### 1. 语言配置的优先级

```
单个视频下载 (DownloadSingleVideo):
    └── 使用全局配置: cfg.Subtitles.Languages

频道下载 (DownloadChannel):
    ├── 频道配置的 languages（如果存在）
    └── 否则使用全局配置: cfg.Subtitles.Languages

如果 languages 为空数组:
    └── 下载所有可用字幕
```

### 2. Dry-Run 模式的行为

```
Dry-Run = true:
    ├── 不执行实际下载
    ├── 只列出视频和字幕链接
    └── 使用相同的语言配置（保证预览和实际一致）

Dry-Run = false:
    └── 执行实际下载操作
```

### 3. 错误处理策略

```
单个视频下载:
    └── 失败 → 返回错误，中断流程

频道下载:
    └── 某个视频失败 → 记录错误，继续下一个视频

所有频道下载:
    └── 某个频道失败 → 记录错误，继续下一个频道
```

## 潜在问题和改进建议

### 1. ✅ 已修复：语言配置不一致
- **问题**：`DownloadSingleVideo` 在 dry-run 和实际下载时使用不同的语言配置
- **修复**：统一使用 `cfg.Subtitles.Languages`

### 2. 语言配置为空时的行为
- **当前**：如果 `languages` 为空，`yt-dlp` 会下载所有字幕
- **建议**：可以考虑在配置中明确区分"下载全部"和"不下载字幕"两种意图

### 3. 文件查找的容错性
- **当前**：如果找不到视频文件会返回错误
- **建议**：可以考虑更详细的错误信息，帮助用户定位问题

### 4. 下载进度反馈
- **当前**：只有开始和完成的日志
- **建议**：可以考虑添加进度条或更详细的进度信息

## 数据流示例

### 示例1：下载单个视频

```
输入: ./blueberry download --video "https://youtube.com/watch?v=abc123"
配置: subtitles.languages = ["en", "zh-Hans"]
    ↓
DownloadSingleVideo("https://youtube.com/watch?v=abc123")
    ↓
languages = ["en", "zh-Hans"]
    ↓
Downloader.DownloadVideo(videoURL, ["en", "zh-Hans"])
    ↓
yt-dlp 命令:
  yt-dlp \
    -o "./downloads/abc123/%(title)s.%(ext)s" \
    --write-sub --write-auto-sub \
    --sub-lang en --sub-lang zh-Hans \
    "https://youtube.com/watch?v=abc123"
    ↓
文件系统:
  ./downloads/abc123/
    ├── video_title.mp4
    ├── video_title.en.vtt
    └── video_title.zh-Hans.vtt
    ↓
返回:
  {
    VideoPath: "./downloads/abc123/video_title.mp4",
    VideoTitle: "video_title",
    SubtitlePaths: [
      "./downloads/abc123/video_title.en.vtt",
      "./downloads/abc123/video_title.zh-Hans.vtt"
    ]
  }
```

### 示例2：下载频道（dry-run）

```
输入: ./blueberry download --channel "https://youtube.com/@channel/videos" --dry-run
配置: 
  youtube_channels:
    - url: "https://youtube.com/@channel/videos"
      languages: ["en", "ar"]
    ↓
DownloadChannel("https://youtube.com/@channel/videos")
    ↓
languages = getChannelLanguages() = ["en", "ar"]
    ↓
DryRun = true
    ↓
Parser.ExtractVideosFromChannel()
    └── 返回: [Video1, Video2, Video3]
    ↓
遍历每个视频:
  Video1:
    ├── 输出: "--- 视频 ---"
    ├── 输出: title, url
    └── SubtitleManager.ListSubtitles(video1.URL, ["en", "ar"])
        └── 输出: 字幕链接列表
  Video2: ...
  Video3: ...
    ↓
不执行实际下载
```

## 总结

整个下载流程设计清晰，分层明确：

1. **CMD 层**：参数解析和路由
2. **Service 层**：业务逻辑编排（语言配置、dry-run、错误处理）
3. **Repository 层**：技术实现（调用 yt-dlp、文件操作）

**关键改进点：**
- ✅ 已修复语言配置不一致问题
- 流程逻辑清晰，错误处理合理
- 支持灵活的配置方式（频道级别 + 全局级别）

