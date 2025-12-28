package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"blueberry/internal/repository/youtube"
	"blueberry/pkg/logger"
)

// SubtitleStatus 字幕状态
type SubtitleStatus string

const (
	SubtitleStatusDownloaded SubtitleStatus = "downloaded" // 已下载
	SubtitleStatusNotFound   SubtitleStatus = "not_found"  // 不存在（该语言没有字幕）
	SubtitleStatusFailed     SubtitleStatus = "failed"     // 下载失败
	SubtitleStatusPending    SubtitleStatus = "pending"    // 待下载
)

// SubtitleStatusRecord 字幕状态记录
type SubtitleStatusRecord struct {
	VideoDir string                        `json:"video_dir"`
	VideoID  string                        `json:"video_id"`
	VideoURL string                        `json:"video_url"`
	Statuses map[string]SubtitleStatusInfo `json:"statuses"` // key: language code
}

// SubtitleStatusInfo 单个语言的字幕状态信息
type SubtitleStatusInfo struct {
	Status    SubtitleStatus `json:"status"`
	FilePath  string         `json:"file_path,omitempty"` // 字幕文件路径（如果已下载）
	ErrorMsg  string         `json:"error_msg,omitempty"` // 错误信息（如果失败）
	UpdatedAt string         `json:"updated_at"`          // 更新时间
}

// subtitleStatusFile 字幕状态文件管理器
type subtitleStatusFile struct {
	filePath string
	records  map[string]*SubtitleStatusRecord // key: video_dir
}

func newSubtitleStatusFile(outputDir string) *subtitleStatusFile {
	globalDir := filepath.Join(outputDir, ".global")
	_ = os.MkdirAll(globalDir, 0755)
	filePath := filepath.Join(globalDir, "subtitle_status.json")
	return &subtitleStatusFile{
		filePath: filePath,
		records:  make(map[string]*SubtitleStatusRecord),
	}
}

func (s *subtitleStatusFile) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，使用空记录
			return nil
		}
		return fmt.Errorf("读取字幕状态文件失败: %w", err)
	}

	if len(data) == 0 {
		return nil
	}

	var records []*SubtitleStatusRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("解析字幕状态文件失败: %w", err)
	}

	for _, record := range records {
		s.records[record.VideoDir] = record
	}

	return nil
}

func (s *subtitleStatusFile) save() error {
	records := make([]*SubtitleStatusRecord, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化字幕状态文件失败: %w", err)
	}

	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("保存字幕状态文件失败: %w", err)
	}

	return nil
}

func (s *subtitleStatusFile) getRecord(videoDir string) *SubtitleStatusRecord {
	if record, ok := s.records[videoDir]; ok {
		return record
	}
	return nil
}

func (s *subtitleStatusFile) setRecord(record *SubtitleStatusRecord) {
	s.records[record.VideoDir] = record
}

func (s *subtitleStatusFile) updateStatus(videoDir, videoID, videoURL, lang string, status SubtitleStatus, filePath, errorMsg string) {
	record := s.getRecord(videoDir)
	if record == nil {
		record = &SubtitleStatusRecord{
			VideoDir: videoDir,
			VideoID:  videoID,
			VideoURL: videoURL,
			Statuses: make(map[string]SubtitleStatusInfo),
		}
	}

	info := SubtitleStatusInfo{
		Status:    status,
		FilePath:  filePath,
		ErrorMsg:  errorMsg,
		UpdatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	record.Statuses[lang] = info
	record.VideoID = videoID
	record.VideoURL = videoURL

	s.setRecord(record)
}

// FixSubtitles 补充缺失的字幕文件（处理所有频道）
func (s *downloadService) FixSubtitles(ctx context.Context) error {
	// 获取输出目录
	outputDir := s.cfg.Output.Directory
	if outputDir == "" {
		return fmt.Errorf("配置文件中未设置输出目录")
	}

	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("解析输出目录路径失败: %w", err)
	}

	logger.Info().Str("output_dir", absOutputDir).Msg("开始补充字幕文件（所有频道）")

	// 遍历所有频道目录
	entries, err := os.ReadDir(absOutputDir)
	if err != nil {
		return fmt.Errorf("读取输出目录失败: %w", err)
	}

	var channelDirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		channelDir := filepath.Join(absOutputDir, entry.Name())
		channelInfoPath := filepath.Join(channelDir, "channel_info.json")

		// 检查是否是频道目录（包含 channel_info.json）
		if _, err := os.Stat(channelInfoPath); err == nil {
			channelDirs = append(channelDirs, channelDir)
		}
	}

	if len(channelDirs) == 0 {
		logger.Info().Msg("未找到任何频道目录")
		return nil
	}

	logger.Info().Int("count", len(channelDirs)).Msg("找到频道目录")

	// 处理每个频道
	for i, dir := range channelDirs {
		logger.Info().
			Int("current", i+1).
			Int("total", len(channelDirs)).
			Str("channel_dir", dir).
			Msg("开始处理频道")
		if err := s.FixSubtitlesForChannelDir(ctx, dir); err != nil {
			logger.Error().
				Str("channel_dir", dir).
				Err(err).
				Msg("处理频道失败，继续处理下一个")
			continue
		}
	}

	return nil
}

