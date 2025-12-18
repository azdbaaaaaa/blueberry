package service

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blueberry/internal/config"
	"blueberry/internal/repository/bilibili"
	"blueberry/internal/repository/file"
	"blueberry/internal/repository/youtube"
	"blueberry/pkg/logger"
)

type UploadService interface {
	// UploadSingleVideo 上传单个视频及其字幕到B站
	// videoPath: 本地视频文件的完整路径
	// accountName: B站账号名称
	// 上传成功后会自动将字幕文件重命名为 {aid}_{lang}.srt 格式
	UploadSingleVideo(ctx context.Context, videoPath string, accountName string) error

	// UploadChannel 上传指定频道下所有已下载的视频
	// channelURL: YouTube频道的完整URL
	// 会查找该频道对应的B站账号，然后查找本地已下载的视频文件，逐个上传
	// 如果某个视频上传失败，会记录错误但继续处理下一个视频
	UploadChannel(ctx context.Context, channelURL string) error

	// UploadChannelDir 直接根据本地频道目录上传该目录下的所有视频
	// 不依赖配置中的频道 URL；账号从全局账号池中随机选择（受每日上限限制）
	UploadChannelDir(ctx context.Context, channelDir string) error

	// UploadAllChannels 上传配置文件中所有频道下已下载的视频
	// 遍历配置中的所有YouTube频道，依次调用 UploadChannel 进行上传
	// 如果某个频道处理失败，会记录错误但继续处理下一个频道
	UploadAllChannels(ctx context.Context) error

	// RenameSubtitlesForAID 将字幕文件重命名为 {aid}_{lang}.srt 格式
	// subtitlePaths: 原始字幕文件的路径列表
	// aid: Bilibili视频ID，用于生成新的文件名
	// 返回重命名后的字幕文件路径列表
	RenameSubtitlesForAID(subtitlePaths []string, aid string) ([]string, error)
}

type uploadService struct {
	uploader        bilibili.Uploader
	parser          youtube.Parser
	subtitleManager youtube.SubtitleManager
	fileManager     file.Repository
	cfg             *config.Config
}

// NewUploadService 创建并返回一个新的 UploadService 实例
// 直接接收 Repository 层依赖，不通过中间层
func NewUploadService(
	uploader bilibili.Uploader,
	parser youtube.Parser,
	subtitleManager youtube.SubtitleManager,
	fileManager file.Repository,
	cfg *config.Config,
) UploadService {
	return &uploadService{
		uploader:        uploader,
		parser:          parser,
		subtitleManager: subtitleManager,
		fileManager:     fileManager,
		cfg:             cfg,
	}
}

func (s *uploadService) RenameSubtitlesForAID(subtitlePaths []string, aid string) ([]string, error) {
	return s.subtitleManager.RenameSubtitlesForAID(subtitlePaths, aid, s.cfg.Output.Directory)
}

// selectAvailableAccount 随机选择当日未达上限的账号
func (s *uploadService) selectAvailableAccount() (string, bool) {
	names := make([]string, 0, len(s.cfg.BilibiliAccounts))
	for name := range s.cfg.BilibiliAccounts {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", false
	}
	counts, err := s.fileManager.LoadTodayUploadCounts()
	if err != nil {
		counts = map[string]int{}
	}
	limit := s.cfg.Bilibili.DailyUploadLimit
	if limit <= 0 {
		limit = 160
	}
	available := make([]string, 0, len(names))
	for _, n := range names {
		if counts[n] < limit {
			available = append(available, n)
		}
	}
	if len(available) == 0 {
		return "", false
	}
	rand.Seed(time.Now().UnixNano())
	return available[rand.Intn(len(available))], true
}

