package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type Config struct {
	Bilibili         BilibiliConfig     `mapstructure:"bilibili"`
	YouTubeChannels  []YouTubeChannel   `mapstructure:"youtube_channels"`
	BilibiliAccounts map[string]Account `mapstructure:"bilibili_accounts"`
	Subtitles        SubtitlesConfig    `mapstructure:"subtitles"`
	Output           OutputConfig       `mapstructure:"output"`
	YouTube          YouTubeConfig      `mapstructure:"youtube"`
	Logging          LoggingConfig      `mapstructure:"logging"`
	Channel          ChannelConfig      `mapstructure:"channel"`
}

type BilibiliConfig struct {
	BaseURL            string `mapstructure:"base_url"`
	CookiesFromBrowser string `mapstructure:"cookies_from_browser"` // 从浏览器导入 cookies（仅本地开发环境使用）
	CookiesFile        string `mapstructure:"cookies_file"`         // Cookies 文件路径（Netscape 格式或 JSON 格式，推荐用于服务器环境）
	UploadMethod       string `mapstructure:"upload_method"`        // 上传方式：http（纯HTTP，推荐）或 chromedp（浏览器自动化，需要浏览器）
	// UploadSubtitles 控制是否上传字幕文件，默认 false（不上传）
	UploadSubtitles bool `mapstructure:"upload_subtitles"`
	// 上传成功后是否删除本地原视频文件（仅删除视频，不删除字幕/元数据）
	DeleteOriginalAfterUpload bool `mapstructure:"delete_original_after_upload"`
	// 每个账号每日最大上传数，用于随机轮转账号时的限流，默认 160
	DailyUploadLimit int `mapstructure:"daily_upload_limit"`
	// 分块上传重试次数（单个分块），默认 3
	ChunkUploadRetries int `mapstructure:"chunk_upload_retries"`
	// 分块上传重试退避（秒），第 n 次重试等待 n*该值 秒，默认 1
	ChunkRetryBackoffSeconds int `mapstructure:"chunk_retry_backoff_seconds"`
}

type YouTubeChannel struct {
	URL       string   `mapstructure:"url"`
	Languages []string `mapstructure:"languages"` // 该频道需要下载的字幕语言，为空则使用全局配置或下载全部
	// Limit: 该频道下载数量上限（用于大频道分片抓取）；<=0 表示不限制
	Limit int `mapstructure:"limit"`
	// Offset: 从第几个开始（基于 channel_info.json 顺序）；<0 表示从 0 开始
	Offset int `mapstructure:"offset"`
}

type Account struct {
	Username    string `mapstructure:"username"`
	UserID      string `mapstructure:"userid"`       // B站用户ID
	CookiesFile string `mapstructure:"cookies_file"` // 账号级别的 cookies 文件路径（优先于全局配置）
}

type SubtitlesConfig struct {
	Languages []string `mapstructure:"languages"`
	// AutoFixOverlap 控制是否自动修复字幕时间轴重叠，默认 false（不启用）
	AutoFixOverlap bool `mapstructure:"auto_fix_overlap"`
}

type OutputConfig struct {
	Directory string `mapstructure:"directory"`
	// SubtitleArchive 字幕归档根目录（上传完成后将字幕复制到 {SubtitleArchive}/{aid}/ 下）
	SubtitleArchive string `mapstructure:"subtitle_archive"`
}