// FixSubtitlesForChannelDir 补充指定频道目录的字幕文件
func (s *downloadService) FixSubtitlesForChannelDir(ctx context.Context, channelDir string) error {
	logger.Info().Str("channel_dir", channelDir).Msg("开始补充字幕文件（指定频道）")

	// 获取输出目录（用于状态文件路径）
	outputDir := s.cfg.Output.Directory
	if outputDir == "" {
		return fmt.Errorf("配置文件中未设置输出目录")
	}

	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("解析输出目录路径失败: %w", err)
	}

	// 加载字幕状态文件
	statusFile := newSubtitleStatusFile(absOutputDir)
	if err := statusFile.load(); err != nil {
		logger.Warn().Err(err).Msg("加载字幕状态文件失败，将创建新文件")
	}

	// 获取默认字幕语言列表
	languages := s.getDefaultSubtitleLanguages()
	logger.Info().Strs("languages", languages).Msg("需要检查的字幕语言")

	// 检查是否是频道目录
	channelInfoPath := filepath.Join(channelDir, "channel_info.json")
	if _, err := os.Stat(channelInfoPath); err != nil {
		return fmt.Errorf("指定的目录不是频道目录（缺少 channel_info.json）: %s", channelDir)
	}

	channelID := filepath.Base(channelDir)
	logger.Info().Str("channel_id", channelID).Msg("处理频道")

	// 遍历频道下的所有视频目录
	videoEntries, err := os.ReadDir(channelDir)
	if err != nil {
		return fmt.Errorf("读取频道目录失败: %w", err)
	}

	totalVideos := 0
	processedVideos := 0
	downloadedSubtitles := 0
	skippedSubtitles := 0
	failedSubtitles := 0

	for _, videoEntry := range videoEntries {
		if !videoEntry.IsDir() {
			continue
		}

		videoDirName := videoEntry.Name()
		// 跳过特殊目录
		if videoDirName == ".global" {
			continue
		}

		videoDir := filepath.Join(channelDir, videoDirName)
		totalVideos++

		// 检查是否有 video_info.json
		videoInfoPath := filepath.Join(videoDir, "video_info.json")
		if _, err := os.Stat(videoInfoPath); err != nil {
			logger.Debug().Str("video_dir", videoDir).Msg("跳过：没有 video_info.json")
			continue
		}

		// 加载视频信息
		videoInfo, err := s.fileManager.LoadVideoInfo(videoDir)
		if err != nil {
			logger.Warn().Err(err).Str("video_dir", videoDir).Msg("加载视频信息失败")
			continue
		}

		videoID := videoInfo.ID
		videoURL := videoInfo.URL
		if videoURL == "" {
			videoURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
		}

		// 检查视频下载状态，只处理下载成功的视频
		videoStatus, videoDownloaded, _, err := s.fileManager.GetDownloadVideoStatus(videoDir)
		if err != nil {
			logger.Debug().
				Str("video_id", videoID).
				Str("video_dir", videoDir).
				Err(err).
				Msg("无法读取视频下载状态，跳过（可能未开始下载）")
			continue
		}

		// 只有视频下载成功（状态为 completed 且 downloaded 为 true）才处理字幕
		if videoStatus != "completed" || !videoDownloaded {
			logger.Debug().
				Str("video_id", videoID).
				Str("title", videoInfo.Title).
				Str("status", videoStatus).
				Bool("downloaded", videoDownloaded).
				Msg("视频下载未成功，跳过字幕补充（由下载逻辑处理）")
			continue
		}

		processedVideos++
		logger.Info().
			Int("current", processedVideos).
			Str("video_id", videoID).
			Str("title", videoInfo.Title).
			Msg("处理视频（视频已下载成功）")

		// 获取或创建状态记录
		record := statusFile.getRecord(videoDir)
		if record == nil {
			record = &SubtitleStatusRecord{
				VideoDir: videoDir,
				VideoID:  videoID,
				VideoURL: videoURL,
				Statuses: make(map[string]SubtitleStatusInfo),
			}
		}

		// 快速检查：如果所有语言都已完成（downloaded 或 not_found），可以提前跳过
		allCompleted := true
		if record != nil && len(record.Statuses) > 0 {
			for _, lang := range languages {
				statusInfo, hasStatus := record.Statuses[lang]
				if !hasStatus {
					allCompleted = false
					break
				}
				if statusInfo.Status != SubtitleStatusDownloaded && statusInfo.Status != SubtitleStatusNotFound {
					allCompleted = false
					break
				}
				// 如果是 downloaded，还需要验证文件是否存在
				if statusInfo.Status == SubtitleStatusDownloaded {
					if statusInfo.FilePath == "" {
						allCompleted = false
						break
					}
					if _, err := os.Stat(statusInfo.FilePath); err != nil {
						allCompleted = false
						break
					}
				}
			}
		} else {
			allCompleted = false
		}

		// 如果所有语言都已完成，检查是否所有新格式文件都存在
		if allCompleted {
			allFilesExist := true
			for _, lang := range languages {
				sanitizedTitle := sanitizeTitle(videoInfo.Title)
				truncatedTitle := truncateTitleForFilename(sanitizedTitle, videoID, lang, ".srt")
				expectedNewFormatName := fmt.Sprintf("%s[%s].%s.srt", truncatedTitle, videoID, lang)
				expectedNewFormatPath := filepath.Join(videoDir, expectedNewFormatName)
				if _, err := os.Stat(expectedNewFormatPath); err != nil {
					// 如果某个语言标记为 not_found，文件不存在是正常的
					if langStatusInfo, hasStatus := record.Statuses[lang]; hasStatus && langStatusInfo.Status == SubtitleStatusNotFound {
						continue
					}
					allFilesExist = false
					break
				}
			}
			if allFilesExist {
				logger.Debug().
					Str("video_id", videoID).
					Str("title", videoInfo.Title).
					Msg("所有字幕已完成，跳过处理")
				skippedSubtitles += len(languages)
				continue
			}
		}

		// 先检查每个语言的字幕，收集需要下载的语言列表
		needDownloadLangs := make([]string, 0)
		subtitleFiles, _ := s.fileManager.FindSubtitleFiles(videoDir)

		for _, lang := range languages {
			// 检查状态记录
			statusInfo, hasStatus := record.Statuses[lang]

			// 如果状态是已下载或不存在，跳过
			if hasStatus {
				if statusInfo.Status == SubtitleStatusDownloaded {
					// 验证文件是否还存在
					if statusInfo.FilePath != "" {
						if _, err := os.Stat(statusInfo.FilePath); err == nil {
							logger.Debug().
								Str("video_id", videoID).
								Str("lang", lang).
								Msg("字幕已下载，跳过")
							skippedSubtitles++
							continue
						}
						// 文件不存在，需要重新下载
						logger.Info().
							Str("video_id", videoID).
							Str("lang", lang).
							Str("file_path", statusInfo.FilePath).
							Msg("字幕文件已丢失，需要重新下载")
						needDownloadLangs = append(needDownloadLangs, lang)
						continue
					}
				} else if statusInfo.Status == SubtitleStatusNotFound {
					// 该语言没有字幕，跳过
					logger.Debug().
						Str("video_id", videoID).
						Str("lang", lang).
						Msg("该语言没有字幕，跳过")
					skippedSubtitles++
					continue
				}
				// 如果是 failed 或 pending，继续尝试下载
			}

			// 检查是否已有新格式的字幕文件：title[video_id].lang.srt
			sanitizedTitle := sanitizeTitle(videoInfo.Title)
			truncatedTitle := truncateTitleForFilename(sanitizedTitle, videoID, lang, ".srt")
			expectedNewFormatName := fmt.Sprintf("%s[%s].%s.srt", truncatedTitle, videoID, lang)
			expectedNewFormatPath := filepath.Join(videoDir, expectedNewFormatName)
			if _, err := os.Stat(expectedNewFormatPath); err == nil {
				// 新格式文件已存在
				statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusDownloaded, expectedNewFormatPath, "")
				logger.Debug().
					Str("video_id", videoID).
					Str("lang", lang).
					Str("file_path", expectedNewFormatPath).
					Msg("新格式字幕文件已存在，跳过")
				skippedSubtitles++
				continue
			}

			// 检查是否有旧格式的字幕文件，如果有则复制为新格式
			var oldFormatPath string
			for _, subPath := range subtitleFiles {
				base := filepath.Base(subPath)
				// 跳过已经是新格式的文件（检查原始标题和截断后的标题）
				if strings.HasPrefix(base, sanitizedTitle+"["+videoID+"]") || strings.HasPrefix(base, truncatedTitle+"["+videoID+"]") {
					continue
				}
				// 检查文件名中是否包含该语言代码
				// 支持多种旧格式：
				// - title.lang.srt
				// - video_id_lang.srt
				// - title-lang.srt
				// - title_lang.srt
				// - aid_lang.srt
				if strings.Contains(base, "."+lang+".") ||
					strings.Contains(base, "-"+lang+".") ||
					strings.Contains(base, "_"+lang+".") ||
					strings.HasSuffix(base, "."+lang+".srt") ||
					strings.HasSuffix(base, "-"+lang+".srt") ||
					strings.HasSuffix(base, "_"+lang+".srt") {
					oldFormatPath = subPath
					break
				}
			}

			if oldFormatPath != "" {
				// 找到旧格式字幕文件，复制为新格式
				if err := s.copyFile(oldFormatPath, expectedNewFormatPath); err != nil {
					logger.Warn().
						Err(err).
						Str("video_id", videoID).
						Str("lang", lang).
						Str("old_path", oldFormatPath).
						Str("new_path", expectedNewFormatPath).
						Msg("复制旧格式字幕文件失败")
					// 复制失败，继续尝试下载
					needDownloadLangs = append(needDownloadLangs, lang)
				} else {
					// 复制成功
					statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusDownloaded, expectedNewFormatPath, "")
					logger.Info().
						Str("video_id", videoID).
						Str("lang", lang).
						Str("old_path", oldFormatPath).
						Str("new_path", expectedNewFormatPath).
						Msg("已从旧格式复制为新格式字幕文件")
					downloadedSubtitles++
					// 保存状态文件
					if err := statusFile.save(); err != nil {
						logger.Warn().Err(err).Msg("保存字幕状态文件失败")
					}
					continue
				}
			} else {
				// 没有找到旧格式，需要下载
				needDownloadLangs = append(needDownloadLangs, lang)
			}
		}

		// 如果有需要下载的语言，一次性下载所有缺失的语言
		if len(needDownloadLangs) > 0 {
			// 更新所有语言的状态为 pending
			for _, lang := range needDownloadLangs {
				statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusPending, "", "")
			}

			logger.Info().
				Str("video_id", videoID).
				Strs("languages", needDownloadLangs).
				Msg("开始下载字幕")

			// 一次性下载所有缺失的语言
			subtitleResults, err := s.downloadSubtitlesOnly(ctx, videoDir, videoURL, needDownloadLangs, videoInfo.Title, videoID)
			if err != nil {
				// 下载失败，记录所有语言为 failed
				errorMsg := err.Error()
				for _, lang := range needDownloadLangs {
					statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusFailed, "", errorMsg)
					failedSubtitles++
				}
				logger.Error().
					Err(err).
					Str("video_id", videoID).
					Strs("languages", needDownloadLangs).
					Msg("下载字幕失败")
			} else {
				// 处理下载结果
				for lang, result := range subtitleResults {
					if result.err != nil {
						// 检查是否是"该语言没有字幕"的错误
						errorMsg := result.err.Error()
						if strings.Contains(errorMsg, "not available") ||
							strings.Contains(errorMsg, "not found") ||
							strings.Contains(errorMsg, "no subtitle") ||
							strings.Contains(errorMsg, "未找到该语言的字幕文件") ||
							strings.Contains(errorMsg, "该语言没有字幕") {
							// 该语言没有字幕，记录为 not_found
							statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusNotFound, "", errorMsg)
							logger.Info().
								Str("video_id", videoID).
								Str("lang", lang).
								Msg("该语言没有字幕")
							skippedSubtitles++
						} else {
							// 下载失败，记录为 failed
							statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusFailed, "", errorMsg)
							logger.Error().
								Err(result.err).
								Str("video_id", videoID).
								Str("lang", lang).
								Msg("下载字幕失败")
							failedSubtitles++
						}
					} else if result.filePath != "" {
						// 下载成功
						statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusDownloaded, result.filePath, "")
						logger.Info().
							Str("video_id", videoID).
							Str("lang", lang).
							Str("file_path", result.filePath).
							Msg("字幕下载成功")
						downloadedSubtitles++
					}
				}
			}

			// 保存状态文件
			if err := statusFile.save(); err != nil {
				logger.Warn().Err(err).Msg("保存字幕状态文件失败")
			}
		}
	}

	// 最终保存状态文件
	if err := statusFile.save(); err != nil {
		logger.Warn().Err(err).Msg("最终保存字幕状态文件失败")
	}

	logger.Info().
		Int("total_videos", totalVideos).
		Int("processed_videos", processedVideos).
		Int("downloaded_subtitles", downloadedSubtitles).
		Int("skipped_subtitles", skippedSubtitles).
		Int("failed_subtitles", failedSubtitles).
		Msg("字幕补充完成")

	return nil
}