func (s *uploadService) UploadSingleVideo(ctx context.Context, videoPath string, accountName string) error {
	account, exists := s.cfg.BilibiliAccounts[accountName]
	if !exists {
		logger.Error().Str("account", accountName).Msg("账号不存在")
		return nil
	}

	// 允许传入“目录或文件”。目录时在目录中查找实际视频文件。
	videoDir := videoPath
	videoFile := videoPath
	if info, err := os.Stat(videoPath); err == nil {
		if info.IsDir() {
			videoDir = videoPath
			if vf, err := s.fileManager.FindVideoFile(videoDir); err == nil && vf != "" {
				videoFile = vf
			} else {
				logger.Error().Str("video_dir", videoDir).Msg("未在该目录找到本地视频文件")
				return fmt.Errorf("未在目录中找到视频文件: %s", videoDir)
			}
		} else {
			videoDir = filepath.Dir(videoPath)
			videoFile = videoPath
		}
	} else {
		return fmt.Errorf("路径不存在: %s", videoPath)
	}

	// 如果该视频已标记为上传完成，则跳过
	if s.fileManager.IsVideoUploaded(videoDir) {
		logger.Info().
			Str("video_dir", videoDir).
			Msg("视频已上传（upload_status.json 已完成），跳过上传")
		return nil
	}

	// 在检查视频/图片等文件之前，先检查下载状态
	if status, downloaded, errMsg, err := s.fileManager.GetDownloadVideoStatus(videoDir); err == nil {
		if !downloaded {
			logger.Warn().
				Str("video_dir", videoDir).
				Str("status", status).
				Str("error", errMsg).
				Msg("视频下载未完成，跳过上传（不再检查视频/封面/字幕文件）")
			return nil
		}
	} else {
		logger.Warn().Err(err).Str("video_dir", videoDir).Msg("读取下载状态失败，视为未完成，跳过上传")
		return nil
	}

	allSubtitlePaths, _ := s.fileManager.FindSubtitleFiles(videoDir)
	// 优先选择英文字幕
	subtitlePaths := s.filterEnglishSubtitles(allSubtitlePaths)
	if len(subtitlePaths) == 0 {
		// 如果没有英文字幕，使用所有字幕
		subtitlePaths = allSubtitlePaths
	}
	if !s.cfg.Bilibili.UploadSubtitles {
		subtitlePaths = []string{}
		logger.Info().Msg("已禁用字幕上传（bilibili.upload_subtitles=false）")
	}
	logger.Info().
		Int("total_subtitles", len(allSubtitlePaths)).
		Int("selected_subtitles", len(subtitlePaths)).
		Msg("字幕文件选择完成")
	// 使用 video_id 作为标题；若无法获取则回退到文件名，同时获取描述（优先 .description）
	videoTitle := ""
	videoDesc := s.getVideoDescription(videoDir, videoFile)
	if info, err := s.fileManager.LoadVideoInfo(videoDir); err == nil && info != nil {
		if id := strings.TrimSpace(info.ID); id != "" {
			videoTitle = id
		}
	}
	if videoTitle == "" {
		videoTitle = s.fileManager.ExtractVideoTitleFromFile(videoFile)
	}

	logger.Info().
		Str("video_dir", videoDir).
		Str("video_file", videoFile).
		Str("title", videoTitle).
		Msg("开始上传视频")

	// 标记开始上传
	if err := s.fileManager.MarkVideoUploading(videoDir); err != nil {
		logger.Warn().Err(err).Msg("标记上传状态失败")
	}

	result, err := s.uploader.UploadVideo(ctx, videoFile, videoTitle, videoDesc, subtitlePaths, account)
	if err != nil {
		logger.Error().Err(err).Msg("上传失败")
		// 标记上传失败
		if markErr := s.fileManager.MarkVideoUploadFailed(videoDir, err.Error()); markErr != nil {
			logger.Warn().Err(markErr).Msg("标记上传失败状态失败")
		}
		return err
	}

	if result.Success {
		logger.Info().Str("video_id", result.VideoID).Msg("上传成功")
		// 增加账号当日上传计数
		if err := s.fileManager.IncrementTodayUploadCount(accountName); err != nil {
			logger.Warn().Err(err).Str("account", accountName).Msg("更新账号当日上传计数失败")
		}
		// 标记上传完成（保存到 upload_status.json，下次运行时会跳过）
		if err := s.fileManager.MarkVideoUploaded(videoDir, result.VideoID, accountName); err != nil {
			logger.Warn().Err(err).Msg("标记上传完成状态失败")
		} else {
			logger.Info().
				Str("video_id", result.VideoID).
				Str("video_dir", videoDir).
				Msg("上传状态已保存到 upload_status.json，下次运行将自动跳过此视频")
		}
		// 按配置删除本地原视频文件
		if s.cfg.Bilibili.DeleteOriginalAfterUpload {
			if err := os.Remove(videoFile); err != nil {
				logger.Warn().Err(err).Str("video_file", videoFile).Msg("删除本地原视频文件失败")
			} else {
				logger.Info().Str("video_file", videoFile).Msg("已删除本地原视频文件（按配置）")
			}
		} else {
			logger.Info().
				Str("video_file", videoFile).
				Msg("未启用删除本地视频文件配置（bilibili.delete_original_after_upload=false），文件已保留")
		}

		// 重命名字幕文件
		if len(subtitlePaths) > 0 {
			renamedPaths, err := s.RenameSubtitlesForAID(subtitlePaths, result.VideoID)
			if err != nil {
				logger.Warn().Err(err).Msg("重命名字幕文件失败")
			} else {
				logger.Info().Int("count", len(renamedPaths)).Msg("字幕文件已重命名")
			}
		}
		// 归档所有字幕文件（无论是否上传字幕），复制到 {subtitle_archive}/{aid}/
		if len(allSubtitlePaths) > 0 {
			if archived, err := s.subtitleManager.CopySubtitlesForAID(allSubtitlePaths, result.VideoID, s.cfg.Output.SubtitleArchive); err != nil {
				logger.Warn().Err(err).Str("aid", result.VideoID).Msg("复制字幕到归档目录失败")
			} else {
				logger.Info().Int("count", len(archived)).Str("aid", result.VideoID).Str("archive_root", s.cfg.Output.SubtitleArchive).Msg("字幕已复制到归档目录")
			}
		}
	} else {
		logger.Error().Err(result.Error).Msg("上传失败")
		return result.Error
	}

	return nil
}

