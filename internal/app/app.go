package app

import (
	"blueberry/internal/config"
	"blueberry/internal/repository/bilibili"
	"blueberry/internal/repository/file"
	"blueberry/internal/repository/youtube"
	"blueberry/internal/service"
)

type App struct {
	DownloadService service.DownloadService
	UploadService   service.UploadService
	Config          *config.Config
}

func NewApp(cfg *config.Config) (*App, error) {
	fileRepo := file.NewRepository(cfg.Output.Directory)

	ytDownloader := youtube.NewDownloader(
		fileRepo,
		cfg.YouTube.CookiesFromBrowser,
		cfg.YouTube.CookiesFile,
	)
	ytParser := youtube.NewParser(
		cfg.YouTube.CookiesFromBrowser,
		cfg.YouTube.CookiesFile,
	)
	if err := ytParser.CheckInstalled(); err != nil {
		return nil, err
	}
	// 根据配置选择上传方式
	var bilibiliUploader bilibili.Uploader
	uploadMethod := cfg.Bilibili.UploadMethod
	if uploadMethod == "" {
		uploadMethod = "http" // 默认使用 HTTP 方式
	}

	if uploadMethod == "http" {
		bilibiliUploader = bilibili.NewHTTPUploader(
			cfg.Bilibili.BaseURL,
			cfg.Bilibili.CookiesFromBrowser,
			cfg.Bilibili.CookiesFile,
		)
	} else {
		bilibiliUploader = bilibili.NewUploader(
			cfg.Bilibili.BaseURL,
			cfg.Bilibili.CookiesFromBrowser,
			cfg.Bilibili.CookiesFile,
		)
	}
	subtitleManager := youtube.NewSubtitleManager(
		cfg.YouTube.CookiesFromBrowser,
		cfg.YouTube.CookiesFile,
	)

	downloadService := service.NewDownloadService(
		ytDownloader,
		ytParser,
		subtitleManager,
		fileRepo,
		cfg,
	)
	uploadService := service.NewUploadService(
		bilibiliUploader,
		ytParser,
		subtitleManager,
		fileRepo,
		cfg,
	)

	return &App{
		DownloadService: downloadService,
		UploadService:   uploadService,
		Config:          cfg,
	}, nil
}