type YouTubeConfig struct {
	// CookiesFromBrowser 从浏览器导入 cookies，例如 "chrome", "firefox", "safari", "edge" 等
	// 如果设置了此选项，会使用 --cookies-from-browser 参数
	CookiesFromBrowser string `mapstructure:"cookies_from_browser"`
	// CookiesFile cookies 文件路径（Netscape 格式或 JSON 格式）
	// 如果设置了此选项，会使用 --cookies 参数
	CookiesFile string `mapstructure:"cookies_file"`
	// ForceDownloadUndownloadable 当之前被标记为“不可下载”时，是否仍强制尝试下载
	// 默认 false：跳过这些视频；true：继续尝试下载
	ForceDownloadUndownloadable bool `mapstructure:"force_download_undownloadable"`
	// MinHeight 限制视频下载的最低分辨率高度（像素），严格限制；若达不到则失败
	// 默认 1080
	MinHeight int `mapstructure:"min_height"`
	// DisableAndroidFallback web 下载失败时，是否禁用回退到 android 客户端；默认 true（不回退，降低风控风险）
	DisableAndroidFallback bool `mapstructure:"disable_android_fallback"`
	// ForceIPv6 是否强制使用 IPv6；默认 true（启用 IPv6）
	ForceIPv6 bool `mapstructure:"force_ipv6"`
	// Retries yt-dlp --retries
	Retries int `mapstructure:"retries"`
	// FragmentRetries yt-dlp --fragment-retries
	FragmentRetries int `mapstructure:"fragment_retries"`
	// ConcurrentFragments yt-dlp --concurrent-fragments
	ConcurrentFragments int `mapstructure:"concurrent_fragments"`
	// SleepIntervalSeconds yt-dlp --sleep-interval（秒）
	SleepIntervalSeconds int `mapstructure:"sleep_interval_seconds"`
	// SleepRequestsSeconds yt-dlp --sleep-requests（秒）
	SleepRequestsSeconds int `mapstructure:"sleep_requests_seconds"`
	// SleepSubtitlesSeconds yt-dlp --sleep-subtitles（秒）
	SleepSubtitlesSeconds int `mapstructure:"sleep_subtitles_seconds"`
	// LimitRate yt-dlp --limit-rate（限速，例如 "10M" 表示 10MB/s）
	LimitRate string `mapstructure:"limit_rate"`
	// BufferSize yt-dlp --buffer-size（缓冲区大小，例如 "1M" 表示 1MB）
	BufferSize string `mapstructure:"buffer_size"`
	// FileAccessRetries yt-dlp --file-access-retries（文件访问重试次数）
	FileAccessRetries int `mapstructure:"file_access_retries"`
	// VideoLimitBeforeRest 成功下载多少个视频后休息（默认 10，0 表示不限制）
	VideoLimitBeforeRest int `mapstructure:"video_limit_before_rest"`
	// VideoLimitRestDuration 休息时长（分钟），默认 1 小时（60分钟），实际休息时间会在此基础上随机增加 0-10%
	VideoLimitRestDuration int `mapstructure:"video_limit_rest_duration"`
	// BotDetectionThreshold 机器人检测累计多少次后触发休息（默认 10 次）
	BotDetectionThreshold int `mapstructure:"bot_detection_threshold"`
	// BotDetectionRestDuration 机器人检测后的休息时长（分钟），默认 8 小时（480分钟），实际休息时间会在此基础上随机增加 0-10%
	BotDetectionRestDuration int `mapstructure:"bot_detection_rest_duration"`
	// 运行期覆盖（命令行优先于配置），不从配置文件读取
	LimitOverride  int `mapstructure:"-"`
	OffsetOverride int `mapstructure:"-"`
}

// LoggingConfig 控制日志级别与输出路径
type LoggingConfig struct {
	// Level: debug/info/warn/error
	Level string `mapstructure:"level"`
	// FilePath: 可选，所有级别写入同一个文件（与下方 stdout/stderr 二选一）
	FilePath string `mapstructure:"file_path"`
	// StdoutPath: 普通日志输出文件（可选）
	StdoutPath string `mapstructure:"stdout_path"`
	// StderrPath: 错误日志输出文件（可选）
	StderrPath string `mapstructure:"stderr_path"`
	// Rotate: 日志滚动策略（使用 lumberjack）
	Rotate LogRotateConfig `mapstructure:"rotate"`
}