func (s *uploadService) UploadChannel(ctx context.Context, channelURL string) error {
	var targetChannel *config.YouTubeChannel
	for i := range s.cfg.YouTubeChannels {
		if s.cfg.YouTubeChannels[i].URL == channelURL {
			targetChannel = &s.cfg.YouTubeChannels[i]
			break
		}
	}

	if targetChannel == nil {
		logger.Error().Str("channel_url", channelURL).Msg("频道未配置")
		return nil
	}

	// 选择当日未达上限的可用账号（随机）
	accountName, ok := s.selectAvailableAccount()
	if !ok {
		logger.Error().Msg("没有可用的B站账号（当日额度已用尽）")
		return fmt.Errorf("no available bilibili account today")
	}
	account := s.cfg.BilibiliAccounts[accountName]

	logger.Info().Str("channel_url", channelURL).Str("account", accountName).Msg("开始处理频道上传")

	channelID := s.fileManager.ExtractChannelID(channelURL)

	// 加载频道信息（从 channel_info.json 或直接扫描目录）
	var videos []map[string]interface{}
	channelInfo, err := s.fileManager.LoadChannelInfo(channelID)
	if err == nil && len(channelInfo) > 0 {
		videos = channelInfo
		logger.Info().Int("count", len(videos)).Msg("从频道信息文件加载视频列表")
	} else {
		// 如果没有频道信息文件，尝试从目录中扫描
		logger.Info().Msg("未找到频道信息文件，从目录扫描视频")
		// 这里可以添加目录扫描逻辑，但为了简化，我们使用解析器
		ytVideos, err := s.parser.ExtractVideosFromChannel(ctx, channelURL)
		if err != nil {
			logger.Error().Err(err).Msg("解析频道失败")
			return err
		}
		// 转换为 map 格式
		for _, v := range ytVideos {
			videos = append(videos, map[string]interface{}{
				"id":    v.ID,
				"title": v.Title,
				"url":   v.URL,
			})
		}
	}

	logger.Info().Int("count", len(videos)).Msg("找到视频")

	for i, videoMap := range videos {
		videoID, _ := videoMap["id"].(string)
		title, _ := videoMap["title"].(string)

		if videoID == "" {
			continue
		}

		logger.Info().
			Int("current", i+1).
			Int("total", len(videos)).
			Str("video_id", videoID).
			Str("title", title).
			Msg("处理视频")

		// 查找本地视频文件（使用新的目录结构：title-based）
		videoDir, err := s.fileManager.FindVideoDirByID(channelID, videoID)
		if err != nil || videoDir == "" {
			// 如果找不到，尝试使用 title 创建目录路径
			if title != "" {
				videoDir, _ = s.fileManager.EnsureVideoDirByTitle(channelID, title)
			} else {
				logger.Warn().Str("video_id", videoID).Msg("未找到本地视频目录，跳过")
				continue
			}
		}

		// 检查是否已上传
		if s.fileManager.IsVideoUploaded(videoDir) {
			logger.Info().
				Str("title", title).
				Str("video_id", videoID).
				Msg("视频已上传，跳过")
			continue
		}

		// 在检查视频/图片等文件之前，先检查下载状态
		if status, downloaded, errMsg, err := s.fileManager.GetDownloadVideoStatus(videoDir); err == nil {
			if !downloaded {
				logger.Warn().
					Str("video_id", videoID).
					Str("status", status).
					Str("error", errMsg).
					Msg("视频下载未完成，跳过上传（不再检查视频/封面/字幕文件）")
				continue
			}
		} else {
			logger.Warn().Err(err).Str("video_id", videoID).Msg("读取下载状态失败，视为未完成，跳过上传")
			continue
		}

		videoFile, err := s.fileManager.FindVideoFile(videoDir)
		if err != nil {
			logger.Warn().Str("title", title).Str("video_id", videoID).Msg("未找到本地视频文件，跳过")
			continue
		}

		allSubtitlePaths, _ := s.fileManager.FindSubtitleFiles(videoDir)
		// 优先选择英文字幕
		subtitlePaths := s.filterEnglishSubtitles(allSubtitlePaths)
		if len(subtitlePaths) == 0 {
			// 如果没有英文字幕，使用所有字幕
			subtitlePaths = allSubtitlePaths
		}
		if !s.cfg.Bilibili.UploadSubtitles {
			subtitlePaths = []string{}
			logger.Info().Msg("已禁用字幕上传（bilibili.upload_subtitles=false）")
		}
		logger.Info().
			Int("total_subtitles", len(allSubtitlePaths)).
			Int("selected_subtitles", len(subtitlePaths)).
			Msg("字幕文件选择完成")

		// 使用 video_id 作为标题，加载描述
		videoTitle := videoID
		videoDesc := s.getVideoDescription(videoDir, videoFile)

		// 检查封面图是否存在（必需，上传器缺失时会直接退出）
		coverPath, _ := s.fileManager.FindCoverFile(videoDir)
		if coverPath != "" {
			logger.Debug().Str("cover_path", coverPath).Msg("找到封面图")
		} else {
			logger.Warn().Msg("未找到封面图，将导致上传器退出。请先生成/下载封面图")
		}

		logger.Info().
			Str("video_file", videoFile).
			Str("title", videoTitle).
			Int("subtitle_count", len(subtitlePaths)).
			Msg("准备上传")

		// 标记开始上传
		if err := s.fileManager.MarkVideoUploading(videoDir); err != nil {
			logger.Warn().Err(err).Msg("标记上传状态失败")
		}

		result, err := s.uploader.UploadVideo(ctx, videoFile, videoTitle, videoDesc, subtitlePaths, account)
		if err != nil {
			errorMsg := err.Error()
			logger.Error().Err(err).Str("title", videoTitle).Msg("上传失败，跳过该视频继续下一个")
			// 标记上传失败
			if markErr := s.fileManager.MarkVideoUploadFailed(videoDir, errorMsg); markErr != nil {
				logger.Warn().Err(markErr).Msg("标记上传失败状态失败")
			}
			// 不中断整个频道，继续下一个视频
			continue
		}

		if result.Success && result.VideoID != "" {
			logger.Info().
				Str("video_id", result.VideoID).
				Str("bilibili_aid", result.VideoID).
				Str("title", videoTitle).
				Str("video_dir", videoDir).
				Msg("视频上传并发布成功")

			// 标记上传完成（保存到 upload_status.json，下次运行时会跳过）
			if err := s.fileManager.MarkVideoUploaded(videoDir, result.VideoID, accountName); err != nil {
				logger.Warn().Err(err).Msg("标记上传完成状态失败")
			} else {
				logger.Info().
					Str("video_id", result.VideoID).
					Str("video_dir", videoDir).
					Msg("上传状态已保存到 upload_status.json，下次运行将自动跳过此视频")
			}

			// 按配置删除本地原视频文件
			if s.cfg.Bilibili.DeleteOriginalAfterUpload {
				if err := os.Remove(videoFile); err != nil {
					logger.Warn().Err(err).Str("video_file", videoFile).Msg("删除本地原视频文件失败")
				} else {
					logger.Info().Str("video_file", videoFile).Msg("已删除本地原视频文件（按配置）")
				}
			} else {
				logger.Info().
					Str("video_file", videoFile).
					Msg("未启用删除本地视频文件配置（bilibili.delete_original_after_upload=false），文件已保留")
			}

			// 重命名字幕文件
			if len(subtitlePaths) > 0 {
				renamedPaths, err := s.RenameSubtitlesForAID(subtitlePaths, result.VideoID)
				if err != nil {
					logger.Warn().Err(err).Msg("重命名字幕文件失败")
				} else {
					logger.Info().Int("count", len(renamedPaths)).Msg("字幕文件已重命名")
				}
			}
			// 归档所有字幕
			if len(allSubtitlePaths) > 0 {
				if archived, err := s.subtitleManager.CopySubtitlesForAID(allSubtitlePaths, result.VideoID, s.cfg.Output.SubtitleArchive); err != nil {
					logger.Warn().Err(err).Str("aid", result.VideoID).Msg("复制字幕到归档目录失败")
				} else {
					logger.Info().Int("count", len(archived)).Str("aid", result.VideoID).Str("archive_root", s.cfg.Output.SubtitleArchive).Msg("字幕已复制到归档目录")
				}
			}
		} else {
			errorMsg := "上传完成但未获取到视频ID"
			if result.Error != nil {
				errorMsg = result.Error.Error()
				logger.Error().Err(result.Error).Str("title", videoTitle).Msg("上传失败")
			} else {
				logger.Warn().Str("title", videoTitle).Msg("上传完成但未获取到视频ID，可能需要手动处理")
			}
			// 标记上传失败
			if markErr := s.fileManager.MarkVideoUploadFailed(videoDir, errorMsg); markErr != nil {
				logger.Warn().Err(markErr).Msg("标记上传失败状态失败")
			}
		}
	}

	return nil
}

