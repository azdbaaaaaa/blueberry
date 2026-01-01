package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"blueberry/internal/config"
	"blueberry/internal/repository/file"
	"blueberry/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	organizeForce   bool   // 强制重新处理，无视 .organized 标记
	organizeDateStr string // 指定归档目录的日期（格式：YYYYMMDD），默认为当前日期
)

// sanitizeFilename 清理文件名中的非法字符，确保文件名可以安全使用
// 移除或替换可能导致文件系统错误的字符
func sanitizeFilename(filename string) string {
	// 先确保是有效的 UTF-8 字符串
	if !utf8.ValidString(filename) {
		// 如果不是有效的 UTF-8，尝试修复
		filename = strings.ToValidUTF8(filename, "")
	}

	// 移除或替换非法字符
	var result strings.Builder
	for _, r := range filename {
		// 允许的字符：字母、数字、常见标点符号、中文字符等
		// 移除控制字符（0x00-0x1F，除了换行符等）和某些特殊字符
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			continue // 跳过控制字符
		}
		// 移除某些可能导致问题的字符
		if r == 0x7F || r == 0xFFFE || r == 0xFFFF {
			continue
		}
		// 替换某些可能导致问题的字符
		if r == '<' || r == '>' || r == ':' || r == '"' || r == '|' || r == '?' || r == '*' {
			result.WriteRune('_')
			continue
		}
		// 保留其他字符
		result.WriteRune(r)
	}

	return result.String()
}

