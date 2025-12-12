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

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "解析/同步频道信息",
	Long:  `解析配置文件中所有YouTube频道并保存视频列表信息到目录下（生成 channel_info.json）。`,
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

		logger.SetLevel(zerolog.DebugLevel)
		ctx := context.Background()

		downloadService := application.DownloadService

		// 同步所有频道信息
		if err := downloadService.ParseChannels(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "同步频道信息失败: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(channelCmd)
}
