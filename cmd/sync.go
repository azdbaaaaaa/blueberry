package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"blueberry/internal/app"
	"blueberry/internal/config"
	"blueberry/internal/repository/file"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	serialChannelURL string
	serialAll        bool
)

// sync: 按视频顺序逐个下载完再上传（下载→上传为一个原子单元）
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "顺序同步：逐个视频下载后立即上传",
	Long:  `对指定频道或所有频道，逐个视频执行“下载→上传”，按顺序处理，避免批量下载后统一上传。`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Get()
		if cfg == nil {
			fmt.Fprintf(os.Stderr, "配置未加载\n")
			os.Exit(1)
		}

		if !serialAll && serialChannelURL == "" {
			fmt.Fprintf(os.Stderr, "请指定频道（--channel）或使用 --all 处理所有频道\n")
			os.Exit(1)
		}

		application, err := app.NewApp(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "初始化应用失败: %v\n", err)
			os.Exit(1)
		}

		logger.SetLevel(zerolog.InfoLevel)
		ctx := context.Background()

		fileRepo := file.NewRepository(cfg.Output.Directory)

		processChannel := func(ch config.YouTubeChannel) error {
			channelID := fileRepo.ExtractChannelID(ch.URL)
			channelDir := filepath.Join(cfg.Output.Directory, channelID)

			// 确保有频道信息
			if !fileRepo.ChannelInfoExists(channelID) {
				logger.Info().Str("channel_url", ch.URL).Msg("未找到频道信息，先解析频道")
				if err := application.DownloadService.ParseChannels(ctx); err != nil {
					return fmt.Errorf("解析频道失败: %w", err)
				}
			}

			videos, err := fileRepo.LoadChannelInfo(channelID)
			if err != nil || len(videos) == 0 {
				return fmt.Errorf("加载频道视频列表失败或为空: %w", err)
			}

			logger.Info().
				Str("channel_id", channelID).
				Int("count", len(videos)).
				Msg("开始顺序处理频道视频")

			for i, v := range videos {
				videoID, _ := v["id"].(string)
				title, _ := v["title"].(string)
				if videoID == "" {
					continue
				}

				logger.Info().
					Int("current", i+1).
					Int("total", len(videos)).
					Str("video_id", videoID).
					Str("title", title).
					Msg("处理视频")

				// 目录使用 videoID（与下载服务一致）
				videoDir := filepath.Join(channelDir, videoID)

				// 先下载该视频（包含字幕/缩略图等按需步骤）
				if err := application.DownloadService.DownloadVideoDir(ctx, videoDir); err != nil {
					logger.Error().Err(err).Str("video_dir", videoDir).Msg("下载该视频失败，继续下一个")
					continue
				}

				// 立即上传该视频
				if err := application.UploadService.UploadSingleVideo(ctx, videoDir, ch.BilibiliAccount); err != nil {
					logger.Error().Err(err).Str("video_dir", videoDir).Msg("上传该视频失败，继续下一个")
					continue
				}
			}
			return nil
		}

		if serialAll {
			for _, ch := range cfg.YouTubeChannels {
				if err := processChannel(ch); err != nil {
					logger.Error().Err(err).Str("channel_url", ch.URL).Msg("顺序同步失败（继续下一个频道）")
				}
			}
			return
		}

		// 单频道
		var target *config.YouTubeChannel
		for i := range cfg.YouTubeChannels {
			if cfg.YouTubeChannels[i].URL == serialChannelURL {
				target = &cfg.YouTubeChannels[i]
				break
			}
		}
		if target == nil {
			fmt.Fprintf(os.Stderr, "未找到该频道配置：%s\n", serialChannelURL)
			os.Exit(1)
		}
		if err := processChannel(*target); err != nil {
			logger.Error().Err(err).Str("channel_url", target.URL).Msg("顺序同步失败")
			os.Exit(1)
		}
	},
}

func init() {
	syncCmd.Flags().StringVar(&serialChannelURL, "channel", "", "要顺序同步的频道URL")
	syncCmd.Flags().BoolVar(&serialAll, "all", false, "顺序同步配置中所有频道")
	rootCmd.AddCommand(syncCmd)
}
