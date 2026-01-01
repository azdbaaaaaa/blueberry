package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"blueberry/internal/app"
	"blueberry/internal/config"
	"blueberry/internal/repository/file"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	retryVideoIDs    []string // 要重新下载和上传的 video_id 列表
	retryDownloadOnly bool     // 只重新下载，不上传
	retryUploadOnly   bool     // 只重新上传，不下载
	retryAccount      string   // 指定上传时使用的账号名称
)

var retryCmd = &cobra.Command{
	Use:   "retry",
	Short: "强制重新下载和上传指定的视频",
	Long: `强制重新下载和上传指定的视频（通过 video_id）。

用法：
  blueberry retry <video_id1> <video_id2> ...

示例：
  # 重新下载和上传单个视频
  blueberry retry rU59tjI587M

  # 重新下载和上传多个视频
  blueberry retry rU59tjI587M znwqAD6RAaA abc123xyz

  # 只重新下载，不上传
  blueberry retry --download-only rU59tjI587M

  # 只重新上传，不下载
  blueberry retry --upload-only rU59tjI587M

该命令会：
1. 遍历所有频道目录，查找匹配的 video_id
2. 清理下载状态（重置 download_status.json）
3. 清理上传状态（删除 upload_status.json）
4. 重新下载视频（如果指定了 --download-only 或未指定 --upload-only）
5. 重新上传视频（如果指定了 --upload-only 或未指定 --download-only）`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Fprintf(os.Stderr, "错误：请至少指定一个 video_id\n")
			cmd.Help()
			os.Exit(1)
		}

		retryVideoIDs = args

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

		downloadsDir := cfg.Output.Directory
		if downloadsDir == "" {
			downloadsDir = "./downloads"
		}

		absDownloadsDir, err := filepath.Abs(downloadsDir)
		if err != nil {
			logger.Error().Err(err).Str("dir", downloadsDir).Msg("解析 downloads 目录路径失败")
			os.Exit(1)
		}

		fileRepo := file.NewRepository(downloadsDir)

		logger.Info().
			Strs("video_ids", retryVideoIDs).
			Str("downloads_dir", absDownloadsDir).
			Bool("download_only", retryDownloadOnly).
			Bool("upload_only", retryUploadOnly).
			Msg("开始处理重新下载和上传请求")

		// 遍历所有频道目录，查找匹配的 video_id
		channelEntries, err := os.ReadDir(absDownloadsDir)
		if err != nil {
			logger.Error().Err(err).Str("dir", absDownloadsDir).Msg("读取 downloads 目录失败")
			os.Exit(1)
		}

		foundCount := 0
		processedCount := 0
		errorCount := 0

		for _, channelEntry := range channelEntries {
			if !channelEntry.IsDir() {
				continue
			}

			channelID := channelEntry.Name()
			channelDir := filepath.Join(absDownloadsDir, channelID)

			// 跳过隐藏目录和特殊目录
			if strings.HasPrefix(channelID, ".") {
				continue
			}

			// 遍历该频道下的所有视频目录
			videoEntries, err := os.ReadDir(channelDir)
			if err != nil {
				logger.Debug().Err(err).Str("channel_dir", channelDir).Msg("读取频道目录失败，跳过")
				continue
			}

			for _, videoEntry := range videoEntries {
				if !videoEntry.IsDir() {
					continue
				}

				videoDir := filepath.Join(channelDir, videoEntry.Name())

				// 加载视频信息
				videoInfo, err := fileRepo.LoadVideoInfo(videoDir)
				if err != nil {
					continue
				}

				// 检查是否是我们要处理的 video_id
				shouldProcess := false
				for _, targetID := range retryVideoIDs {
					if videoInfo.ID == targetID {
						shouldProcess = true
						break
					}
				}

				if !shouldProcess {
					continue
				}

				foundCount++
				logger.Info().
					Str("video_id", videoInfo.ID).
					Str("title", videoInfo.Title).
					Str("video_dir", videoDir).
					Msg("找到匹配的视频，开始处理")

				// 清理状态
				if err := cleanupVideoStatus(videoDir, fileRepo, retryDownloadOnly, retryUploadOnly); err != nil {
					logger.Error().
						Err(err).
						Str("video_id", videoInfo.ID).
						Str("video_dir", videoDir).
						Msg("清理视频状态失败")
					errorCount++
					continue
				}

				// 重新下载（如果需要）
				if !retryUploadOnly {
					logger.Info().
						Str("video_id", videoInfo.ID).
						Str("video_dir", videoDir).
						Msg("开始重新下载视频")
					if err := application.DownloadService.DownloadVideoDir(ctx, videoDir); err != nil {
						logger.Error().
							Err(err).
							Str("video_id", videoInfo.ID).
							Str("video_dir", videoDir).
							Msg("重新下载视频失败")
						errorCount++
						continue
					}
					logger.Info().
						Str("video_id", videoInfo.ID).
						Str("video_dir", videoDir).
						Msg("重新下载视频完成")
				}

				// 重新上传（如果需要）
				if !retryDownloadOnly {
					logger.Info().
						Str("video_id", videoInfo.ID).
						Str("video_dir", videoDir).
						Msg("开始重新上传视频")
					// 获取账号名称
					accountName := retryAccount
					if accountName == "" {
						// 如果没有指定账号，使用第一个可用账号
						if len(cfg.BilibiliAccounts) > 0 {
							// 获取第一个账号名称（map 遍历顺序不确定，但至少有一个）
							for name := range cfg.BilibiliAccounts {
								accountName = name
								break
							}
						}
					}
					// 验证账号是否存在
					if accountName == "" {
						logger.Error().Msg("未配置B站账号，无法上传")
						errorCount++
						continue
					}
					if _, exists := cfg.BilibiliAccounts[accountName]; !exists {
						logger.Error().Str("account", accountName).Msg("指定的账号不存在")
						errorCount++
						continue
					}
					logger.Info().Str("account", accountName).Msg("使用指定账号上传")
					if err := application.UploadService.UploadSingleVideo(ctx, videoDir, accountName); err != nil {
						logger.Error().
							Err(err).
							Str("video_id", videoInfo.ID).
							Str("video_dir", videoDir).
							Str("account", accountName).
							Msg("重新上传视频失败")
						errorCount++
						continue
					}
					logger.Info().
						Str("video_id", videoInfo.ID).
						Str("video_dir", videoDir).
						Str("account", accountName).
						Msg("重新上传视频完成")
				}

				processedCount++
			}
		}

		// 检查是否有未找到的 video_id
		notFoundIDs := []string{}
		for _, targetID := range retryVideoIDs {
			found := false
			for _, channelEntry := range channelEntries {
				if !channelEntry.IsDir() {
					continue
				}
				channelID := channelEntry.Name()
				if strings.HasPrefix(channelID, ".") {
					continue
				}
				videoDir, _ := fileRepo.FindVideoDirByID(channelID, targetID)
				if videoDir != "" {
					if _, err := os.Stat(videoDir); err == nil {
						found = true
						break
					}
				}
			}
			if !found {
				notFoundIDs = append(notFoundIDs, targetID)
			}
		}

		// 输出总结
		logger.Info().
			Int("total_requested", len(retryVideoIDs)).
			Int("found", foundCount).
			Int("processed", processedCount).
			Int("errors", errorCount).
			Strs("not_found", notFoundIDs).
			Msg("处理完成")

		if len(notFoundIDs) > 0 {
			logger.Warn().
				Strs("video_ids", notFoundIDs).
				Msg("以下 video_id 未找到对应的视频目录")
		}

		if errorCount > 0 {
			os.Exit(1)
		}
	},
}