// UploadChannelDir 根据本地频道目录上传
func (s *uploadService) UploadChannelDir(ctx context.Context, channelDir string) error {
	accountName, ok := s.selectAvailableAccount()
	if !ok {
		logger.Error().Msg("没有可用的B站账号（当日额度已用尽）")
		return fmt.Errorf("no available bilibili account today")
	}
	account := s.cfg.BilibiliAccounts[accountName]

	logger.Info().Str("channel_dir", channelDir).Str("account", accountName).Msg("开始处理频道目录上传")

	// 推导 channelID（目录名）
	channelID := filepath.Base(channelDir)

	// 尝试加载 channel_info.json
	var videos []map[string]interface{}
	if info, err := s.fileManager.LoadChannelInfo(channelID); err == nil && len(info) > 0 {
		videos = info
		logger.Info().Int("count", len(videos)).Msg("从频道信息文件加载视频列表")
	} else {
		// 目录扫描：遍历子目录作为视频目录
		logger.Info().Str("channel_dir", channelDir).Msg("未找到频道信息文件，从目录扫描视频")
		entries, err := os.ReadDir(channelDir)
		if err != nil {
			return fmt.Errorf("读取频道目录失败: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			videoID := e.Name()
			videos = append(videos, map[string]interface{}{
				"id":    videoID,
				"title": "",
				"url":   "",
			})
		}
	}

	logger.Info().Int("count", len(videos)).Msg("找到视频")

	for i, videoMap := range videos {
		videoID, _ := videoMap["id"].(string)
		title, _ := videoMap["title"].(string)
		if videoID == "" {
			continue
		}
		logger.Info().
			Int("current", i+1).
			Int("total", len(videos)).
			Str("video_id", videoID).
			Str("title", title).
			Msg("处理视频")

		// 本地视频目录
		videoDir, _ := s.fileManager.FindVideoDirByID(channelID, videoID)
		if videoDir == "" {
			// 直接拼接
			videoDir = filepath.Join(s.cfg.Output.Directory, channelID, videoID)
		}

		// 已上传跳过
		if s.fileManager.IsVideoUploaded(videoDir) {
			logger.Info().Str("video_id", videoID).Msg("视频已上传，跳过")
			continue
		}

		// 在检查视频/图片等文件之前，先检查下载状态
		if status, downloaded, errMsg, err := s.fileManager.GetDownloadVideoStatus(videoDir); err == nil {
			if !downloaded {
				logger.Warn().
					Str("video_id", videoID).
					Str("status", status).
					Str("error", errMsg).
					Msg("视频下载未完成，跳过上传（不再检查视频/封面/字幕文件）")
				continue
			}
		} else {
			logger.Warn().Err(err).Str("video_id", videoID).Msg("读取下载状态失败，视为未完成，跳过上传")
			continue
		}

		videoFile, err := s.fileManager.FindVideoFile(videoDir)
		if err != nil || videoFile == "" {
			logger.Warn().Str("video_id", videoID).Str("video_dir", videoDir).Msg("未找到本地视频文件，跳过")
			continue
		}

		allSubtitlePaths, _ := s.fileManager.FindSubtitleFiles(videoDir)
		subtitlePaths := s.filterEnglishSubtitles(allSubtitlePaths)
		if len(subtitlePaths) == 0 {
			subtitlePaths = allSubtitlePaths
		}
		if !s.cfg.Bilibili.UploadSubtitles {
			subtitlePaths = []string{}
			logger.Info().Msg("已禁用字幕上传（bilibili.upload_subtitles=false）")
		}

		// 使用 video_id 作为标题，加载描述
		videoTitle := videoID
		videoDesc := s.getVideoDescription(videoDir, videoFile)

		logger.Info().
			Str("video_file", videoFile).
			Str("title", videoTitle).
			Int("subtitle_count", len(subtitlePaths)).
			Msg("准备上传")

		if err := s.fileManager.MarkVideoUploading(videoDir); err != nil {
			logger.Warn().Err(err).Msg("标记上传状态失败")
		}

		result, err := s.uploader.UploadVideo(ctx, videoFile, videoTitle, videoDesc, subtitlePaths, account)
		if err != nil {
			errorMsg := err.Error()
			logger.Error().Err(err).Str("title", videoTitle).Msg("上传失败")
			if markErr := s.fileManager.MarkVideoUploadFailed(videoDir, errorMsg); markErr != nil {
				logger.Warn().Err(markErr).Msg("标记上传失败状态失败")
			}
			// 不中断整个频道，继续下一个视频
			continue
		}

		if result.Success && result.VideoID != "" {
			logger.Info().Str("video_id", result.VideoID).Str("title", videoTitle).Msg("视频上传并发布成功")
			if err := s.fileManager.MarkVideoUploaded(videoDir, result.VideoID, accountName); err != nil {
				logger.Warn().Err(err).Msg("标记上传完成状态失败")
			}
			// 按配置删除本地原视频文件
			if s.cfg.Bilibili.DeleteOriginalAfterUpload {
				if err := os.Remove(videoFile); err != nil {
					logger.Warn().Err(err).Str("video_file", videoFile).Msg("删除本地原视频文件失败")
				} else {
					logger.Info().Str("video_file", videoFile).Msg("已删除本地原视频文件（按配置）")
				}
			} else {
				logger.Info().
					Str("video_file", videoFile).
					Msg("未启用删除本地视频文件配置（bilibili.delete_original_after_upload=false），文件已保留")
			}
			if len(subtitlePaths) > 0 {
				if renamed, err := s.RenameSubtitlesForAID(subtitlePaths, result.VideoID); err == nil {
					logger.Info().Int("count", len(renamed)).Msg("字幕文件已重命名")
				}
			}
			// 归档所有字幕
			if len(allSubtitlePaths) > 0 {
				if archived, err := s.subtitleManager.CopySubtitlesForAID(allSubtitlePaths, result.VideoID, s.cfg.Output.SubtitleArchive); err != nil {
					logger.Warn().Err(err).Str("aid", result.VideoID).Msg("复制字幕到归档目录失败")
				} else {
					logger.Info().Int("count", len(archived)).Str("aid", result.VideoID).Str("archive_root", s.cfg.Output.SubtitleArchive).Msg("字幕已复制到归档目录")
				}
			}
		} else {
			errorMsg := "上传完成但未获取到视频ID"
			if result.Error != nil {
				errorMsg = result.Error.Error()
			}
			if markErr := s.fileManager.MarkVideoUploadFailed(videoDir, errorMsg); markErr != nil {
				logger.Warn().Err(markErr).Msg("标记上传失败状态失败")
			}
		}
	}

	return nil
}

// filterEnglishSubtitles 从字幕文件列表中筛选出英文字幕
// 优先匹配 "en"、"en-US"、"en-GB" 等英语变体
func (s *uploadService) filterEnglishSubtitles(subtitlePaths []string) []string {
	var englishSubtitles []string
	for _, path := range subtitlePaths {
		base := filepath.Base(path)
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)

		// 检查文件名中是否包含英语标识
		// 匹配模式：.en.、.en-US.、.en-GB.、-en.、-en-US. 等
		if strings.Contains(name, ".en.") ||
			strings.Contains(name, ".en-") ||
			strings.Contains(name, "-en.") ||
			strings.Contains(name, "-en-") ||
			strings.HasSuffix(name, ".en") ||
			strings.HasSuffix(name, "-en") {
			englishSubtitles = append(englishSubtitles, path)
		}
	}
	return englishSubtitles
}

func (s *uploadService) UploadAllChannels(ctx context.Context) error {
	for _, channel := range s.cfg.YouTubeChannels {
		logger.Info().Str("channel_url", channel.URL).Msg("处理频道")

		if err := s.UploadChannel(ctx, channel.URL); err != nil {
			logger.Error().Err(err).Msg("处理频道失败")
			continue
		}
	}

	return nil
}

// getVideoDescription 优先从与视频同名的 .description 文件读取描述，若不存在则回退到 video_info.json 的 Description
func (s *uploadService) getVideoDescription(videoDir string, videoFile string) string {
	base := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
	descPath := base + ".description"
	if data, err := os.ReadFile(descPath); err == nil {
		if txt := strings.TrimSpace(string(data)); txt != "" {
			return txt
		}
	}
	// 回退 video_info.json
	if info, err := s.fileManager.LoadVideoInfo(videoDir); err == nil && info != nil {
		if d := strings.TrimSpace(info.Description); d != "" {
			return d
		}
	}
	return ""
}
