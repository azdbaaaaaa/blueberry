package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"blueberry/internal/app"
	"blueberry/internal/config"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	subtitleChannelDir string
	subtitleVideoDir   string
	subtitleForce      bool
)

var subtitleCmd = &cobra.Command{
	Use:   "subtitle",
	Short: "补充缺失的字幕文件",
	Long: `检查视频目录，补充缺失的字幕文件。
可以指定单个视频目录、单个频道目录。
如果不指定任何参数，将自动扫描并处理所有频道。
会跳过已下载的字幕和已确认不存在的语言字幕，只下载缺失或之前下载失败的字幕。`,
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

		downloadService := application.DownloadService

		var errExecute error

		if subtitleVideoDir != "" {
			// 处理指定视频目录
			absVideoDir, err := filepath.Abs(subtitleVideoDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "解析视频目录路径失败: %v\n", err)
				os.Exit(1)
			}
			errExecute = downloadService.FixSubtitlesForVideoDir(ctx, absVideoDir, subtitleForce)
		} else if subtitleChannelDir != "" {
			// 处理指定频道目录
			absChannelDir, err := filepath.Abs(subtitleChannelDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "解析频道目录路径失败: %v\n", err)
				os.Exit(1)
			}
			errExecute = downloadService.FixSubtitlesForChannelDir(ctx, absChannelDir, subtitleForce)
		} else {
			// 默认行为：处理所有频道
			errExecute = downloadService.FixSubtitles(ctx, subtitleForce)
		}

		if errExecute != nil {
			fmt.Fprintf(os.Stderr, "补充字幕失败: %v\n", errExecute)
			os.Exit(1)
		}
	},
}

func init() {
	subtitleCmd.Flags().StringVar(&subtitleChannelDir, "channel-dir", "", "指定要处理的频道目录（例如：downloads/Comic-likerhythm）")
	subtitleCmd.Flags().StringVar(&subtitleVideoDir, "video-dir", "", "指定要处理的视频目录（例如：downloads/Comic-likerhythm/videoTitle）")
	subtitleCmd.Flags().BoolVar(&subtitleForce, "force", false, "强制模式：忽略状态文件，对所有缺失的字幕进行下载，并确保新旧格式都存在")
	rootCmd.AddCommand(subtitleCmd)
}