var organizeCmd = &cobra.Command{
	Use:   "organize",
	Short: "整理 output 目录：生成流量汇总统计，移动流量统计文件，整理已上传视频的字幕文件",
	Long: `整理 output 目录：
1. 生成流量汇总统计（从详细文件中解析并汇总）
2. 移动流量统计文件到 output-{日期} 目录
3. 遍历 downloads 目录下已上传的视频，整理字幕文件到 output-{日期}/subtitles/ 目录
4. 如果字幕文件是旧格式（aid_lang.srt 或 video_id_*.lang.srt），会创建新格式（title[video_id].lang.srt）的副本
5. 在视频目录下创建 .organized 标记文件，避免重复处理`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.Get()
		if cfg == nil {
			fmt.Fprintf(os.Stderr, "配置未加载\n")
			os.Exit(1)
		}

		logger.SetLevel(zerolog.InfoLevel)

		// 获取 downloads 目录（用于遍历视频）
		downloadsDir := cfg.Output.Directory
		if downloadsDir == "" {
			downloadsDir = "./downloads"
		}

		// 获取项目根目录（output 目录的父目录）
		absDownloadsDir, err := filepath.Abs(downloadsDir)
		if err != nil {
			logger.Error().Err(err).Str("dir", downloadsDir).Msg("解析 downloads 目录路径失败")
			os.Exit(1)
		}
		projectRoot := filepath.Dir(absDownloadsDir)

		fileRepo := file.NewRepository(downloadsDir)

		// 创建归档目录（在项目根目录下）
		// 如果指定了日期，使用指定的日期；否则使用当前日期
		dateStr := organizeDateStr
		if dateStr == "" {
			dateStr = time.Now().Format("20060102")
		}
		// 验证日期格式（YYYYMMDD）
		if len(dateStr) != 8 {
			logger.Error().Str("date", dateStr).Msg("日期格式错误，应为 YYYYMMDD（例如：20251228）")
			os.Exit(1)
		}
		archiveDir := filepath.Join(projectRoot, fmt.Sprintf("output-%s", dateStr))
		subtitlesDir := filepath.Join(archiveDir, "subtitles")

		if err := os.MkdirAll(subtitlesDir, 0755); err != nil {
			logger.Error().Err(err).Str("dir", subtitlesDir).Msg("创建归档目录失败")
			os.Exit(1)
		}

		logger.Info().Str("archive_dir", archiveDir).Msg("开始整理 output 目录")

		// 1. 移动流量统计文件（从 output 目录）- 已移除，流量统计现在保存在 output-日期/traffic_stats/ 中

		// 2. 遍历 downloads 目录整理字幕文件
		downloadsStat, err := os.Stat(absDownloadsDir)
		if err != nil || !downloadsStat.IsDir() {
			logger.Warn().Str("dir", downloadsDir).Msg("downloads 目录不存在，跳过字幕整理")
			logger.Info().Str("archive_dir", archiveDir).Msg("output 目录整理完成")
			return
		}

		processedCount := 0 // 本次运行中成功处理并标记为已处理的视频目录数量
		skippedCount := 0
		skippedAlreadyOrganized := 0 // 已经有 .organized 标记的
		skippedNotUploaded := 0      // 还没有上传成功的
		totalScanned := 0            // 总共扫描的视频目录数量
		copiedCount := 0

		// 频道统计信息
		type ChannelStats struct {
			ChannelID              string `json:"channel_id"`
			TotalVideos            int    `json:"total_videos"`
			DownloadedVideos       int    `json:"downloaded_videos"`
			TotalSubtitles         int    `json:"total_subtitles"`
			UploadedVideos         int    `json:"uploaded_videos"`
			UploadedWithSubtitles  int    `json:"uploaded_with_subtitles"`
		}
		channelStatsList := make([]ChannelStats, 0)

		// 全局统计
		var globalStats struct {
			TotalVideos            int `json:"total_videos"`
			TotalDownloadedVideos  int `json:"total_downloaded_videos"`
			TotalUploadedVideos    int `json:"total_uploaded_videos"`
			TotalUploadedWithSubtitles int `json:"total_uploaded_with_subtitles"`
			TotalSubtitles         int `json:"total_subtitles"`
		}

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

			logger.Info().Str("channel_id", channelID).Msg("处理频道")

			// 初始化频道统计
			channelStats := ChannelStats{
				ChannelID:             channelID,
				TotalVideos:           0,
				DownloadedVideos:      0,
				TotalSubtitles:        0,
				UploadedVideos:        0,
				UploadedWithSubtitles: 0,
			}

			// 遍历频道下的所有视频目录
			videoEntries, err := os.ReadDir(channelDir)
			if err != nil {
				logger.Warn().Err(err).Str("channel_dir", channelDir).Msg("读取频道目录失败，跳过")
				continue
			}

			for _, videoEntry := range videoEntries {
				if !videoEntry.IsDir() {
					continue
				}

				totalScanned++ // 统计扫描的视频目录数量
				videoDir := filepath.Join(channelDir, videoEntry.Name())

				// 统计频道信息
				channelStats.TotalVideos++

				// 检查上传状态（先检查上传，因为已上传的视频肯定已经下载过）
				isUploaded := fileRepo.IsVideoUploaded(videoDir)
				if isUploaded {
					channelStats.UploadedVideos++
					globalStats.TotalUploadedVideos++
				}

				// 检查视频下载状态
				// 如果视频已上传，也应该算作已下载（因为上传前必须先下载）
				// 这样可以处理上传后删除原视频文件的情况
				isDownloaded := fileRepo.IsVideoDownloaded(videoDir) || isUploaded
				if isDownloaded {
					channelStats.DownloadedVideos++
					globalStats.TotalDownloadedVideos++
				}

				// 统计字幕数量
				subtitleFilesForStats, err := fileRepo.FindSubtitleFiles(videoDir)
				if err == nil {
					channelStats.TotalSubtitles += len(subtitleFilesForStats)
					globalStats.TotalSubtitles += len(subtitleFilesForStats)
					// 如果已上传且有字幕，统计 uploaded_with_subtitles
					if isUploaded && len(subtitleFilesForStats) > 0 {
						channelStats.UploadedWithSubtitles++
						globalStats.TotalUploadedWithSubtitles++
					}
				}

				// 更新全局统计
				globalStats.TotalVideos++

				// 检查是否已经 organize 过（除非使用 --force）
				organizeMarker := filepath.Join(videoDir, ".organized")
				if !organizeForce {
					if _, err := os.Stat(organizeMarker); err == nil {
						skippedAlreadyOrganized++
						skippedCount++
						continue
					}
				}

				// 检查上传状态
				uploadStatusFile := filepath.Join(videoDir, "upload_status.json")
				if !fileRepo.IsVideoUploaded(videoDir) {
					skippedNotUploaded++
					continue
				}

				// 读取 upload_status.json 获取 aid
				var uploadStatus map[string]interface{}
				uploadStatusData, err := os.ReadFile(uploadStatusFile)
				if err != nil {
					logger.Warn().Err(err).Str("video_dir", videoDir).Msg("读取 upload_status.json 失败，跳过")
					continue
				}
				if err := json.Unmarshal(uploadStatusData, &uploadStatus); err != nil {
					logger.Warn().Err(err).Str("video_dir", videoDir).Msg("解析 upload_status.json 失败，跳过")
					continue
				}

				aid := ""
				if aidVal, ok := uploadStatus["bilibili_aid"]; ok {
					if aidStr, ok := aidVal.(string); ok {
						aid = aidStr
					} else if aidNum, ok := aidVal.(float64); ok {
						aid = fmt.Sprintf("%.0f", aidNum)
					}
				}

				// 读取 video_info.json 获取 video_id 和 title
				videoInfo, err := fileRepo.LoadVideoInfo(videoDir)
				if err != nil {
					logger.Warn().Err(err).Str("video_dir", videoDir).Msg("读取 video_info.json 失败，跳过")
					continue
				}

				videoID := videoInfo.ID
				title := videoInfo.Title
				if videoID == "" || title == "" {
					logger.Warn().Str("video_dir", videoDir).Msg("视频信息不完整，跳过")
					continue
				}

				sanitizedTitle := fileRepo.SanitizeTitle(title)

				// 查找字幕文件
				subtitleFiles, err := fileRepo.FindSubtitleFiles(videoDir)
				if err != nil || len(subtitleFiles) == 0 {
					// 没有字幕文件，标记为已处理
					if err := os.WriteFile(organizeMarker, []byte(""), 0644); err == nil {
						processedCount++
					}
					continue
				}

				// 按语言组织字幕
				subtitleByLang := make(map[string]map[string]string) // lang -> {"new": file, "old": file}

				// 新格式正则：title[video_id].lang.ext
				escapedVideoID := regexp.QuoteMeta(videoID)
				newFormatPattern := regexp.MustCompile(fmt.Sprintf(`.*\[%s\]\.([a-zA-Z-]+)\.(srt|vtt)$`, escapedVideoID))

				// 旧格式正则：*.{lang}.srt（任何不匹配新格式的 *.lang.ext 都视为旧格式）
				oldFormatPattern := regexp.MustCompile(`^(.+)\.([a-zA-Z-]+)\.(srt|vtt)$`)

				for _, subtitleFile := range subtitleFiles {
					subtitleBase := filepath.Base(subtitleFile)

					// 检查是否是新格式
					if matches := newFormatPattern.FindStringSubmatch(subtitleBase); len(matches) > 0 {
						lang := matches[1]
						if subtitleByLang[lang] == nil {
							subtitleByLang[lang] = make(map[string]string)
						}
						subtitleByLang[lang]["new"] = subtitleFile
						continue
					}

					// 检查是否是旧格式：*.{lang}.srt
					if matches := oldFormatPattern.FindStringSubmatch(subtitleBase); len(matches) > 0 {
						lang := matches[2] // 语言代码是第二个捕获组
						// 验证语言代码格式（字母、数字、连字符，至少2个字符）
						if matched, _ := regexp.MatchString(`^[a-zA-Z0-9-]{2,}$`, lang); matched {
							if subtitleByLang[lang] == nil {
								subtitleByLang[lang] = make(map[string]string)
							}
							// 如果该语言还没有旧格式文件，或者当前文件更合适（优先选择更短的路径）
							if _, exists := subtitleByLang[lang]["old"]; !exists {
								subtitleByLang[lang]["old"] = subtitleFile
							}
						}
					}
				}

				// 创建按 aid 分组的字幕目录
				aidSubtitlesDir := subtitlesDir
				if aid != "" {
					aidSubtitlesDir = filepath.Join(subtitlesDir, aid)
					if err := os.MkdirAll(aidSubtitlesDir, 0755); err != nil {
						logger.Warn().Err(err).Str("aid", aid).Str("dir", aidSubtitlesDir).Msg("创建 aid 字幕目录失败，使用默认目录")
						aidSubtitlesDir = subtitlesDir
					}
				}

				// 处理每个语言的字幕
				for lang, files := range subtitleByLang {
					var subtitleFile string
					var subtitleExt string
					var needCreateNewFormat bool

					// 优先使用新格式
					if newFile, ok := files["new"]; ok {
						subtitleFile = newFile
						subtitleExt = filepath.Ext(subtitleFile)[1:] // 去掉点
						needCreateNewFormat = false
					} else if oldFile, ok := files["old"]; ok {
						// 如果新格式不存在，使用旧格式并创建新格式副本
						subtitleFile = oldFile
						subtitleExt = filepath.Ext(subtitleFile)[1:] // 去掉点
						needCreateNewFormat = true
					} else {
						continue
					}

					// 如果需要创建新格式，先创建副本
					if needCreateNewFormat {
						truncatedTitle := fileRepo.TruncateTitleForFilename(sanitizedTitle, videoID, lang, subtitleExt)
						newFormatSubtitle := fmt.Sprintf("%s[%s].%s.%s", truncatedTitle, videoID, lang, subtitleExt)
						// 清理文件名中的非法字符
						newFormatSubtitle = sanitizeFilename(newFormatSubtitle)
						newFormatPath := filepath.Join(videoDir, newFormatSubtitle)

						if _, err := os.Stat(newFormatPath); os.IsNotExist(err) {
							// 读取旧文件内容
							data, err := os.ReadFile(subtitleFile)
							if err == nil {
								if err := os.WriteFile(newFormatPath, data, 0644); err == nil {
									logger.Info().
										Str("file", newFormatSubtitle).
										Str("source", filepath.Base(subtitleFile)).
										Msg("已创建新格式字幕（从旧格式转换）")
									// 更新 subtitleFile 为新格式路径，后续复制时使用新格式
									subtitleFile = newFormatPath
								} else {
									logger.Warn().Err(err).Str("dest", newFormatPath).Msg("创建新格式字幕失败")
								}
							} else {
								logger.Warn().Err(err).Str("src", subtitleFile).Msg("读取旧格式字幕失败")
							}
						} else {
							// 新格式已存在，使用新格式
							subtitleFile = newFormatPath
						}
					}

					// 复制到 subtitles/{aid}/ 目录（使用新格式命名）
					if subtitleFile != "" {
						truncatedTitle := fileRepo.TruncateTitleForFilename(sanitizedTitle, videoID, lang, subtitleExt)
						destSubtitle := fmt.Sprintf("%s[%s].%s.%s", truncatedTitle, videoID, lang, subtitleExt)
						// 清理文件名中的非法字符
						destSubtitle = sanitizeFilename(destSubtitle)
						destPath := filepath.Join(aidSubtitlesDir, destSubtitle)

						// 读取源文件
						data, err := os.ReadFile(subtitleFile)
						if err == nil {
							if err := os.WriteFile(destPath, data, 0644); err == nil {
								copiedCount++
								logger.Info().
									Str("lang", lang).
									Str("dest", destPath).
									Str("aid", aid).
									Str("video_id", videoID).
									Msg("已复制字幕文件")
							} else {
								logger.Warn().Err(err).Str("dest", destPath).Msg("复制字幕失败")
							}
						} else {
							logger.Warn().Err(err).Str("src", subtitleFile).Msg("读取字幕文件失败")
						}
					}
				}

				// 标记为已处理
				if err := os.WriteFile(organizeMarker, []byte(""), 0644); err == nil {
					processedCount++
				}
			}

			// 将频道统计添加到列表
			channelStatsList = append(channelStatsList, channelStats)
			logger.Info().
				Str("channel_id", channelID).
				Int("total_videos", channelStats.TotalVideos).
				Int("downloaded_videos", channelStats.DownloadedVideos).
				Int("total_subtitles", channelStats.TotalSubtitles).
				Int("uploaded_videos", channelStats.UploadedVideos).
				Int("uploaded_with_subtitles", channelStats.UploadedWithSubtitles).
				Msg("频道统计")
		}

		// 保存频道统计到文件
		if len(channelStatsList) > 0 {
			statsData, err := json.MarshalIndent(channelStatsList, "", "  ")
			if err == nil {
				statsFile := filepath.Join(archiveDir, "channel_stats.json")
				if err := os.WriteFile(statsFile, statsData, 0644); err == nil {
					logger.Info().
						Str("stats_file", statsFile).
						Int("channel_count", len(channelStatsList)).
						Msg("已保存频道统计信息")
				} else {
					logger.Warn().Err(err).Str("stats_file", statsFile).Msg("保存频道统计信息失败")
				}
			} else {
				logger.Warn().Err(err).Msg("序列化频道统计信息失败")
			}
		}

		// 生成全局统计 JSON 文件（类似 sync 脚本中的格式）
		globalStatsData, err := json.MarshalIndent(globalStats, "", "  ")
		if err == nil {
			globalStatsFile := filepath.Join(archiveDir, "download_stats.json")
			if err := os.WriteFile(globalStatsFile, globalStatsData, 0644); err == nil {
				logger.Info().
					Str("stats_file", globalStatsFile).
					Int("total_videos", globalStats.TotalVideos).
					Int("total_downloaded_videos", globalStats.TotalDownloadedVideos).
					Int("total_uploaded_videos", globalStats.TotalUploadedVideos).
					Int("total_uploaded_with_subtitles", globalStats.TotalUploadedWithSubtitles).
					Int("total_subtitles", globalStats.TotalSubtitles).
					Msg("已保存全局下载统计信息")
			} else {
				logger.Warn().Err(err).Str("stats_file", globalStatsFile).Msg("保存全局下载统计信息失败")
			}
		} else {
			logger.Warn().Err(err).Msg("序列化全局下载统计信息失败")
		}

		logger.Info().
			Int("total_scanned", totalScanned).
			Int("processed", processedCount).
			Int("skipped", skippedCount).
			Int("skipped_already_organized", skippedAlreadyOrganized).
			Int("skipped_not_uploaded", skippedNotUploaded).
			Int("copied", copiedCount).
			Str("archive_dir", archiveDir).
			Msg("字幕整理完成")
		logger.Info().Str("archive_dir", archiveDir).Msg("output 目录整理完成")
	},
}