// LogRotateConfig 日志滚动参数（配合 lumberjack 使用）
type LogRotateConfig struct {
	// 单个日志文件最大尺寸（MB），默认 100
	MaxSizeMB int `mapstructure:"max_size_mb"`
	// 最多保留的旧文件个数，默认 7
	MaxBackups int `mapstructure:"max_backups"`
	// 最多保留的天数，默认 30
	MaxAgeDays int `mapstructure:"max_age_days"`
	// 是否压缩旧日志，默认 true
	Compress bool `mapstructure:"compress"`
}

// ChannelConfig 控制频道解析行为
type ChannelConfig struct {
	// 是否在解析后生成 pending_downloads.json（扫描本地状态，可能较慢），默认 true
	GeneratePendingDownloads bool `mapstructure:"generate_pending_downloads"`
}

var globalConfig *Config

func Load(configPath string) (*Config, error) {
	// 默认值
	viper.SetDefault("channel.generate_pending_downloads", false)
	viper.SetDefault("bilibili.base_url", "https://www.bilibili.tv/en/")
	viper.SetDefault("bilibili.daily_upload_limit", 160)
	viper.SetDefault("bilibili.upload_subtitles", false)
	viper.SetDefault("bilibili.chunk_upload_retries", 5)
	viper.SetDefault("bilibili.chunk_retry_backoff_seconds", 1)
	viper.SetDefault("bilibili.delete_original_after_upload", true)
	viper.SetDefault("subtitles.auto_fix_overlap", false)
	viper.SetDefault("youtube.force_download_undownloadable", true)
	viper.SetDefault("youtube.min_height", 1080)
	viper.SetDefault("youtube.disable_android_fallback", true)
	viper.SetDefault("youtube.force_ipv6", true) // 默认启用 IPv6
	viper.SetDefault("youtube.retries", 3)
	viper.SetDefault("youtube.fragment_retries", 3)
	viper.SetDefault("youtube.concurrent_fragments", 1)
	viper.SetDefault("youtube.sleep_interval_seconds", 60)
	viper.SetDefault("youtube.sleep_requests_seconds", 3)
	viper.SetDefault("youtube.sleep_subtitles_seconds", 2)
	viper.SetDefault("youtube.limit_rate", "10M")
	viper.SetDefault("youtube.buffer_size", "1M")
	viper.SetDefault("youtube.file_access_retries", 5)
	viper.SetDefault("youtube.video_limit_before_rest", 10)
	viper.SetDefault("youtube.video_limit_rest_duration", 60)    // 1小时 = 60分钟，实际休息时间会在此基础上随机增加 0-10%
	viper.SetDefault("youtube.bot_detection_threshold", 10)      // 机器人检测累计10次后触发休息
	viper.SetDefault("youtube.bot_detection_rest_duration", 480) // 8小时 = 480分钟，实际休息时间会在此基础上随机增加 0-10%
	viper.SetDefault("output.directory", "./downloads")
	viper.SetDefault("output.subtitle_archive", "./output")

	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.blueberry")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil, fmt.Errorf("配置文件未找到: %w", err)
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	globalConfig = &config
	return &config, nil
}

func Get() *Config {
	return globalConfig
}

func validate(cfg *Config) error {
	if cfg.Output.Directory == "" {
		return fmt.Errorf("输出目录不能为空")
	}

	if cfg.Bilibili.BaseURL == "" {
		return fmt.Errorf("B站基础URL不能为空")
	}

	for _, channel := range cfg.YouTubeChannels {
		if channel.URL == "" {
			return fmt.Errorf("YouTube频道URL不能为空")
		}
	}

	for accountName, account := range cfg.BilibiliAccounts {
		if account.Username == "" {
			return fmt.Errorf("账号 %s 的用户名不能为空", accountName)
		}
		// 必须配置 cookies 文件（账号级别或全局）
		hasCookies := account.CookiesFile != "" || cfg.Bilibili.CookiesFile != ""
		if !hasCookies {
			return fmt.Errorf("账号 %s 必须配置 cookies 文件", accountName)
		}
	}

	if err := os.MkdirAll(cfg.Output.Directory, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	return nil
}
