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
	channelDir string
	videoDir   string
)

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "下载YouTube视频和字幕",
	Long: `下载视频和字幕文件到目标目录。
可以指定单个视频目录、单个频道目录。
如果不指定任何参数，将自动扫描并下载 downloads 目录下的所有频道。
所有下载都基于 sync-channel 同步的信息，包括字幕语言配置。`,
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

		var errExecute error

		if videoDir != "" {
			// 下载指定视频目录
			absVideoDir, err := filepath.Abs(videoDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "解析视频目录路径失败: %v\n", err)
				os.Exit(1)
			}
			errExecute = downloadService.DownloadVideoDir(ctx, absVideoDir)
		} else if channelDir != "" {
			// 下载指定频道目录
			absChannelDir, err := filepath.Abs(channelDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "解析频道目录路径失败: %v\n", err)
				os.Exit(1)
			}
			errExecute = downloadService.DownloadChannel(ctx, absChannelDir)
		} else {
			// 默认行为：扫描 downloads 目录下的所有频道并下载
			outputDir := cfg.Output.Directory
			if outputDir == "" {
				fmt.Fprintf(os.Stderr, "配置文件中未设置输出目录\n")
				os.Exit(1)
			}

			absOutputDir, err := filepath.Abs(outputDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "解析输出目录路径失败: %v\n", err)
				os.Exit(1)
			}

			logger.Info().Str("output_dir", absOutputDir).Msg("扫描频道目录")

			// 读取输出目录下的所有子目录
			entries, err := os.ReadDir(absOutputDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "读取输出目录失败: %v\n", err)
				os.Exit(1)
			}

			var channelDirs []string
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				// 检查是否是频道目录（包含 channel_info.json 或 video_info.json）
				channelPath := filepath.Join(absOutputDir, entry.Name())
				channelInfoPath := filepath.Join(channelPath, "channel_info.json")
				// 如果存在 channel_info.json，认为是频道目录
				if _, err := os.Stat(channelInfoPath); err == nil {
					channelDirs = append(channelDirs, channelPath)
				}
			}

			if len(channelDirs) == 0 {
				logger.Info().Msg("未找到任何频道目录")
				return
			}

			logger.Info().Int("count", len(channelDirs)).Msg("找到频道目录")

			// 下载每个频道
			for i, dir := range channelDirs {
				logger.Info().
					Int("current", i+1).
					Int("total", len(channelDirs)).
					Str("channel_dir", dir).
					Msg("开始下载频道")
				if err := downloadService.DownloadChannel(ctx, dir); err != nil {
					logger.Error().
						Str("channel_dir", dir).
						Err(err).
						Msg("下载频道失败，继续处理下一个")
					continue
				}
			}
		}

		if errExecute != nil {
			fmt.Fprintf(os.Stderr, "下载失败: %v\n", errExecute)
			os.Exit(1)
		}
	},
}

func init() {
	downloadCmd.Flags().StringVar(&channelDir, "channel-dir", "", "指定要下载的频道目录（例如：downloads/Comic-likerhythm）")
	downloadCmd.Flags().StringVar(&videoDir, "video-dir", "", "指定要下载的视频目录（例如：downloads/Comic-likerhythm/videoTitle）")
	rootCmd.AddCommand(downloadCmd)
}