// generateNetworkStatsSummary 从详细统计文件中生成汇总统计（已废弃，流量统计现在使用 JSON 格式）
// 保留函数定义以避免编译错误，但不再使用
func generateNetworkStatsSummary(detailFile, dateStr, projectRoot string) error {
	return nil // 不再生成文本格式的汇总统计
	logger.Info().Str("detail_file", detailFile).Msg("生成流量汇总统计")

	// 读取详细文件内容
	content, err := os.ReadFile(detailFile)
	if err != nil {
		return fmt.Errorf("读取详细统计文件失败: %w", err)
	}

	contentStr := string(content)

	// 解析统计信息
	var (
		totalRxBytes    int64
		totalTxBytes    int64
		totalInterfaces int
		serverCount     int
	)

	// 正则表达式模式
	rxPattern := regexp.MustCompile(`总接收流量:\s+([0-9,]+)\s+字节`)
	txPattern := regexp.MustCompile(`总发送流量:\s+([0-9,]+)\s+字节`)
	interfacePattern := regexp.MustCompile(`网卡数量:\s+([0-9]+)`)
	serverPattern := regexp.MustCompile(`^===.*服务器:`)

	// 统计服务器数量
	serverMatches := serverPattern.FindAllString(contentStr, -1)
	serverCount = len(serverMatches)

	// 提取所有接收流量
	rxMatches := rxPattern.FindAllStringSubmatch(contentStr, -1)
	for _, match := range rxMatches {
		if len(match) > 1 {
			rxStr := strings.ReplaceAll(match[1], ",", "")
			if rxBytes, err := strconv.ParseInt(rxStr, 10, 64); err == nil {
				totalRxBytes += rxBytes
			}
		}
	}

	// 提取所有发送流量
	txMatches := txPattern.FindAllStringSubmatch(contentStr, -1)
	for _, match := range txMatches {
		if len(match) > 1 {
			txStr := strings.ReplaceAll(match[1], ",", "")
			if txBytes, err := strconv.ParseInt(txStr, 10, 64); err == nil {
				totalTxBytes += txBytes
			}
		}
	}

	// 提取所有网卡数量
	interfaceMatches := interfacePattern.FindAllStringSubmatch(contentStr, -1)
	for _, match := range interfaceMatches {
		if len(match) > 1 {
			if ifCount, err := strconv.Atoi(match[1]); err == nil {
				totalInterfaces += ifCount
			}
		}
	}

	if serverCount == 0 {
		return fmt.Errorf("未找到服务器统计信息")
	}

	// 格式化汇总信息
	totalBytes := totalRxBytes + totalTxBytes
	totalRxGB := float64(totalRxBytes) / 1073741824.0
	totalTxGB := float64(totalTxBytes) / 1073741824.0
	totalRxTB := float64(totalRxBytes) / 1099511627776.0
	totalTxTB := float64(totalTxBytes) / 1099511627776.0
	totalGB := float64(totalBytes) / 1073741824.0
	totalTB := float64(totalBytes) / 1099511627776.0

	// 格式化数字（添加千分位）
	formatNumber := func(n int64) string {
		s := strconv.FormatInt(n, 10)
		var result strings.Builder
		for i, r := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				result.WriteString(",")
			}
			result.WriteRune(r)
		}
		return result.String()
	}

	// 生成汇总文件内容
	summary := fmt.Sprintf(`========================================
网卡流量统计汇总
========================================
收集时间: %s
服务器数量: %d
总网卡数量: %d

总下行流量（接收）: %s 字节 (%.2f GB / %.2f TB)
总上行流量（发送）: %s 字节 (%.2f GB / %.2f TB)
总流量: %s 字节 (%.2f GB / %.2f TB)

`,
		time.Now().Format("2006-01-02 15:04:05"),
		serverCount,
		totalInterfaces,
		formatNumber(totalRxBytes),
		totalRxGB,
		totalRxTB,
		formatNumber(totalTxBytes),
		totalTxGB,
		totalTxTB,
		formatNumber(totalBytes),
		totalGB,
		totalTB,
	)

	// 写入汇总文件（先写入 output 目录，后续会被移动到归档目录）
	summaryFile := filepath.Join(projectRoot, "output", fmt.Sprintf("1.network_stats_summary_%s.txt", dateStr))
	if err := os.WriteFile(summaryFile, []byte(summary), 0644); err != nil {
		return fmt.Errorf("写入汇总文件失败: %w", err)
	}

	logger.Info().
		Int("server_count", serverCount).
		Int64("total_rx_bytes", totalRxBytes).
		Int64("total_tx_bytes", totalTxBytes).
		Float64("total_rx_gb", totalRxGB).
		Float64("total_tx_gb", totalTxGB).
		Str("summary_file", summaryFile).
		Msg("网卡流量汇总统计已生成")

	return nil
}

func init() {
	organizeCmd.Flags().BoolVar(&organizeForce, "force", false, "强制重新处理，无视 .organized 标记文件")
	organizeCmd.Flags().StringVar(&organizeDateStr, "date", "", "指定归档目录的日期（格式：YYYYMMDD，例如：20251228），默认为当前日期")
	rootCmd.AddCommand(organizeCmd)
}