// cleanupVideoStatus 清理视频的下载和上传状态
func cleanupVideoStatus(videoDir string, fileRepo file.Repository, downloadOnly, uploadOnly bool) error {
	// 清理下载状态
	if !uploadOnly {
		// 重置下载状态为 downloading，允许重新下载
		videoInfo, err := fileRepo.LoadVideoInfo(videoDir)
		if err != nil {
			return fmt.Errorf("加载视频信息失败: %w", err)
		}
		videoURL := videoInfo.URL
		if videoURL == "" {
			videoURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoInfo.ID)
		}
		if err := fileRepo.MarkVideoDownloading(videoDir, videoURL); err != nil {
			return fmt.Errorf("重置下载状态失败: %w", err)
		}
		logger.Info().Str("video_dir", videoDir).Msg("已重置下载状态")
	}

	// 清理上传状态
	if !downloadOnly {
		uploadStatusFile := filepath.Join(videoDir, "upload_status.json")
		if _, err := os.Stat(uploadStatusFile); err == nil {
			if err := os.Remove(uploadStatusFile); err != nil {
				return fmt.Errorf("删除上传状态文件失败: %w", err)
			}
			logger.Info().Str("video_dir", videoDir).Msg("已删除上传状态文件")
		}
	}

	return nil
}

func init() {
	retryCmd.Flags().BoolVar(&retryDownloadOnly, "download-only", false, "只重新下载，不上传")
	retryCmd.Flags().BoolVar(&retryUploadOnly, "upload-only", false, "只重新上传，不下载")
	retryCmd.Flags().StringVar(&retryAccount, "account", "", "指定上传时使用的B站账号名称（如果不指定，使用配置中的第一个账号）")
	rootCmd.AddCommand(retryCmd)
}

