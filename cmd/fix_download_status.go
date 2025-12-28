package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"blueberry/internal/config"
	"blueberry/internal/repository/file"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var fixDownloadStatusCmd = &cobra.Command{
	Use:   "fix-download-status",
	Short: "修复下载状态：对于已上传但下载状态未标记的视频，更新其下载状态",
	Long: `修复下载状态：
对于所有已上传到B站的视频（有 upload_status.json 且 status 为 completed），
如果 download_status.json 中的 video.downloaded 不是 true，则更新为 true。

这通常用于修复统计不准确的问题。`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Get()
		if cfg == nil {
			fmt.Fprintf(os.Stderr, "配置未加载\n")
			os.Exit(1)
		}

		logger.SetLevel(zerolog.InfoLevel)

		// 获取 downloads 目录
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

		logger.Info().Str("downloads_dir", absDownloadsDir).Msg("开始修复下载状态")

		fixedCount := 0
		skippedCount := 0
		errorCount := 0

		// 遍历所有频道目录
		channelEntries, err := os.ReadDir(absDownloadsDir)
		if err != nil {
			logger.Error().Err(err).Str("dir", absDownloadsDir).Msg("读取 downloads 目录失败")
			os.Exit(1)
		}

		for _, channelEntry := range channelEntries {
			if !channelEntry.IsDir() {
				continue
			}

			channelID := channelEntry.Name()
			channelDir := filepath.Join(absDownloadsDir, channelID)

			// 跳过特殊目录
			if channelID == ".global" {
				continue
			}

			// 遍历频道下的所有视频目录
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

				// 检查是否已上传
				if !fileRepo.IsVideoUploaded(videoDir) {
					continue
				}

				// 检查下载状态
				if fileRepo.IsVideoDownloaded(videoDir) {
					skippedCount++
					continue
				}

				// 需要修复：已上传但下载状态未标记
				// 尝试找到视频文件路径
				videoPath, err := fileRepo.FindVideoFile(videoDir)
				if err != nil {
					// 即使找不到视频文件，也标记为已下载（因为已经上传了）
					videoPath = ""
				}

				// 更新下载状态
				if err := fileRepo.MarkVideoDownloadedWithPath(videoDir, videoPath); err != nil {
					logger.Warn().Err(err).Str("video_dir", videoDir).Msg("更新下载状态失败")
					errorCount++
					continue
				}

				fixedCount++
				logger.Info().
					Str("channel_id", channelID).
					Str("video_id", videoEntry.Name()).
					Str("video_path", videoPath).
					Msg("已修复下载状态")
			}
		}

		logger.Info().
			Int("fixed_count", fixedCount).
			Int("skipped_count", skippedCount).
			Int("error_count", errorCount).
			Msg("下载状态修复完成")
	},
}

func init() {
	rootCmd.AddCommand(fixDownloadStatusCmd)
}

