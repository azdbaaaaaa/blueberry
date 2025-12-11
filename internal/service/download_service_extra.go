package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"blueberry/internal/config"
	"blueberry/pkg/logger"
)

// DownloadChannel 下载指定频道的所有视频
// channelDir: 频道目录路径（例如：downloads/Comic-likerhythm）
func (s *downloadService) DownloadChannel(ctx context.Context, channelDir string) error {
	// 从目录路径中提取频道ID（目录名就是频道ID）
	channelID := filepath.Base(channelDir)

	logger.Info().
		Str("channel_dir", channelDir).
		Str("channel_id", channelID).
		Msg("开始下载指定频道的视频")

	// 查找配置中对应的频道
	var channel *config.YouTubeChannel
	for _, ch := range s.cfg.YouTubeChannels {
		chID := s.fileManager.ExtractChannelID(ch.URL)
		if chID == channelID {
			channel = &ch
			break
		}
	}

	if channel == nil {
		return fmt.Errorf("未找到频道配置: %s", channelID)
	}

	// 加载频道信息
	videoMaps, err := s.fileManager.LoadChannelInfo(channelID)
	if err != nil {
		return fmt.Errorf("加载频道信息失败: %w", err)
	}

	logger.Info().Int("count", len(videoMaps)).Msg("从文件加载视频列表")

	// 获取频道语言配置
	languages := s.getChannelLanguages(channel)

	// 遍历每个视频，逐个下载
	for i, videoMap := range videoMaps {
		videoID, _ := videoMap["id"].(string)
		title, _ := videoMap["title"].(string)
		url, _ := videoMap["url"].(string)
		if url == "" {
			if videoID != "" {
				url = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
			}
		}

		if videoID == "" {
			logger.Warn().Int("index", i+1).Msg("视频ID为空，跳过")
			continue
		}

		logger.Info().
			Int("current", i+1).
			Int("total", len(videoMaps)).
			Str("video_id", videoID).
			Str("title", title).
			Msg("处理视频")

		// 使用统一的下载逻辑
		if err := s.downloadVideoAndSaveInfo(ctx, channelID, videoID, title, url, languages, videoMap); err != nil {
			logger.Error().
				Str("video_id", videoID).
				Str("title", title).
				Err(err).
				Msg("下载视频失败，继续处理下一个")
			continue
		}

		// 在下载每个视频后添加延迟，避免触发 429 错误
		// 字幕下载已经通过 --sleep-subtitles 参数添加了延迟，这里再添加一个整体延迟
		if i < len(videoMaps)-1 {
			delay := 3 * time.Second
			logger.Debug().Dur("delay", delay).Msg("下载完成，等待后继续下一个视频")
			time.Sleep(delay)
		}
	}

	return nil
}

// DownloadVideoDir 下载指定视频目录的视频
// videoDir: 视频目录路径（例如：downloads/Comic-likerhythm/videoTitle）
func (s *downloadService) DownloadVideoDir(ctx context.Context, videoDir string) error {
	logger.Info().Str("video_dir", videoDir).Msg("开始下载指定视频目录的视频")

	// 从目录路径中提取频道ID
	channelID := filepath.Base(filepath.Dir(videoDir))

	// 加载视频信息（从 video_info.json 中获取 videoID）
	videoInfo, err := s.fileManager.LoadVideoInfo(videoDir)
	if err != nil {
		return fmt.Errorf("加载视频信息失败: %w", err)
	}

	// 从 videoInfo 中获取 videoID
	videoID := videoInfo.ID
	if videoID == "" {
		return fmt.Errorf("视频信息中没有 videoID")
	}

	logger.Info().
		Str("channel_id", channelID).
		Str("video_id", videoID).
		Str("title", videoInfo.Title).
		Msg("提取频道和视频信息")

	// 优先从 download_status.json 中获取字幕语言列表（这是 sync-channel 时保存的）
	languages, err := s.fileManager.GetSubtitleLanguagesFromStatus(videoDir)
	if err != nil {
		logger.Warn().Err(err).Msg("从 download_status.json 读取字幕语言失败，尝试从配置获取")
		// 如果读取失败，回退到从配置中获取
		var channel *config.YouTubeChannel
		for _, ch := range s.cfg.YouTubeChannels {
			chID := s.fileManager.ExtractChannelID(ch.URL)
			if chID == channelID {
				channel = &ch
				break
			}
		}

		if channel != nil {
			languages = s.getChannelLanguages(channel)
		} else {
			languages = s.cfg.Subtitles.Languages
		}
	} else {
		logger.Info().Strs("languages", languages).Msg("从 download_status.json 读取字幕语言")
	}

	// 如果仍然没有语言列表，使用默认配置
	if len(languages) == 0 {
		var channel *config.YouTubeChannel
		for _, ch := range s.cfg.YouTubeChannels {
			chID := s.fileManager.ExtractChannelID(ch.URL)
			if chID == channelID {
				channel = &ch
				break
			}
		}

		if channel != nil {
			languages = s.getChannelLanguages(channel)
		} else {
			languages = s.cfg.Subtitles.Languages
		}
		logger.Info().Strs("languages", languages).Msg("使用配置中的字幕语言")
	}

	// 构建 rawData（从 videoInfo 中提取）
	rawData := make(map[string]interface{})
	if videoInfo.RawData != nil {
		rawData = videoInfo.RawData
	} else {
		// 如果没有 RawData，从 VideoInfo 构建
		rawData["id"] = videoInfo.ID
		rawData["title"] = videoInfo.Title
		rawData["url"] = videoInfo.URL
		rawData["webpage_url"] = videoInfo.WebpageURL
		rawData["duration"] = videoInfo.Duration
		rawData["upload_date"] = videoInfo.UploadDate
		rawData["description"] = videoInfo.Description
		rawData["channel_id"] = videoInfo.ChannelID
		rawData["channel"] = videoInfo.Channel
		rawData["channel_url"] = videoInfo.ChannelURL
	}

	// 使用统一的下载逻辑
	videoURL := videoInfo.URL
	if videoURL == "" {
		videoURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	}

	return s.downloadVideoAndSaveInfo(ctx, channelID, videoID, videoInfo.Title, videoURL, languages, rawData)
}
