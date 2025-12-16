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
	// 上传成功后是否删除本地原视频文件（仅删除视频，不删除字幕/元数据）
	DeleteOriginalAfterUpload bool `mapstructure:"delete_original_after_upload"`
	// 每个账号每日最大上传数，用于随机轮转账号时的限流，默认 160
	DailyUploadLimit int `mapstructure:"daily_upload_limit"`
}

type YouTubeChannel struct {
	URL       string   `mapstructure:"url"`
	Languages []string `mapstructure:"languages"` // 该频道需要下载的字幕语言，为空则使用全局配置或下载全部
}

type Account struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	CookiesFromBrowser string `mapstructure:"cookies_from_browser"` // 账号级别的 cookies 配置（从浏览器导入，仅本地开发环境使用）
	CookiesFile        string `mapstructure:"cookies_file"`         // 账号级别的 cookies 文件路径（优先于全局配置）
}

type SubtitlesConfig struct {
	Languages []string `mapstructure:"languages"`
	// AutoFixOverlap 控制是否自动修复字幕时间轴重叠，默认 false（不启用）
	AutoFixOverlap bool `mapstructure:"auto_fix_overlap"`
}

type OutputConfig struct {
	Directory string `mapstructure:"directory"`
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
	viper.SetDefault("bilibili.daily_upload_limit", 160)
	viper.SetDefault("subtitles.auto_fix_overlap", false)
	viper.SetDefault("youtube.force_download_undownloadable", false)
	viper.SetDefault("youtube.min_height", 1080)
	viper.SetDefault("youtube.disable_android_fallback", true)

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
		// 如果配置了 cookies 文件（账号级别或全局），密码可以为空（使用 cookies 登录）
		hasCookies := account.CookiesFile != "" || account.CookiesFromBrowser != "" ||
			cfg.Bilibili.CookiesFile != "" || cfg.Bilibili.CookiesFromBrowser != ""
		if account.Password == "" && !hasCookies {
			return fmt.Errorf("账号 %s 的密码不能为空（或配置 cookies 文件）", accountName)
		}
	}

	if err := os.MkdirAll(cfg.Output.Directory, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	return nil
}