// FixSubtitlesForVideoDir 补充指定视频目录的字幕文件
func (s *downloadService) FixSubtitlesForVideoDir(ctx context.Context, videoDir string) error {
	logger.Info().Str("video_dir", videoDir).Msg("开始补充字幕文件（指定视频）")

	// 获取输出目录（用于状态文件路径）
	outputDir := s.cfg.Output.Directory
	if outputDir == "" {
		return fmt.Errorf("配置文件中未设置输出目录")
	}

	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("解析输出目录路径失败: %w", err)
	}

	// 加载字幕状态文件
	statusFile := newSubtitleStatusFile(absOutputDir)
	if err := statusFile.load(); err != nil {
		logger.Warn().Err(err).Msg("加载字幕状态文件失败，将创建新文件")
	}

	// 获取默认字幕语言列表
	languages := s.getDefaultSubtitleLanguages()
	logger.Info().Strs("languages", languages).Msg("需要检查的字幕语言")

	// 检查是否有 video_info.json
	videoInfoPath := filepath.Join(videoDir, "video_info.json")
	if _, err := os.Stat(videoInfoPath); err != nil {
		return fmt.Errorf("指定的目录不是视频目录（缺少 video_info.json）: %s", videoDir)
	}

	// 加载视频信息
	videoInfo, err := s.fileManager.LoadVideoInfo(videoDir)
	if err != nil {
		return fmt.Errorf("加载视频信息失败: %w", err)
	}

	videoID := videoInfo.ID
	videoURL := videoInfo.URL
	if videoURL == "" {
		videoURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	}

	// 检查视频下载状态，只处理下载成功的视频
	videoStatus, videoDownloaded, _, err := s.fileManager.GetDownloadVideoStatus(videoDir)
	if err != nil {
		return fmt.Errorf("无法读取视频下载状态: %w", err)
	}

	// 只有视频下载成功（状态为 completed 且 downloaded 为 true）才处理字幕
	if videoStatus != "completed" || !videoDownloaded {
		return fmt.Errorf("视频下载未成功（状态: %s, downloaded: %v），跳过字幕补充（由下载逻辑处理）", videoStatus, videoDownloaded)
	}

	logger.Info().
		Str("video_id", videoID).
		Str("title", videoInfo.Title).
		Msg("处理视频（视频已下载成功）")

	// 获取或创建状态记录
	record := statusFile.getRecord(videoDir)
	if record == nil {
		record = &SubtitleStatusRecord{
			VideoDir: videoDir,
			VideoID:  videoID,
			VideoURL: videoURL,
			Statuses: make(map[string]SubtitleStatusInfo),
		}
	}

	// 先检查每个语言的字幕，收集需要下载的语言列表
	needDownloadLangs := make([]string, 0)
	subtitleFiles, _ := s.fileManager.FindSubtitleFiles(videoDir)

	downloadedSubtitles := 0
	skippedSubtitles := 0
	failedSubtitles := 0

	for _, lang := range languages {
		// 检查状态记录
		statusInfo, hasStatus := record.Statuses[lang]

		// 如果状态是已下载或不存在，跳过
		if hasStatus {
			if statusInfo.Status == SubtitleStatusDownloaded {
				// 验证文件是否还存在
				if statusInfo.FilePath != "" {
					if _, err := os.Stat(statusInfo.FilePath); err == nil {
						logger.Debug().
							Str("video_id", videoID).
							Str("lang", lang).
							Msg("字幕已下载，跳过")
						skippedSubtitles++
						continue
					}
					// 文件不存在，需要重新下载
					logger.Info().
						Str("video_id", videoID).
						Str("lang", lang).
						Str("file_path", statusInfo.FilePath).
						Msg("字幕文件已丢失，需要重新下载")
					needDownloadLangs = append(needDownloadLangs, lang)
					continue
				}
			} else if statusInfo.Status == SubtitleStatusNotFound {
				// 该语言没有字幕，跳过
				logger.Debug().
					Str("video_id", videoID).
					Str("lang", lang).
					Msg("该语言没有字幕，跳过")
				skippedSubtitles++
				continue
			}
			// 如果是 failed 或 pending，继续尝试下载
		}

		// 检查是否已有新格式的字幕文件：title[video_id].lang.srt
		sanitizedTitle := sanitizeTitle(videoInfo.Title)
		truncatedTitle := truncateTitleForFilename(sanitizedTitle, videoID, lang, ".srt")
		expectedNewFormatName := fmt.Sprintf("%s[%s].%s.srt", truncatedTitle, videoID, lang)
		expectedNewFormatPath := filepath.Join(videoDir, expectedNewFormatName)
		if _, err := os.Stat(expectedNewFormatPath); err == nil {
			// 新格式文件已存在
			statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusDownloaded, expectedNewFormatPath, "")
			logger.Debug().
				Str("video_id", videoID).
				Str("lang", lang).
				Str("file_path", expectedNewFormatPath).
				Msg("新格式字幕文件已存在，跳过")
			skippedSubtitles++
			continue
		}

		// 检查是否有旧格式的字幕文件，如果有则复制为新格式
		var oldFormatPath string
		for _, subPath := range subtitleFiles {
			base := filepath.Base(subPath)
			// 跳过已经是新格式的文件（检查原始标题和截断后的标题）
			if strings.HasPrefix(base, sanitizedTitle+"["+videoID+"]") || strings.HasPrefix(base, truncatedTitle+"["+videoID+"]") {
				continue
			}
			// 检查文件名中是否包含该语言代码
			if strings.Contains(base, "."+lang+".") ||
				strings.Contains(base, "-"+lang+".") ||
				strings.Contains(base, "_"+lang+".") ||
				strings.HasSuffix(base, "."+lang+".srt") ||
				strings.HasSuffix(base, "-"+lang+".srt") ||
				strings.HasSuffix(base, "_"+lang+".srt") {
				oldFormatPath = subPath
				break
			}
		}

		if oldFormatPath != "" {
			// 找到旧格式字幕文件，复制为新格式
			if err := s.copyFile(oldFormatPath, expectedNewFormatPath); err != nil {
				logger.Warn().
					Err(err).
					Str("video_id", videoID).
					Str("lang", lang).
					Str("old_path", oldFormatPath).
					Str("new_path", expectedNewFormatPath).
					Msg("复制旧格式字幕文件失败")
				// 复制失败，继续尝试下载
				needDownloadLangs = append(needDownloadLangs, lang)
			} else {
				// 复制成功
				statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusDownloaded, expectedNewFormatPath, "")
				logger.Info().
					Str("video_id", videoID).
					Str("lang", lang).
					Str("old_path", oldFormatPath).
					Str("new_path", expectedNewFormatPath).
					Msg("已从旧格式复制为新格式字幕文件")
				downloadedSubtitles++
				// 保存状态文件
				if err := statusFile.save(); err != nil {
					logger.Warn().Err(err).Msg("保存字幕状态文件失败")
				}
				continue
			}
		} else {
			// 没有找到旧格式，需要下载
			needDownloadLangs = append(needDownloadLangs, lang)
		}
	}

	// 如果有需要下载的语言，一次性下载所有缺失的语言
	if len(needDownloadLangs) > 0 {
		// 更新所有语言的状态为 pending
		for _, lang := range needDownloadLangs {
			statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusPending, "", "")
		}

		logger.Info().
			Str("video_id", videoID).
			Strs("languages", needDownloadLangs).
			Msg("开始下载字幕")

		// 一次性下载所有缺失的语言
		subtitleResults, err := s.downloadSubtitlesOnly(ctx, videoDir, videoURL, needDownloadLangs, videoInfo.Title, videoID)
		if err != nil {
			// 下载失败，记录所有语言为 failed
			errorMsg := err.Error()
			for _, lang := range needDownloadLangs {
				statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusFailed, "", errorMsg)
				failedSubtitles++
			}
			logger.Error().
				Err(err).
				Str("video_id", videoID).
				Strs("languages", needDownloadLangs).
				Msg("下载字幕失败")
		} else {
			// 处理下载结果
			for lang, result := range subtitleResults {
				if result.err != nil {
					// 检查是否是"该语言没有字幕"的错误
					errorMsg := result.err.Error()
					if strings.Contains(errorMsg, "not available") ||
						strings.Contains(errorMsg, "not found") ||
						strings.Contains(errorMsg, "no subtitle") ||
						strings.Contains(errorMsg, "未找到该语言的字幕文件") ||
						strings.Contains(errorMsg, "该语言没有字幕") {
						// 该语言没有字幕，记录为 not_found
						statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusNotFound, "", errorMsg)
						logger.Info().
							Str("video_id", videoID).
							Str("lang", lang).
							Msg("该语言没有字幕")
						skippedSubtitles++
					} else {
						// 下载失败，记录为 failed
						statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusFailed, "", errorMsg)
						logger.Error().
							Err(result.err).
							Str("video_id", videoID).
							Str("lang", lang).
							Msg("下载字幕失败")
						failedSubtitles++
					}
				} else if result.filePath != "" {
					// 下载成功
					statusFile.updateStatus(videoDir, videoID, videoURL, lang, SubtitleStatusDownloaded, result.filePath, "")
					logger.Info().
						Str("video_id", videoID).
						Str("lang", lang).
						Str("file_path", result.filePath).
						Msg("字幕下载成功")
					downloadedSubtitles++
				}
			}
		}

		// 保存状态文件
		if err := statusFile.save(); err != nil {
			logger.Warn().Err(err).Msg("保存字幕状态文件失败")
		}
	}

	logger.Info().
		Str("video_id", videoID).
		Int("downloaded_subtitles", downloadedSubtitles).
		Int("skipped_subtitles", skippedSubtitles).
		Int("failed_subtitles", failedSubtitles).
		Msg("视频字幕补充完成")

	return nil
}

