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

var (
	videoURL string
	dryRun   bool
)

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "下载YouTube视频和字幕",
	Long:  `从配置的YouTube频道下载视频和字幕文件到目标目录`,
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

		logger.SetLevel(zerolog.DebugLevel) // 使用 Debug 级别以便看到更详细的日志
		ctx := context.Background()

		downloadService := application.DownloadService

		var errExecute error
		if videoURL != "" {
			// 单个视频直接下载
			errExecute = downloadService.DownloadSingleVideo(ctx, videoURL)
		} else {
			// 所有频道：根据 dry-run 决定是解析还是下载
			if dryRun {
				// dry-run 模式：只解析所有频道信息
				errExecute = downloadService.ParseChannels(ctx)
			} else {
				// 正常模式：先解析（如果需要），然后下载
				if err := downloadService.ParseChannels(ctx); err != nil {
					errExecute = err
				} else {
					errExecute = downloadService.DownloadChannels(ctx)
				}
			}
		}

		if errExecute != nil {
			os.Exit(1)
		}
	},
}

func init() {
	downloadCmd.Flags().StringVar(&videoURL, "video", "", "指定要下载的单个视频URL")
	downloadCmd.Flags().BoolVar(&dryRun, "dry-run", false, "预览模式：仅解析频道信息并保存，不实际下载")
	rootCmd.AddCommand(downloadCmd)
}
