package service

import (
	"context"
	"fmt"
	"path/filepath"

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

func (s *uploadService) UploadSingleVideo(ctx context.Context, videoPath string, accountName string) error {
	account, exists := s.cfg.BilibiliAccounts[accountName]
	if !exists {
		logger.Error().Str("account", accountName).Msg("账号不存在")
		return nil
	}

	videoDir := filepath.Dir(videoPath)
	subtitlePaths, _ := s.fileManager.FindSubtitleFiles(videoDir)
	videoTitle := s.fileManager.ExtractVideoTitleFromFile(videoPath)

	logger.Info().Str("video_path", videoPath).Str("title", videoTitle).Msg("开始上传视频")

	result, err := s.uploader.UploadVideo(ctx, videoPath, videoTitle, subtitlePaths, account)
	if err != nil {
		logger.Error().Err(err).Msg("上传失败")
		return err
	}

	if result.Success {
		logger.Info().Str("video_id", result.VideoID).Msg("上传成功")

		// 重命名字幕文件
		if len(subtitlePaths) > 0 {
			renamedPaths, err := s.RenameSubtitlesForAID(subtitlePaths, result.VideoID)
			if err != nil {
				logger.Warn().Err(err).Msg("重命名字幕文件失败")
			} else {
				logger.Info().Int("count", len(renamedPaths)).Msg("字幕文件已重命名")
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

	account, exists := s.cfg.BilibiliAccounts[targetChannel.BilibiliAccount]
	if !exists {
		logger.Error().Str("account", targetChannel.BilibiliAccount).Msg("账号不存在")
		return nil
	}

	logger.Info().Str("channel_url", channelURL).Str("account", targetChannel.BilibiliAccount).Msg("开始处理频道上传")

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

		videoFile, err := s.fileManager.FindVideoFile(videoDir)
		if err != nil {
			logger.Warn().Str("title", title).Str("video_id", videoID).Msg("未找到本地视频文件，跳过")
			continue
		}

		subtitlePaths, _ := s.fileManager.FindSubtitleFiles(videoDir)

		// 使用 title 或从文件提取
		videoTitle := title
		if videoTitle == "" {
			videoTitle = s.fileManager.ExtractVideoTitleFromFile(videoFile)
		}

		// 检查封面图是否存在（可选，上传器会自动查找）
		coverPath, _ := s.fileManager.FindCoverFile(videoDir)
		if coverPath != "" {
			logger.Debug().Str("cover_path", coverPath).Msg("找到封面图")
		} else {
			logger.Debug().Msg("未找到封面图，上传器将使用默认封面")
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

		result, err := s.uploader.UploadVideo(ctx, videoFile, videoTitle, subtitlePaths, account)
		if err != nil {
			errorMsg := err.Error()
			logger.Error().Err(err).Str("title", videoTitle).Msg("上传失败")
			// 标记上传失败
			if markErr := s.fileManager.MarkVideoUploadFailed(videoDir, errorMsg); markErr != nil {
				logger.Warn().Err(markErr).Msg("标记上传失败状态失败")
			}
			// 发布失败时退出，方便排查问题
			logger.Error().
				Str("title", videoTitle).
				Str("video_dir", videoDir).
				Msg("上传失败，退出程序以便排查问题")
			return fmt.Errorf("上传失败: %w", err)
		}

		if result.Success && result.VideoID != "" {
			logger.Info().
				Str("video_id", result.VideoID).
				Str("bilibili_aid", result.VideoID).
				Str("title", videoTitle).
				Str("video_dir", videoDir).
				Msg("视频上传并发布成功")

			// 标记上传完成（保存到 upload_status.json，下次运行时会跳过）
			if err := s.fileManager.MarkVideoUploaded(videoDir, result.VideoID); err != nil {
				logger.Warn().Err(err).Msg("标记上传完成状态失败")
			} else {
				logger.Info().
					Str("video_id", result.VideoID).
					Str("video_dir", videoDir).
					Msg("上传状态已保存到 upload_status.json，下次运行将自动跳过此视频")
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