// subtitleDownloadResult 字幕下载结果
type subtitleDownloadResult struct {
	filePath string
	err      error
}

// downloadSubtitlesOnly 只下载字幕，不下载视频（一次性下载多个语言）
func (s *downloadService) downloadSubtitlesOnly(ctx context.Context, videoDir, videoURL string, languages []string, title, videoID string) (map[string]subtitleDownloadResult, error) {
	if len(languages) == 0 {
		return make(map[string]subtitleDownloadResult), nil
	}

	// 构建 yt-dlp 命令参数（只下载字幕）
	args := []string{
		"--skip-download", // 跳过视频下载
		"--write-sub",
		"--write-auto-sub",
		"--sub-langs", strings.Join(languages, ","), // 一次性指定所有语言
		"--convert-subs", "srt", // 转换为 SRT 格式
		"-o", filepath.Join(videoDir, "%(title)s.%(ext)s"), // 输出路径
	}

	// 添加 cookies
	if s.cfg.YouTube.CookiesFile != "" {
		args = append(args, "--cookies", s.cfg.YouTube.CookiesFile)
	} else if s.cfg.YouTube.CookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", s.cfg.YouTube.CookiesFromBrowser)
	}

	// 添加稳定性参数
	args = append(args, youtube.BuildYtDlpStabilityArgs(s.cfg)...)

	// 添加视频URL
	args = append(args, videoURL)

	// 打印命令
	cmdStr := "yt-dlp " + strings.Join(args, " ")
	logger.Info().
		Str("video_id", videoID).
		Str("command", cmdStr).
		Msg("执行字幕下载命令")

	// 执行命令
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		logger.Error().
			Str("video_id", videoID).
			Str("command", cmdStr).
			Str("output", outputStr).
			Err(err).
			Msg("字幕下载命令执行失败")
		return nil, fmt.Errorf("下载字幕失败: %w, 输出: %s", err, outputStr)
	}

	// 查找下载的字幕文件
	subtitleFiles, err := s.fileManager.FindSubtitleFiles(videoDir)
	if err != nil {
		return nil, fmt.Errorf("查找字幕文件失败: %w", err)
	}

	// 处理每个语言的字幕文件
	results := make(map[string]subtitleDownloadResult)
	for _, lang := range languages {
		expectedNewFormatName := fmt.Sprintf("%s[%s].%s.srt", sanitizeTitle(title), videoID, lang)
		expectedNewFormatPath := filepath.Join(videoDir, expectedNewFormatName)

		// 检查是否已经是新格式
		if _, err := os.Stat(expectedNewFormatPath); err == nil {
			// 新格式文件已存在
			results[lang] = subtitleDownloadResult{filePath: expectedNewFormatPath}
			continue
		}

		// 查找该语言的字幕文件（可能是旧格式）
		var foundPath string
		for _, subPath := range subtitleFiles {
			base := filepath.Base(subPath)
			// 跳过已经是新格式的文件（检查是否以 title[videoID] 开头）
			sanitizedTitle := sanitizeTitle(title)
			if strings.HasPrefix(base, sanitizedTitle+"["+videoID+"]") {
				continue
			}
			// 也检查截断后的标题（可能之前已经截断过）
			truncatedTitle := truncateTitleForFilename(sanitizedTitle, videoID, lang, ".srt")
			if strings.HasPrefix(base, truncatedTitle+"["+videoID+"]") {
				continue
			}
			// 检查文件名中是否包含语言代码
			// 支持多种旧格式：
			// - title.lang.srt
			// - video_id_lang.srt
			// - title-lang.srt
			// - title_lang.srt
			// - aid_lang.srt
			if strings.Contains(base, "."+lang+".") ||
				strings.Contains(base, "-"+lang+".") ||
				strings.Contains(base, "_"+lang+".") ||
				strings.HasSuffix(base, "."+lang+".srt") ||
				strings.HasSuffix(base, "-"+lang+".srt") ||
				strings.HasSuffix(base, "_"+lang+".srt") {
				foundPath = subPath
				break
			}
		}

		if foundPath != "" {
			// 复制为新格式（不删除旧格式）
			if err := s.copyFile(foundPath, expectedNewFormatPath); err != nil {
				logger.Warn().Err(err).Str("old_path", foundPath).Str("new_path", expectedNewFormatPath).Str("lang", lang).Msg("复制字幕文件失败")
				results[lang] = subtitleDownloadResult{filePath: foundPath}
			} else {
				logger.Info().
					Str("old_path", foundPath).
					Str("new_path", expectedNewFormatPath).
					Str("lang", lang).
					Msg("已从旧格式复制为新格式字幕文件")
				results[lang] = subtitleDownloadResult{filePath: expectedNewFormatPath}
			}
		} else {
			// 未找到该语言的字幕文件
			results[lang] = subtitleDownloadResult{
				err: fmt.Errorf("未找到该语言的字幕文件"),
			}
		}
	}

	return results, nil
}

