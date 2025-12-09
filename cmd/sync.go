package cmd

import (
	"context"
	"fmt"
	"os"

	"blueberry/internal/app"
	"blueberry/internal/config"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "同步：下载并上传",
	Long:  `一键执行下载和上传流程，从YouTube下载视频后自动上传到B站`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Get()
		if cfg == nil {
			fmt.Fprintf(os.Stderr, "配置未加载\n")
			os.Exit(1)
		}

		application, err := app.NewApp(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "初始化应用失败: %v\n", err)
			os.Exit(1)
		}

		logger.SetLevel(zerolog.InfoLevel)

		ctx := context.Background()

		// 先下载所有频道
		if err := application.DownloadService.DownloadChannels(ctx); err != nil {
			logger.Error().Err(err).Msg("下载失败")
			os.Exit(1)
		}

		// 然后上传所有频道
		if err := application.UploadService.UploadAllChannels(ctx); err != nil {
			logger.Error().Err(err).Msg("上传失败")
			os.Exit(1)
		}
	},
}
