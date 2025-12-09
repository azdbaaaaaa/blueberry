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
}

type BilibiliConfig struct {
	BaseURL            string `mapstructure:"base_url"`
	CookiesFromBrowser string `mapstructure:"cookies_from_browser"` // 从浏览器导入 cookies（仅本地开发环境使用）
	CookiesFile        string `mapstructure:"cookies_file"`         // Cookies 文件路径（Netscape 格式或 JSON 格式，推荐用于服务器环境）
	UploadMethod       string `mapstructure:"upload_method"`        // 上传方式：http（纯HTTP，推荐）或 chromedp（浏览器自动化，需要浏览器）
}

type YouTubeChannel struct {
	URL             string   `mapstructure:"url"`
	BilibiliAccount string   `mapstructure:"bilibili_account"`
	Languages       []string `mapstructure:"languages"` // 该频道需要下载的字幕语言，为空则使用全局配置或下载全部
}

type Account struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	CookiesFromBrowser string `mapstructure:"cookies_from_browser"` // 账号级别的 cookies 配置（从浏览器导入，仅本地开发环境使用）
	CookiesFile        string `mapstructure:"cookies_file"`         // 账号级别的 cookies 文件路径（优先于全局配置）
}

type SubtitlesConfig struct {
	Languages []string `mapstructure:"languages"`
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
}

var globalConfig *Config

func Load(configPath string) (*Config, error) {
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
		if channel.BilibiliAccount == "" {
			return fmt.Errorf("频道 %s 未指定B站账号", channel.URL)
		}
		if _, exists := cfg.BilibiliAccounts[channel.BilibiliAccount]; !exists {
			return fmt.Errorf("频道 %s 指定的账号 %s 不存在", channel.URL, channel.BilibiliAccount)
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