// truncateTitleForFilename 截断标题以适应文件名长度限制
// 格式：{title}[{video_id}].{lang}.{ext}
// 保留 video_id 和 lang.ext 部分，只截断 title
func truncateTitleForFilename(title, videoID, lang, ext string) string {
	maxFilenameLength := 255 // Linux 文件名最大长度
	fixedPart := fmt.Sprintf("[%s].%s%s", videoID, lang, ext)
	maxTitleLength := maxFilenameLength - len(fixedPart)

	titleBytes := []byte(title)
	if len(titleBytes) <= maxTitleLength {
		return title
	}

	// 截断标题，确保不超过最大长度（按字节计算）
	truncated := titleBytes[:maxTitleLength]
	// 如果最后一个字节是 UTF-8 字符的中间字节，继续往前截断
	for len(truncated) > 0 && (truncated[len(truncated)-1]&0xC0) == 0x80 {
		truncated = truncated[:len(truncated)-1]
	}
	return string(truncated)
}

// sanitizeTitle 清理标题，用于文件名
func sanitizeTitle(title string) string {
	// 移除或替换不适合文件名的字符
	title = strings.ReplaceAll(title, "/", "_")
	title = strings.ReplaceAll(title, "\\", "_")
	title = strings.ReplaceAll(title, ":", "_")
	title = strings.ReplaceAll(title, "*", "_")
	title = strings.ReplaceAll(title, "?", "_")
	title = strings.ReplaceAll(title, "\"", "_")
	title = strings.ReplaceAll(title, "<", "_")
	title = strings.ReplaceAll(title, ">", "_")
	title = strings.ReplaceAll(title, "|", "_")
	return strings.TrimSpace(title)
}
