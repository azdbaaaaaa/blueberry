# 项目架构说明

## 目录结构

```
blueberry/
├── cmd/                    # 命令行入口层
│   ├── download.go        # 下载命令
│   ├── upload.go          # 上传命令
│   ├── sync.go            # 同步命令（下载+上传）
│   ├── rename.go          # 重命名命令
│   └── root.go            # 根命令
├── internal/              # 内部代码（不对外暴露）
│   ├── app/               # 应用层：依赖注入和初始化
│   │   └── app.go
│   ├── config/            # 配置层：配置加载和验证
│   │   └── config.go
│   ├── repository/        # 数据访问层（Repository层）
│   │   ├── bilibili/      # B站相关数据访问
│   │   ├── file/          # 文件系统相关数据访问
│   │   └── youtube/       # YouTube相关数据访问
│   └── service/           # 业务逻辑层（Service层）
│       ├── download_service.go   # 下载业务服务
│       └── upload_service.go     # 上传业务服务
└── pkg/                   # 公共包（可对外暴露）
    ├── logger/            # 日志工具
    └── utils/             # 工具函数
```

## 分层架构详解

### 1. Repository 层（数据访问层）

**职责：** 负责与外部系统或数据源交互，封装技术细节

**特点：**
- 不包含业务逻辑，只负责数据访问
- 通过接口定义，便于测试和替换实现
- 每个 repository 专注于一个外部系统或数据源

**具体实现：**

#### `repository/youtube/` - YouTube 数据访问
- `youtube_downloader.go`: 调用 `yt-dlp` 下载视频和字幕
- `youtube_parser.go`: 调用 `yt-dlp` 解析频道和视频信息
- `subtitle_manager.go`: 管理字幕相关的操作（列出、重命名）

#### `repository/bilibili/` - B站数据访问
- `bilibili_uploader.go`: 使用 `chromedp` 自动化浏览器上传视频

#### `repository/file/` - 文件系统数据访问
- `file_repository.go`: 文件查找、路径提取、目录创建等文件操作

**示例：**
```go
// Repository 层只负责调用外部工具，不关心业务逻辑
func (d *downloader) DownloadVideo(ctx context.Context, videoURL string, languages []string) (*DownloadResult, error) {
    // 调用 yt-dlp 命令
    cmd := exec.CommandContext(ctx, "yt-dlp", args...)
    // 返回结果，不处理业务逻辑
}
```

---

### 2. Service 层（业务逻辑层）

**职责：** 包含核心业务逻辑，编排 Repository 层的调用，处理业务规则

**特点：**
- 包含业务逻辑和业务规则
- 编排多个 Repository 的调用
- 处理错误、日志、配置等业务相关操作
- 不直接与外部系统交互

**具体实现：**

#### `DownloadService` - 下载业务服务
- **职责：** 处理下载相关的业务逻辑和编排
- **特点：** 直接调用 Repository，处理批量下载、dry-run、日志等
- **方法：**
  - `DownloadSingleVideo()` - 单个视频下载（包含 dry-run 逻辑）
  - `DownloadChannel()` - 频道下载（解析频道 → 批量下载）
  - `DownloadAllChannels()` - 所有频道下载（遍历配置）
- **业务逻辑：**
  - 频道语言配置查找（`getChannelLanguages()`）
  - dry-run 模式处理
  - 批量下载编排
  - 错误处理策略（失败继续下一个）

**业务逻辑示例：**
```go
// Service 层包含业务逻辑
func (s *downloadService) DownloadChannel(ctx context.Context, channelURL string) error {
    // 1. 业务逻辑：获取频道配置的语言
    languages := s.getChannelLanguages(channelURL)
    
    // 2. 业务逻辑：根据 dryRun 配置决定行为
    if s.cfg.DryRun {
        // 预览模式：只列出信息
    } else {
        // 实际下载：直接调用 Repository
        result, err := s.downloader.DownloadVideo(ctx, video.URL, languages)
    }
    
    // 3. 业务逻辑：错误处理和日志
    if err != nil {
        logger.Error().Err(err).Msg("下载视频失败")
        continue  // 继续处理下一个，不中断
    }
}
```

#### `UploadService` - 上传业务服务
- **职责：** 处理上传相关的业务逻辑和编排
- **特点：** 直接调用 Repository，处理批量上传、字幕重命名等
- **方法：**
  - `UploadSingleVideo()` - 单个视频上传（包含字幕重命名）
  - `UploadChannel()` - 频道上传（查找本地文件 → 批量上传）
  - `UploadAllChannels()` - 所有频道上传
  - `RenameSubtitlesForAID()` - 字幕文件重命名（业务逻辑）
- **业务逻辑：**
  - 字幕文件重命名规则（`{aid}_{lang}.srt`）
  - 批量上传编排
  - 账号匹配逻辑
  - 错误处理策略

---

### 3. App 层（应用层）

**职责：** 依赖注入和初始化，组装各个组件

**特点：**
- 创建和组装所有依赖
- 不包含业务逻辑
- 负责初始化检查（如 yt-dlp 是否安装）

**示例：**
```go
func NewApp(cfg *config.Config) (*App, error) {
    // 1. 创建 Repository 实例
    ytDownloader := youtube.NewDownloader(fileRepo)
    ytParser := youtube.NewParser()
    
    // 2. 初始化检查（业务规则）
    if err := ytParser.CheckInstalled(); err != nil {
        return nil, err
    }
    
    // 3. 创建 Service 实例（依赖注入）
    videoService := service.NewVideoService(...)
    downloadService := service.NewDownloadService(...)
    uploadService := service.NewUploadService(...)
    
    // 4. 组装应用
    return &App{...}
}
```

---

### 4. CMD 层（命令行入口层）

**职责：** 处理命令行参数，调用 Service 层

**特点：**
- 解析命令行参数
- 调用 Service 层方法
- 处理用户交互和错误输出
- 不包含业务逻辑

**示例：**
```go
var downloadCmd = &cobra.Command{
    Run: func(cmd *cobra.Command, args []string) {
        // 1. 加载配置
        cfg := config.Get()
        
        // 2. 创建应用（依赖注入）
        application, err := app.NewApp(cfg)
        
        // 3. 调用 Service 层（业务逻辑在 Service 层）
        if videoURL != "" {
            errExecute = downloadService.DownloadSingleVideo(ctx, videoURL)
        }
    },
}
```

---

## 数据流向

```
用户输入 (CLI)
    ↓
CMD 层 (参数解析)
    ↓
App 层 (依赖注入)
    ↓
Service 层 (业务逻辑) ← 这里是业务逻辑的主要位置
    ↓
Repository 层 (数据访问)
    ↓
外部系统 (YouTube/Bilibili/文件系统)
```

## 业务逻辑的位置

**业务逻辑主要在 Service 层：**

1. **DownloadService**: 包含下载相关的业务逻辑
   - 频道语言配置查找（`getChannelLanguages()`）
   - dry-run 模式处理
   - 批量下载编排
   - 错误处理和重试策略
   - 日志输出格式

2. **UploadService**: 包含上传相关的业务逻辑
   - 字幕文件重命名规则（`RenameSubtitlesForAID()`）
   - 批量上传编排
   - 账号匹配逻辑
   - 错误处理策略

**Repository 层不包含业务逻辑**，只负责：
- 调用外部工具（yt-dlp, chromedp）
- 封装技术细节
- 返回原始数据

## 设计原则

1. **单一职责**: 每个层只负责自己的职责
2. **依赖注入**: 通过接口和构造函数注入依赖
3. **接口隔离**: 每个 Repository 和 Service 都有明确的接口
4. **依赖方向**: 上层依赖下层，下层不依赖上层
   - CMD → App → Service → Repository

## 总结

- **Repository 层**: 数据访问，技术实现，不包含业务逻辑
- **Service 层**: 业务逻辑的主要位置，按功能域划分（Download/Upload）
  - `DownloadService`: 下载相关的业务逻辑
  - `UploadService`: 上传相关的业务逻辑
- **App 层**: 依赖注入和初始化
- **CMD 层**: 命令行入口，参数解析

**设计原则：**
- Service 层直接使用 Repository，不通过中间层
- 按功能域划分 Service（下载/上传），而不是按技术层次
- 业务逻辑集中在对应的 Service 中，保持单一职责

