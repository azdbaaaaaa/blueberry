package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"blueberry/internal/config"
	"blueberry/internal/repository/file"
	"blueberry/internal/repository/youtube"
	"blueberry/pkg/logger"
)

type DownloadService interface {
	// ParseChannels 解析配置文件中所有频道并保存视频列表信息到目录下
	// 遍历配置中的所有YouTube频道，为每个频道解析视频列表
	// 解析结果保存到 {outputDir}/{channelID}/channel_info.json
	// 并为每个视频创建目录（使用 title 作为目录名）并保存基本信息
	ParseChannels(ctx context.Context) error

	// DownloadChannels 根据已保存的频道信息下载所有频道的视频
	// 遍历配置中的所有YouTube频道，从各自的 channel_info.json 读取视频列表
	// 然后逐个下载视频、字幕、缩略图等
	// 如果某个视频已存在（视频文件和字幕文件都存在），则跳过
	DownloadChannels(ctx context.Context) error

	// DownloadSingleVideo 下载单个YouTube视频及其字幕
	// videoURL: YouTube视频的完整URL
	// 直接下载，不使用频道信息文件
	DownloadSingleVideo(ctx context.Context, videoURL string) error

	// DownloadChannel 下载指定频道的所有视频
	// channelDir: 频道目录路径（例如：downloads/Comic-likerhythm）
	DownloadChannel(ctx context.Context, channelDir string) error

	// DownloadVideoDir 下载指定视频目录的视频
	// videoDir: 视频目录路径（例如：downloads/Comic-likerhythm/videoID）
	DownloadVideoDir(ctx context.Context, videoDir string) error
}

type downloadService struct {
	downloader      youtube.Downloader
	parser          youtube.Parser
	subtitleManager youtube.SubtitleManager
	fileManager     file.Repository
	cfg             *config.Config
}

// NewDownloadService 创建并返回一个新的 DownloadService 实例
// 直接接收 Repository 层依赖，不通过中间层
func NewDownloadService(
	downloader youtube.Downloader,
	parser youtube.Parser,
	subtitleManager youtube.SubtitleManager,
	fileManager file.Repository,
	cfg *config.Config,
) DownloadService {
	return &downloadService{
		downloader:      downloader,
		parser:          parser,
		subtitleManager: subtitleManager,
		fileManager:     fileManager,
		cfg:             cfg,
	}
}

// getChannelLanguages 获取指定频道配置的字幕语言列表
func (s *downloadService) getChannelLanguages(channel *config.YouTubeChannel) []string {
	if len(channel.Languages) > 0 {
		return channel.Languages
	}
	return s.cfg.Subtitles.Languages
}

// parseChannel 解析单个频道并保存视频列表信息到目录下（内部方法）
func (s *downloadService) parseChannel(ctx context.Context, channel *config.YouTubeChannel) error {
	channelID := s.fileManager.ExtractChannelID(channel.URL)

	// 检查频道信息是否已存在，如果存在则记录日志
	existingCount := 0
	if s.fileManager.ChannelInfoExists(channelID) {
		existingVideos, err := s.fileManager.LoadChannelInfo(channelID)
		if err == nil {
			existingCount = len(existingVideos)
			logger.Info().
				Str("channel_url", channel.URL).
				Str("channel_id", channelID).
				Int("existing_count", existingCount).
				Msg("频道信息已存在，将重新解析以确保完整性")
		}
	}

	logger.Info().
		Str("channel_url", channel.URL).
		Str("channel_id", channelID).
		Msg("开始解析频道")

	// 解析频道，获取所有视频列表
	videos, err := s.parser.ExtractVideosFromChannel(ctx, channel.URL)
	if err != nil {
		logger.Error().Err(err).Msg("解析频道失败")
		return err
	}

	logger.Info().Int("count", len(videos)).Msg("找到视频")

	// 如果新解析的视频数量与已存在的不同，记录日志
	if existingCount > 0 && len(videos) != existingCount {
		logger.Info().
			Int("existing_count", existingCount).
			Int("new_count", len(videos)).
			Msg("视频数量发生变化，将更新频道信息")
	}

	// 从第一个视频中获取真正的频道ID（channel_id），如果没有视频则使用URL提取的ID
	realChannelID := channelID
	if len(videos) > 0 {
		firstVideo := videos[0]
		if firstVideo.ChannelID != "" {
			realChannelID = firstVideo.ChannelID
			logger.Info().
				Str("extracted_channel_id", channelID).
				Str("real_channel_id", realChannelID).
				Msg("使用视频中的频道ID作为目录名")
		}
	}

	// 确保频道目录存在（使用真正的频道ID）
	_, err = s.fileManager.EnsureChannelDir(realChannelID)
	if err != nil {
		logger.Error().Err(err).Msg("创建频道目录失败")
		return err
	}

	// 将 videos 转换为 []map[string]interface{} 以便保存，并为每个视频创建目录
	videoMaps := make([]map[string]interface{}, 0, len(videos))
	for i, video := range videos {
		// 使用 RawData 如果存在，否则手动构建
		var videoMap map[string]interface{}
		if video.RawData != nil {
			videoMap = video.RawData
		} else {
			// 手动构建 map
			videoMap = make(map[string]interface{})
			videoMap["id"] = video.ID
			videoMap["title"] = video.Title
			videoMap["url"] = video.URL
			videoMap["webpage_url"] = video.WebpageURL
			videoMap["original_url"] = video.OriginalURL
			videoMap["duration"] = video.Duration
			videoMap["duration_string"] = video.DurationString
			videoMap["upload_date"] = video.UploadDate
			videoMap["description"] = video.Description
			videoMap["channel_id"] = video.ChannelID
			videoMap["channel"] = video.Channel
			videoMap["channel_url"] = video.ChannelURL
			videoMap["uploader"] = video.Uploader
			videoMap["uploader_id"] = video.UploaderID
			videoMap["uploader_url"] = video.UploaderURL
			videoMap["playlist_count"] = video.PlaylistCount
			videoMap["playlist"] = video.Playlist
			videoMap["playlist_id"] = video.PlaylistID
			videoMap["playlist_title"] = video.PlaylistTitle
			videoMap["playlist_uploader"] = video.PlaylistUploader
			videoMap["playlist_uploader_id"] = video.PlaylistUploaderID
			videoMap["playlist_channel"] = video.PlaylistChannel
			videoMap["playlist_channel_id"] = video.PlaylistChannelID
			videoMap["playlist_webpage_url"] = video.PlaylistWebpageURL
			videoMap["playlist_index"] = video.PlaylistIndex
			videoMap["n_entries"] = video.NEntries
			videoMap["view_count"] = video.ViewCount
			videoMap["live_status"] = video.LiveStatus
			videoMap["channel_is_verified"] = video.ChannelIsVerified
			videoMap["timestamp"] = video.Timestamp
			videoMap["release_timestamp"] = video.ReleaseTimestamp
			videoMap["epoch"] = video.Epoch
			videoMap["availability"] = video.Availability
		}

		// 为每个视频创建目录（使用视频ID作为目录名）
		videoID, _ := videoMap["id"].(string)
		if videoID == "" {
			videoID = video.ID
		}
		if videoID == "" {
			logger.Warn().
				Int("index", i+1).
				Msg("视频ID为空，跳过")
			continue
		}

		videoDir, err := s.fileManager.EnsureVideoDir(realChannelID, videoID)
		if err != nil {
			logger.Warn().
				Int("index", i+1).
				Str("video_id", videoID).
				Err(err).
				Msg("创建视频目录失败，跳过")
			continue
		}

		// 构建 VideoInfo 并保存到各自目录
		videoURL, _ := videoMap["url"].(string)
		if videoURL == "" {
			videoURL = video.URL
		}
		title, _ := videoMap["title"].(string)
		if title == "" {
			title = video.Title
		}

		// 构建缩略图列表
		thumbnails := s.buildThumbnailsFromRawData(videoMap)

		// 获取频道配置的语言列表
		languages := s.getChannelLanguages(channel)

		// 从 rawData 中提取字幕URL（仅提取配置中指定的语言）
		subtitleURLs := s.extractSubtitleURLs(videoMap, languages)

		// 提取缩略图URL（使用最后一个，通常是最高质量的）
		thumbnailURL := ""
		if len(thumbnails) > 0 {
			thumbnailURL = thumbnails[len(thumbnails)-1].URL
		}

		// 构建 VideoInfo（此时还没有字幕信息，字幕信息在下载时添加）
		videoInfo := s.buildVideoInfoFromRawData(videoMap, videoID, title, videoURL, subtitleURLs, thumbnails)

		// 保存视频信息到各自目录（解析阶段，仅保存基本信息，不表示已下载）
		if err := s.fileManager.SaveVideoInfo(videoDir, videoInfo); err != nil {
			logger.Warn().
				Int("index", i+1).
				Str("title", title).
				Err(err).
				Msg("保存视频解析信息失败")
		} else {
			logger.Debug().
				Int("index", i+1).
				Int("total", len(videos)).
				Str("title", title).
				Str("video_dir", videoDir).
				Msg("视频解析信息已保存（未下载）")
		}

		// 初始化下载状态文件（包含所有资源的URL，状态为pending）
		// 即使没有字幕URL，也保存需要下载的语言列表
		if err := s.fileManager.InitializeDownloadStatus(videoDir, videoURL, subtitleURLs, languages, thumbnailURL); err != nil {
			logger.Warn().
				Int("index", i+1).
				Str("title", title).
				Str("video_dir", videoDir).
				Err(err).
				Msg("初始化下载状态失败")
		} else {
			logger.Debug().
				Int("index", i+1).
				Str("title", title).
				Str("video_dir", videoDir).
				Int("subtitle_count", len(subtitleURLs)).
				Msg("下载状态已初始化（pending）")
		}

		videoMaps = append(videoMaps, videoMap)
	}

	// 保存频道信息到文件（总是更新，确保数据是最新的）
	if err := s.fileManager.SaveChannelInfo(realChannelID, videoMaps); err != nil {
		logger.Error().Err(err).Msg("保存频道信息失败")
		return err
	}

	logger.Info().
		Str("channel_id", realChannelID).
		Int("video_count", len(videos)).
		Msg("频道信息已保存")

	// 生成待下载状态文件（在解析后立即生成，方便查看）
	languages := s.getChannelLanguages(channel)
	if err := s.generatePendingDownloads(realChannelID, channel.URL, videoMaps, languages); err != nil {
		logger.Warn().Err(err).Msg("生成待下载状态文件失败")
	} else {
		channelDir, _ := s.fileManager.EnsureChannelDir(realChannelID)
		statusFile := filepath.Join(channelDir, "pending_downloads.json")
		logger.Info().
			Str("channel_id", realChannelID).
			Str("status_file", statusFile).
			Msg("待下载状态文件已生成")
	}

	return nil
}

// downloadFromChannelInfo 根据已保存的频道信息下载单个频道的视频（内部方法）
func (s *downloadService) downloadFromChannelInfo(ctx context.Context, channel *config.YouTubeChannel) error {
	extractedChannelID := s.fileManager.ExtractChannelID(channel.URL)
	languages := s.getChannelLanguages(channel)

	logger.Info().
		Str("channel_url", channel.URL).
		Str("extracted_channel_id", extractedChannelID).
		Msg("开始下载频道视频")

	if len(languages) > 0 {
		logger.Info().Strs("languages", languages).Msg("字幕语言")
	}

	// 尝试从文件加载频道信息（先使用提取的ID）
	var videoMaps []map[string]interface{}
	var err error
	channelID := extractedChannelID
	videoMaps, err = s.fileManager.LoadChannelInfo(channelID)
	if err != nil {
		// 如果使用提取的ID加载失败，尝试从输出目录中查找包含 channel_info.json 的目录
		// 并检查其中的第一个视频的 channel_id 是否匹配
		logger.Debug().Err(err).Msg("使用提取的频道ID加载失败，尝试查找真实频道ID")

		// 扫描输出目录，查找包含 channel_info.json 的目录
		outputDir := s.cfg.Output.Directory
		entries, readErr := os.ReadDir(outputDir)
		if readErr == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				potentialChannelDir := filepath.Join(outputDir, entry.Name())
				channelInfoPath := filepath.Join(potentialChannelDir, "channel_info.json")
				if _, statErr := os.Stat(channelInfoPath); statErr == nil {
					// 找到了 channel_info.json，尝试加载并检查第一个视频的 channel_id
					potentialVideoMaps, loadErr := s.fileManager.LoadChannelInfo(entry.Name())
					if loadErr == nil && len(potentialVideoMaps) > 0 {
						// 检查第一个视频的 channel_id 或 channel_url 是否匹配
						firstVideo := potentialVideoMaps[0]
						videoChannelID, _ := firstVideo["channel_id"].(string)
						videoChannelURL, _ := firstVideo["channel_url"].(string)

						// 如果 channel_id 或 channel_url 匹配，使用这个目录
						if videoChannelID == extractedChannelID || videoChannelURL == channel.URL {
							channelID = entry.Name()
							videoMaps = potentialVideoMaps
							logger.Info().
								Str("found_channel_id", channelID).
								Msg("找到匹配的频道目录")
							err = nil
							break
						}
					}
				}
			}
		}

		// 如果仍然失败，返回错误
		if err != nil {
			logger.Error().Err(err).Msg("加载频道信息失败，请先执行 ParseChannels")
			return err
		}
	}

	logger.Info().Int("count", len(videoMaps)).Msg("从文件加载视频列表")

	// 生成待下载状态文件（如果不存在或需要更新）
	if err := s.generatePendingDownloads(channelID, channel.URL, videoMaps, languages); err != nil {
		logger.Warn().Err(err).Msg("生成待下载状态文件失败，继续下载")
	} else {
		channelDir, _ := s.fileManager.EnsureChannelDir(channelID)
		statusFile := filepath.Join(channelDir, "pending_downloads.json")
		logger.Info().
			Str("status_file", statusFile).
			Msg("待下载状态文件已生成/更新")
	}

	// 遍历每个视频，逐个下载（不使用并发）
	for i, videoMap := range videoMaps {
		// 从 map 中提取基本信息
		videoID, _ := videoMap["id"].(string)
		title, _ := videoMap["title"].(string)
		url, _ := videoMap["url"].(string)
		if url == "" {
			// 如果没有 url，尝试从 id 构建
			if videoID != "" {
				url = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
			}
		}

		if videoID == "" || title == "" {
			logger.Warn().
				Int("index", i+1).
				Msg("视频信息不完整，跳过")
			continue
		}

		logger.Info().
			Int("current", i+1).
			Int("total", len(videoMaps)).
			Str("title", title).
			Str("video_id", videoID).
			Msg("处理视频")

		// 查找视频目录
		videoDir, _ := s.fileManager.FindVideoDirByID(channelID, videoID)

		// 分别检查视频、字幕、缩略图的下载状态
		videoDownloaded := s.fileManager.IsVideoDownloaded(videoDir)
		subtitlesDownloaded := s.fileManager.IsSubtitlesDownloaded(videoDir, languages)
		thumbnailDownloaded := s.fileManager.IsThumbnailDownloaded(videoDir)

		// 记录检查状态
		logger.Debug().
			Str("title", title).
			Str("video_id", videoID).
			Str("video_dir", videoDir).
			Bool("video_downloaded", videoDownloaded).
			Bool("subtitles_downloaded", subtitlesDownloaded).
			Bool("thumbnail_downloaded", thumbnailDownloaded).
			Msg("检查下载状态")

		// 不再在这里跳过，让 downloadVideoAndSaveInfo 内部处理每个步骤的独立检查
		// 如果视频未下载或下载失败，会重新下载
		if !videoDownloaded {
			logger.Info().
				Str("title", title).
				Str("video_id", videoID).
				Bool("video_downloaded", videoDownloaded).
				Bool("subtitles_downloaded", subtitlesDownloaded).
				Bool("thumbnail_downloaded", thumbnailDownloaded).
				Msg("视频未下载或下载失败，将开始/重新下载")
		} else {
			logger.Info().
				Str("title", title).
				Str("video_id", videoID).
				Bool("video_downloaded", videoDownloaded).
				Bool("subtitles_downloaded", subtitlesDownloaded).
				Bool("thumbnail_downloaded", thumbnailDownloaded).
				Msg("检查视频资源状态")
		}

		// 调用公共的下载视频方法
		if err := s.downloadVideoAndSaveInfo(ctx, channelID, videoID, title, url, languages, videoMap); err != nil {
			logger.Error().Err(err).Str("title", title).Str("video_id", videoID).Msg("下载视频失败")
			// 下载失败时，状态文件已经在 downloadVideoAndSaveInfo 中更新为 failed
			continue
		}
	}

	return nil
}

// generatePendingDownloads 生成待下载资源状态文件
func (s *downloadService) generatePendingDownloads(channelID, channelURL string, videoMaps []map[string]interface{}, languages []string) error {
	pending := &file.PendingDownloads{
		ChannelID:   channelID,
		ChannelURL:  channelURL,
		GeneratedAt: time.Now().Unix(),
		Videos:      make([]file.PendingVideoDownload, 0, len(videoMaps)),
	}

	for _, videoMap := range videoMaps {
		videoID, _ := videoMap["id"].(string)
		title, _ := videoMap["title"].(string)
		url, _ := videoMap["url"].(string)
		if url == "" && videoID != "" {
			url = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
		}

		if videoID == "" {
			continue
		}

		// 查找视频目录
		videoDir, _ := s.fileManager.FindVideoDirByID(channelID, videoID)

		// 检查当前下载状态
		videoDownloaded := s.fileManager.IsVideoDownloaded(videoDir)
		subtitlesDownloaded := s.fileManager.IsSubtitlesDownloaded(videoDir, languages)
		thumbnailDownloaded := s.fileManager.IsThumbnailDownloaded(videoDir)

		// 构建视频状态
		videoStatus := "pending"
		videoPath := ""
		if videoDownloaded {
			videoStatus = "completed"
			// 尝试获取视频文件路径
			if videoFile, err := s.fileManager.FindVideoFile(videoDir); err == nil {
				videoPath = videoFile
			}
		}

		// 构建字幕状态
		subtitleStatuses := make(map[string]file.PendingResourceStatus)
		for _, lang := range languages {
			subStatus := file.PendingResourceStatus{
				Status: "pending",
				URL:    "", // 字幕URL可以从video_info.json中获取
			}
			// 检查该语言的字幕是否已下载
			if subtitlesDownloaded {
				// 检查具体语言
				if s.fileManager.IsSubtitlesDownloaded(videoDir, []string{lang}) {
					subStatus.Status = "completed"
					// 尝试获取字幕文件路径
					if subFiles, err := s.fileManager.FindSubtitleFiles(videoDir); err == nil {
						for _, subFile := range subFiles {
							if strings.Contains(subFile, "."+lang+".") || strings.Contains(subFile, "-"+lang+".") {
								subStatus.FilePath = subFile
								break
							}
						}
					}
				}
			}
			subtitleStatuses[lang] = subStatus
		}

		// 构建缩略图状态
		thumbnailStatus := "pending"
		thumbnailPath := ""
		if thumbnailDownloaded {
			thumbnailStatus = "completed"
			thumbnailPath = filepath.Join(videoDir, "thumbnail.jpg")
			if _, err := os.Stat(thumbnailPath); err != nil {
				thumbnailPath = ""
			}
		}

		pendingVideo := file.PendingVideoDownload{
			VideoID:  videoID,
			Title:    title,
			VideoURL: url,
			Video: file.PendingResourceStatus{
				Status:   videoStatus,
				URL:      url,
				FilePath: videoPath,
			},
			Subtitles: subtitleStatuses,
			Thumbnail: file.PendingResourceStatus{
				Status:   thumbnailStatus,
				FilePath: thumbnailPath,
			},
		}

		// 如果所有资源都已下载，标记为 completed
		if videoDownloaded && subtitlesDownloaded {
			pendingVideo.Video.Status = "completed"
			for lang := range pendingVideo.Subtitles {
				pendingVideo.Subtitles[lang] = file.PendingResourceStatus{
					Status: "completed",
				}
			}
		}

		pending.Videos = append(pending.Videos, pendingVideo)
	}

	return s.fileManager.SavePendingDownloads(channelID, pending)
}

// ParseChannels 解析配置文件中所有频道并保存视频列表信息到目录下
func (s *downloadService) ParseChannels(ctx context.Context) error {
	for chIdx, channel := range s.cfg.YouTubeChannels {
		logger.Info().Msg("========================================")
		logger.Info().
			Int("current", chIdx+1).
			Int("total", len(s.cfg.YouTubeChannels)).
			Str("channel_url", channel.URL).
			Msg("解析频道")
		logger.Info().Msg("========================================")

		if err := s.parseChannel(ctx, &channel); err != nil {
			logger.Error().Err(err).Msg("解析频道失败")
			continue
		}
	}

	return nil
}

// DownloadChannels 根据已保存的频道信息下载所有频道的视频
func (s *downloadService) DownloadChannels(ctx context.Context) error {
	for _, channel := range s.cfg.YouTubeChannels {
		logger.Info().Str("channel_url", channel.URL).Msg("处理频道")
		if len(channel.Languages) > 0 {
			logger.Info().Strs("languages", channel.Languages).Msg("字幕语言")
		}

		if err := s.downloadFromChannelInfo(ctx, &channel); err != nil {
			logger.Error().Err(err).Msg("下载频道失败")
			continue
		}
	}

	return nil
}

// DownloadSingleVideo 下载单个YouTube视频及其字幕
func (s *downloadService) DownloadSingleVideo(ctx context.Context, videoURL string) error {
	logger.Info().Str("video_url", videoURL).Msg("开始下载视频")

	// 使用全局配置的语言列表
	languages := s.cfg.Subtitles.Languages
	if len(languages) > 0 {
		logger.Info().Strs("languages", languages).Msg("字幕语言")
	}

	// 对于单个视频，使用 "single" 作为频道ID
	channelID := "single"
	_, err := s.fileManager.EnsureChannelDir(channelID)
	if err != nil {
		logger.Error().Err(err).Msg("创建频道目录失败")
		return err
	}

	videoID := s.fileManager.ExtractVideoID(videoURL)

	// 不再在这里跳过，让 downloadVideoAndSaveInfo 内部处理每个步骤的独立检查
	logger.Info().Str("video_id", videoID).Msg("处理视频资源")

	// 获取视频的完整信息（使用 yt-dlp --dump-json）
	args := []string{
		"--dump-json",
		"--no-warnings",
		"--skip-download",
		"--extractor-args", "youtube:player_client=android,web",
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	// 优先使用 cookies 文件（服务器上可能没有浏览器）
	if s.cfg.YouTube.CookiesFile != "" {
		args = append(args, "--cookies", s.cfg.YouTube.CookiesFile)
	} else if s.cfg.YouTube.CookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", s.cfg.YouTube.CookiesFromBrowser)
	}
	args = append(args, videoURL)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	var rawVideoData map[string]interface{}
	if err == nil {
		if err := json.Unmarshal(output, &rawVideoData); err != nil {
			logger.Warn().Err(err).Msg("解析完整视频信息失败")
		}
	} else {
		logger.Warn().Err(err).Msg("获取完整视频信息失败")
	}

	// 从原始数据中提取 title
	title := ""
	if rawVideoData != nil {
		if rawTitle, ok := rawVideoData["title"].(string); ok && rawTitle != "" {
			title = rawTitle
		}
	}

	// 调用公共的下载视频方法
	if err := s.downloadVideoAndSaveInfo(ctx, channelID, videoID, title, videoURL, languages, rawVideoData); err != nil {
		logger.Error().Err(err).Msg("下载失败")
		return err
	}

	return nil
}

// downloadVideoAndSaveInfo 下载视频并保存信息的公共方法
// 按顺序执行：1. 下载视频 2. 生成封面图 3. 下载字幕 4. 下载缩略图
// 每个步骤都独立检查是否需要执行
// channelID: 频道ID
// videoID: 视频ID
// title: 视频标题（如果为空，会从下载结果或rawData中获取）
// videoURL: 视频URL
// languages: 字幕语言列表
// rawData: 视频的原始数据（可以为nil，会重新获取）
func (s *downloadService) downloadVideoAndSaveInfo(
	ctx context.Context,
	channelID, videoID, title, videoURL string,
	languages []string,
	rawData map[string]interface{},
) error {
	// 查找或创建视频目录（使用视频ID）
	var videoDir string
	var err error
	// 先尝试查找已存在的目录（可能之前用标题创建的）
	videoDir, _ = s.fileManager.FindVideoDirByID(channelID, videoID)
	if videoDir == "" {
		// 如果找不到，使用视频ID创建新目录
		videoDir, err = s.fileManager.EnsureVideoDir(channelID, videoID)
		if err != nil {
			return fmt.Errorf("创建视频目录失败: %w", err)
		}
	}

	// ========== 步骤 1: 下载视频 ==========
	// 先检查下载状态，只有在未下载或失败时才进行下载
	var videoPath string
	videoDownloaded := s.fileManager.IsVideoDownloaded(videoDir)

	logger.Debug().
		Str("video_id", videoID).
		Str("video_dir", videoDir).
		Bool("video_downloaded", videoDownloaded).
		Msg("检查视频下载状态")

	// 如果视频未下载，初始化下载状态并开始下载
	if !videoDownloaded {
		// 在下载开始时就创建 download_status.json，包含所有资源的 URL
		if videoDir != "" {
			// 尝试从 video_info.json 或 rawData 中获取字幕和缩略图 URL
			var subtitleURLs map[string]string
			var thumbnailURL string

			// 优先从 video_info.json 读取
			if videoInfo, err := s.fileManager.LoadVideoInfo(videoDir); err == nil {
				subtitleURLs = videoInfo.Subtitles
				if len(videoInfo.Thumbnails) > 0 {
					thumbnailURL = videoInfo.Thumbnails[0].URL
				}
			} else if rawData != nil {
				// 如果 video_info.json 不存在，从 rawData 中提取
				// 提取字幕 URL
				subtitleURLs = make(map[string]string)
				if subtitles, ok := rawData["subtitles"].(map[string]interface{}); ok {
					for lang, langData := range subtitles {
						if langMap, ok := langData.(map[string]interface{}); ok {
							if urls, ok := langMap["url"].(string); ok && urls != "" {
								subtitleURLs[lang] = urls
							}
						}
					}
				}
				// 提取自动字幕 URL
				if autoSubtitles, ok := rawData["automatic_captions"].(map[string]interface{}); ok {
					for lang, langData := range autoSubtitles {
						if langMap, ok := langData.(map[string]interface{}); ok {
							if urls, ok := langMap["url"].(string); ok && urls != "" {
								// 自动字幕优先级较低，如果手动字幕已存在则不覆盖
								if _, exists := subtitleURLs[lang]; !exists {
									subtitleURLs[lang] = urls
								}
							}
						}
					}
				}
				// 提取缩略图 URL
				if thumbnails, ok := rawData["thumbnails"].([]interface{}); ok && len(thumbnails) > 0 {
					if firstThumb, ok := thumbnails[0].(map[string]interface{}); ok {
						if url, ok := firstThumb["url"].(string); ok {
							thumbnailURL = url
						}
					}
				}
			}

			// 初始化下载状态（包含所有资源的 URL）
			// 注意：如果之前失败过，InitializeDownloadStatus 不会覆盖失败状态
			// 即使没有字幕URL，也保存需要下载的语言列表
			if err := s.fileManager.InitializeDownloadStatus(videoDir, videoURL, subtitleURLs, languages, thumbnailURL); err != nil {
				logger.Warn().Err(err).Str("video_dir", videoDir).Msg("初始化下载状态文件失败")
			} else {
				statusFile := filepath.Join(videoDir, "download_status.json")
				logger.Info().
					Str("status_file", statusFile).
					Str("video_dir", videoDir).
					Str("video_url", videoURL).
					Int("subtitle_count", len(subtitleURLs)).
					Bool("has_thumbnail", thumbnailURL != "").
					Msg("已初始化下载状态文件，包含所有资源 URL")
			}
		}

		logger.Info().Str("video_id", videoID).Msg("开始下载视频")
		// 标记视频为 downloading 状态（重置之前的失败状态）
		if err := s.fileManager.MarkVideoDownloading(videoDir, videoURL); err != nil {
			logger.Warn().Err(err).Str("video_dir", videoDir).Msg("标记视频下载状态失败")
		}

		result, err := s.downloader.DownloadVideo(ctx, channelID, videoURL, languages, title)
		if err != nil {
			// 下载失败，更新状态为 failed
			errorMsg := err.Error()
			if markErr := s.fileManager.MarkVideoFailed(videoDir, errorMsg); markErr != nil {
				logger.Warn().Err(markErr).Msg("标记下载失败状态失败")
			}
			return fmt.Errorf("下载视频失败: %w", err)
		}

		videoPath = result.VideoPath
		// 更新视频目录（使用下载器实际创建的目录）
		resultDir := filepath.Dir(videoPath)
		if videoDir != resultDir {
			videoDir = resultDir
		}

		// 标记视频已下载完成
		if err := s.fileManager.MarkVideoDownloadedWithPath(videoDir, videoPath); err != nil {
			logger.Warn().Err(err).Msg("标记视频下载状态失败")
		} else {
			logger.Info().Str("video_path", videoPath).Msg("视频下载完成")
			s.fileManager.UpdatePendingDownloadStatus(channelID, videoID, "video", "completed", videoPath)
		}
	} else {
		// 视频已下载，查找视频文件路径
		if videoFile, err := s.fileManager.FindVideoFile(videoDir); err == nil {
			videoPath = videoFile
			logger.Info().Str("video_path", videoPath).Msg("视频已存在，跳过下载")
		} else {
			logger.Warn().Str("video_dir", videoDir).Msg("视频标记为已下载，但未找到视频文件")
		}
	}

	// 如果没有 rawData，重新获取完整信息（用于后续步骤）
	if rawData == nil {
		args := []string{
			"--dump-json",
			"--no-warnings",
			"--skip-download",
			"--extractor-args", "youtube:player_client=android,web",
			"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		}
		if s.cfg.YouTube.CookiesFile != "" {
			args = append(args, "--cookies", s.cfg.YouTube.CookiesFile)
		} else if s.cfg.YouTube.CookiesFromBrowser != "" {
			args = append(args, "--cookies-from-browser", s.cfg.YouTube.CookiesFromBrowser)
		}
		args = append(args, videoURL)

		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			if err := json.Unmarshal(output, &rawData); err != nil {
				logger.Warn().Err(err).Msg("解析完整视频信息失败")
			}
		} else {
			logger.Warn().Err(err).Msg("获取完整视频信息失败")
		}
	}

	// 如果没有 title，从 rawData 中获取
	if title == "" && rawData != nil {
		if rawTitle, ok := rawData["title"].(string); ok && rawTitle != "" {
			title = rawTitle
		}
	}

	// ========== 步骤 2: 下载字幕 ==========
	subtitlesDownloaded := s.fileManager.IsSubtitlesDownloaded(videoDir, languages)
	subtitleMap := make(map[string]string) // 用于保存视频信息
	if !subtitlesDownloaded {
		logger.Info().Str("video_id", videoID).Strs("languages", languages).Msg("开始下载字幕")

		// 如果视频已下载，需要单独下载字幕
		if videoDownloaded {
			// 使用 yt-dlp 单独下载字幕（只下载字幕，不下载视频）
			// 注意：yt-dlp 的 DownloadVideo 会同时下载视频和字幕，如果视频已存在，我们需要单独处理字幕
			// 这里我们重新调用 DownloadVideo，但 yt-dlp 会跳过已存在的视频文件
			result, err := s.downloader.DownloadVideo(ctx, channelID, videoURL, languages, title)
			if err != nil {
				logger.Warn().Err(err).Msg("下载字幕失败，继续处理其他任务")
				// 标记所有字幕为失败状态
				for _, lang := range languages {
					if err := s.fileManager.MarkSubtitleFailed(videoDir, lang, fmt.Sprintf("下载字幕失败: %v", err)); err != nil {
						logger.Warn().Str("lang", lang).Err(err).Msg("标记字幕失败状态失败")
					}
					// 同时更新 pending_downloads.json
					s.fileManager.UpdatePendingDownloadStatus(channelID, videoID, lang, "failed", "")
				}
			} else {
				logger.Info().Int("subtitle_count", len(result.SubtitlePaths)).Msg("字幕下载完成")
			}
		}
		// 如果视频刚下载，字幕应该已经在下载结果中

		// 获取字幕信息并更新状态
		subtitleInfo, err := s.subtitleManager.ListSubtitles(ctx, videoURL, languages)
		if err != nil {
			logger.Warn().Err(err).Msg("获取字幕信息失败")
			// 如果获取字幕信息失败，标记所有配置的语言为失败状态
			for _, lang := range languages {
				if err := s.fileManager.MarkSubtitleFailed(videoDir, lang, fmt.Sprintf("获取字幕信息失败: %v", err)); err != nil {
					logger.Warn().Str("lang", lang).Err(err).Msg("标记字幕失败状态失败")
				}
				// 同时更新 pending_downloads.json
				s.fileManager.UpdatePendingDownloadStatus(channelID, videoID, lang, "failed", "")
			}
		} else if subtitleInfo != nil {
			downloadedLanguages := make([]string, 0)
			subtitlePaths := make(map[string]string)
			seenLanguages := make(map[string]bool) // 用于去重

			// 获取所有字幕文件（只查找一次）
			subtitleFiles, _ := s.fileManager.FindSubtitleFiles(videoDir)

			for _, sub := range subtitleInfo.SubtitleURLs {
				subtitleMap[sub.Language] = sub.URL

				// 如果已经处理过这个语言，跳过
				if seenLanguages[sub.Language] {
					continue
				}

				// 优先查找已重命名的文件格式 {video_id}_{lang}.srt
				videoID := s.fileManager.ExtractVideoID(videoURL)
				expectedName := fmt.Sprintf("%s_%s.srt", videoID, sub.Language)
				expectedPath := filepath.Join(videoDir, expectedName)

				var foundPath string
				// 首先检查期望的文件名
				if _, err := os.Stat(expectedPath); err == nil {
					foundPath = expectedPath
				} else {
					// 如果期望的文件不存在，查找其他格式（兼容旧格式）
					for _, subPath := range subtitleFiles {
						base := filepath.Base(subPath)
						// 忽略 .frame.srt 文件
						if strings.Contains(base, ".frame.srt") {
							continue
						}
						// 检查文件名是否包含语言代码（支持 .{lang}. 和 _{lang}. 格式）
						if strings.Contains(base, "."+sub.Language+".") ||
							strings.Contains(base, "-"+sub.Language+".") ||
							strings.Contains(base, "_"+sub.Language+".") {
							foundPath = subPath
							break
						}
					}
				}

				if foundPath != "" {
					downloadedLanguages = append(downloadedLanguages, sub.Language)
					subtitlePaths[sub.Language] = foundPath
					seenLanguages[sub.Language] = true
				}
			}

			// 标记已下载的字幕
			if len(downloadedLanguages) > 0 {
				if err := s.fileManager.MarkSubtitlesDownloadedWithPaths(videoDir, downloadedLanguages, subtitlePaths, subtitleMap); err != nil {
					logger.Warn().Err(err).Msg("标记字幕下载状态失败")
				} else {
					logger.Info().Strs("languages", downloadedLanguages).Msg("字幕下载状态已保存")
					for _, lang := range downloadedLanguages {
						subPath := subtitlePaths[lang]
						s.fileManager.UpdatePendingDownloadStatus(channelID, videoID, lang, "completed", subPath)
					}
				}
			}

			// 检查是否有未下载的字幕（标记为失败）
			for _, lang := range languages {
				found := false
				for _, downloadedLang := range downloadedLanguages {
					if lang == downloadedLang {
						found = true
						break
					}
				}
				if !found {
					// 如果字幕应该存在但没有找到，标记为失败
					// 检查是否在 subtitleInfo 中存在（YouTube 上是否有这个语言的字幕）
					hasSubtitleURL := false
					for _, sub := range subtitleInfo.SubtitleURLs {
						if sub.Language == lang {
							hasSubtitleURL = true
							break
						}
					}

					errorMsg := ""
					if !hasSubtitleURL {
						errorMsg = "该语言的字幕在 YouTube 上不存在"
					} else {
						errorMsg = "字幕文件未找到"
					}

					if err := s.fileManager.MarkSubtitleFailed(videoDir, lang, errorMsg); err != nil {
						logger.Warn().Str("lang", lang).Err(err).Msg("标记字幕失败状态失败")
					} else {
						logger.Warn().Str("lang", lang).Str("error", errorMsg).Msg("字幕下载失败，已标记为失败")
					}
					// 同时更新 pending_downloads.json
					s.fileManager.UpdatePendingDownloadStatus(channelID, videoID, lang, "failed", "")
				}
			}
		}
	} else {
		logger.Info().Str("video_id", videoID).Msg("字幕已下载，跳过")
		// 即使字幕已下载，也需要获取字幕信息用于保存 video_info.json
		subtitleInfo, err := s.subtitleManager.ListSubtitles(ctx, videoURL, languages)
		if err == nil && subtitleInfo != nil {
			for _, sub := range subtitleInfo.SubtitleURLs {
				subtitleMap[sub.Language] = sub.URL
			}
		}
	}

	// ========== 步骤 3: 下载缩略图并设置为封面图 ==========
	thumbnailDownloaded := s.fileManager.IsThumbnailDownloaded(videoDir)
	thumbnails := s.buildThumbnailsFromRawData(rawData) // 用于保存视频信息
	var coverPath string
	hasCover := false

	if !thumbnailDownloaded {
		logger.Info().Str("video_id", videoID).Msg("开始下载缩略图")

		thumbnailURL := ""
		if len(thumbnails) > 0 {
			thumbnailURL = thumbnails[len(thumbnails)-1].URL // 使用最后一个缩略图
		}

		downloadedCoverPath, err := s.downloadThumbnails(ctx, videoDir, rawData)
		if err != nil {
			logger.Warn().Err(err).Msg("下载缩略图失败")
			s.fileManager.UpdatePendingDownloadStatus(channelID, videoID, "thumbnail", "failed", "")
		} else if downloadedCoverPath != "" {
			coverPath = downloadedCoverPath
			if err := s.fileManager.MarkThumbnailDownloadedWithPath(videoDir, coverPath, thumbnailURL); err != nil {
				logger.Warn().Err(err).Msg("标记缩略图下载状态失败")
			} else {
				logger.Info().Str("cover_path", coverPath).Msg("缩略图已下载为 cover.{ext} 格式")
				s.fileManager.UpdatePendingDownloadStatus(channelID, videoID, "thumbnail", "completed", coverPath)
				hasCover = true
			}
		}
	} else {
		logger.Info().Str("video_id", videoID).Msg("缩略图已下载，检查封面图")
		// 检查是否存在 cover.{ext} 文件（可能是 .jpg, .png, .webp 等）
		possibleExtensions := []string{".jpg", ".jpeg", ".png", ".webp", ".gif"}
		for _, ext := range possibleExtensions {
			potentialCoverPath := filepath.Join(videoDir, "cover"+ext)
			if _, err := os.Stat(potentialCoverPath); err == nil {
				coverPath = potentialCoverPath
				hasCover = true
				logger.Info().Str("cover_path", coverPath).Msg("封面图已存在")
				break
			}
		}
		// 兼容旧格式：如果找不到 cover.{ext}，检查是否有 thumbnail.jpg
		if !hasCover {
			thumbnailPath := filepath.Join(videoDir, "thumbnail.jpg")
			if _, err := os.Stat(thumbnailPath); err == nil {
				// 将旧的 thumbnail.jpg 重命名为 cover.jpg
				coverPath = filepath.Join(videoDir, "cover.jpg")
				if err := os.Rename(thumbnailPath, coverPath); err != nil {
					logger.Warn().Err(err).Msg("重命名旧缩略图失败")
				} else {
					hasCover = true
					logger.Info().Str("cover_path", coverPath).Msg("已将旧缩略图重命名为 cover.jpg")
				}
			}
		}
	}

	// 如果没有封面图（缩略图下载失败或不存在），且视频已下载，则从视频首帧生成封面图
	if !hasCover && videoPath != "" {
		if _, err := os.Stat(coverPath); os.IsNotExist(err) {
			logger.Info().Str("video_id", videoID).Msg("缩略图不存在，开始从视频首帧生成封面图")
			if err := s.generateCoverFromVideo(ctx, videoDir, videoPath); err != nil {
				logger.Warn().Err(err).Msg("生成封面图失败，继续处理其他任务")
			} else {
				logger.Info().Str("cover_path", coverPath).Msg("封面图已从视频首帧生成")
			}
		} else {
			logger.Info().Str("cover_path", coverPath).Msg("封面图已存在，跳过生成")
		}
	}

	// ========== 步骤 5: 保存视频信息 ==========
	// 只有在视频真正下载完成（或已存在）时才保存完整的视频信息
	// 这样可以避免在解析阶段就保存信息，导致后续误判为已下载
	if videoDownloaded || videoPath != "" {
		// 构建完整的视频信息
		videoInfo := s.buildVideoInfoFromRawData(rawData, videoID, title, videoURL, subtitleMap, thumbnails)

		// 保存视频信息（videoDir 已经在上面获取了）
		if err := s.fileManager.SaveVideoInfo(videoDir, videoInfo); err != nil {
			logger.Warn().Err(err).Msg("保存视频信息失败")
			// 不返回错误，因为视频可能已经下载，只是保存信息失败
		} else {
			logger.Info().Str("info_file", filepath.Join(videoDir, "video_info.json")).Msg("视频信息已保存")
		}
	} else {
		logger.Debug().Str("video_id", videoID).Msg("视频未下载完成，跳过保存视频信息（避免误判为已下载）")
	}

	return nil
}

// buildThumbnailsFromRawData 从原始数据构建缩略图列表
func (s *downloadService) buildThumbnailsFromRawData(rawData map[string]interface{}) []file.Thumbnail {
	thumbnails := make([]file.Thumbnail, 0)
	if rawData == nil {
		return thumbnails
	}

	if thumbs, ok := rawData["thumbnails"].([]interface{}); ok {
		for _, thumb := range thumbs {
			if thumbMap, ok := thumb.(map[string]interface{}); ok {
				thumbnail := file.Thumbnail{}
				if url, ok := thumbMap["url"].(string); ok {
					thumbnail.URL = url
				}
				if height, ok := thumbMap["height"].(float64); ok {
					thumbnail.Height = int(height)
				}
				if width, ok := thumbMap["width"].(float64); ok {
					thumbnail.Width = int(width)
				}
				thumbnails = append(thumbnails, thumbnail)
			}
		}
	}

	return thumbnails
}

// downloadThumbnails 下载视频缩略图，保存为 cover.{ext} 格式
func (s *downloadService) downloadThumbnails(ctx context.Context, videoDir string, rawData map[string]interface{}) (string, error) {
	if rawData == nil {
		return "", nil
	}

	thumbnails := s.buildThumbnailsFromRawData(rawData)
	if len(thumbnails) == 0 {
		return "", nil
	}

	// 下载最后一个缩略图（通常是最高质量的）
	thumbnail := thumbnails[len(thumbnails)-1]
	if thumbnail.URL == "" {
		return "", nil
	}

	// 从 URL 中提取文件扩展名
	ext := s.extractExtensionFromURL(thumbnail.URL)
	if ext == "" {
		// 如果无法从 URL 提取，默认使用 .jpg
		ext = ".jpg"
	}

	// 直接下载为 cover.{ext} 格式（优先尝试按 URL 扩展名的格式获取）
	coverPath := filepath.Join(videoDir, "cover"+ext)

	// 先下载到临时文件，然后检测实际文件类型
	tempPath := filepath.Join(videoDir, "cover_temp"+ext)
	// 根据目标扩展名设置 Accept
	accept := "*/*"
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		accept = "image/jpeg"
	case ".png":
		accept = "image/png"
	case ".gif":
		accept = "image/gif"
	case ".webp":
		accept = "image/webp"
	}
	cmd := exec.CommandContext(ctx, "curl", "-L", "-H", "Accept: "+accept, "-o", tempPath, thumbnail.URL)
	if err := cmd.Run(); err != nil {
		// 如果 curl 失败，尝试使用 wget
		cmd = exec.CommandContext(ctx, "wget", "--header=Accept: "+accept, "-O", tempPath, thumbnail.URL)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("下载缩略图失败: %w", err)
		}
	}

	// 检测实际文件类型
	actualExt := s.detectImageExtension(tempPath)
	if strings.ToLower(actualExt) != strings.ToLower(ext) {
		// 目标为 URL 指定的扩展；若返回类型不同，尝试转码为目标扩展
		targetPath := coverPath
		// 优先使用 ffmpeg 转码为目标格式（仅常见格式）
		if _, err := exec.LookPath("ffmpeg"); err == nil {
			// 构建 ffmpeg 转码参数
			args := []string{"-y", "-i", tempPath}
			switch strings.ToLower(ext) {
			case ".jpg", ".jpeg":
				args = append(args, "-q:v", "2")
			}
			args = append(args, targetPath)
			if out, convErr := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); convErr != nil {
				// 转码失败则回退为直接重命名到实际扩展
				logger.Warn().Err(convErr).Str("output", string(out)).Str("from", actualExt).Str("to", ext).Msg("封面转码失败，回退为实际格式")
				fallbackPath := filepath.Join(videoDir, "cover"+actualExt)
				_ = os.Rename(tempPath, fallbackPath)
				coverPath = fallbackPath
				ext = actualExt
			} else {
				// 转码成功
				_ = os.Remove(tempPath)
			}
		} else {
			// 无转码工具，直接按实际格式保存
			logger.Warn().Str("desired_ext", ext).Str("actual_ext", actualExt).Msg("未检测到 ffmpeg，按实际格式保存封面图")
			fallbackPath := filepath.Join(videoDir, "cover"+actualExt)
			if err := os.Rename(tempPath, fallbackPath); err != nil {
				os.Remove(tempPath)
				return "", fmt.Errorf("重命名缩略图失败: %w", err)
			}
			coverPath = fallbackPath
			ext = actualExt
		}
	} else {
		// 如果扩展名与目标一致，直接重命名
		if err := os.Rename(tempPath, coverPath); err != nil {
			os.Remove(tempPath)
			return "", fmt.Errorf("重命名缩略图失败: %w", err)
		}
	}

	logger.Info().Str("cover_path", coverPath).Msg("缩略图已下载为 cover.{ext} 格式")
	return coverPath, nil
}

// extractExtensionFromURL 从 URL 中提取文件扩展名
func (s *downloadService) extractExtensionFromURL(url string) string {
	// 移除查询参数
	urlWithoutQuery := strings.Split(url, "?")[0]
	// 提取扩展名
	ext := filepath.Ext(urlWithoutQuery)
	if ext == "" {
		return ""
	}
	// 转换为小写并确保以 . 开头
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

// detectImageExtension 检测图片文件的实际扩展名
func (s *downloadService) detectImageExtension(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return ".jpg" // 默认返回 .jpg
	}
	defer file.Close()

	// 读取文件头部的几个字节来检测图片类型
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return ".jpg"
	}
	if n < 4 {
		return ".jpg"
	}

	// 检测常见的图片格式
	// JPEG: FF D8 FF
	if n >= 3 && buffer[0] == 0xFF && buffer[1] == 0xD8 && buffer[2] == 0xFF {
		return ".jpg"
	}
	// PNG: 89 50 4E 47
	if n >= 4 && buffer[0] == 0x89 && buffer[1] == 0x50 && buffer[2] == 0x4E && buffer[3] == 0x47 {
		return ".png"
	}
	// GIF: 47 49 46 38
	if n >= 4 && buffer[0] == 0x47 && buffer[1] == 0x49 && buffer[2] == 0x46 && buffer[3] == 0x38 {
		return ".gif"
	}
	// WebP: RIFF ... WEBP
	if n >= 12 && string(buffer[0:4]) == "RIFF" && string(buffer[8:12]) == "WEBP" {
		return ".webp"
	}

	// 如果无法检测，默认返回 .jpg
	return ".jpg"
}

// generateCoverFromVideo 从视频第一帧生成封面图
func (s *downloadService) generateCoverFromVideo(ctx context.Context, videoDir, videoPath string) error {
	// 检查是否有 ffmpeg
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		logger.Debug().Msg("未检测到 ffmpeg，跳过生成封面图")
		return nil
	}

	// 封面图路径
	coverPath := filepath.Join(videoDir, "cover.jpg")

	// 使用 ffmpeg 提取视频第一帧
	// -ss 0: 从第 0 秒开始
	// -vframes 1: 只提取 1 帧
	// -q:v 2: 高质量 JPEG（1-31，数字越小质量越高，2 是高质量）
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath,
		"-ss", "00:00:00",
		"-vframes", "1",
		"-q:v", "2",
		"-y", // 覆盖已存在的文件
		coverPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("提取视频第一帧失败: %w, 输出: %s", err, string(output))
	}

	// 检查文件是否成功创建
	if _, err := os.Stat(coverPath); err != nil {
		return fmt.Errorf("封面图文件未创建: %w", err)
	}

	logger.Info().
		Str("cover_path", coverPath).
		Str("video_path", videoPath).
		Msg("已从视频第一帧生成封面图")

	return nil
}

// buildVideoInfoFromRawData 从原始 yt-dlp JSON 数据构建 VideoInfo
func (s *downloadService) buildVideoInfoFromRawData(
	rawData map[string]interface{},
	videoID, title, videoURL string,
	subtitleMap map[string]string,
	thumbnails []file.Thumbnail,
) *file.VideoInfo {
	videoInfo := &file.VideoInfo{
		ID:         videoID,
		Title:      title,
		URL:        videoURL,
		Subtitles:  subtitleMap,
		Thumbnails: thumbnails,
		RawData:    rawData,
	}

	if rawData == nil {
		return videoInfo
	}

	// 提取基本信息
	if val, ok := rawData["webpage_url"].(string); ok {
		videoInfo.WebpageURL = val
	}
	if val, ok := rawData["original_url"].(string); ok {
		videoInfo.OriginalURL = val
	}

	// 提取时长和日期
	if val, ok := rawData["duration"].(float64); ok {
		videoInfo.Duration = val
	}
	if val, ok := rawData["duration_string"].(string); ok {
		videoInfo.DurationString = val
	}
	if val, ok := rawData["upload_date"].(string); ok {
		videoInfo.UploadDate = val
	}
	if val, ok := rawData["release_year"].(float64); ok {
		year := int(val)
		videoInfo.ReleaseYear = &year
	}

	// 提取描述
	if val, ok := rawData["description"].(string); ok {
		videoInfo.Description = val
	}

	// 提取频道信息
	if val, ok := rawData["channel_id"].(string); ok {
		videoInfo.ChannelID = val
	}
	if val, ok := rawData["channel"].(string); ok {
		videoInfo.Channel = val
	}
	if val, ok := rawData["channel_url"].(string); ok {
		videoInfo.ChannelURL = val
	}
	if val, ok := rawData["uploader"].(string); ok {
		videoInfo.Uploader = val
	}
	if val, ok := rawData["uploader_id"].(string); ok {
		videoInfo.UploaderID = val
	}
	if val, ok := rawData["uploader_url"].(string); ok {
		videoInfo.UploaderURL = val
	}

	// 提取播放列表信息
	if val, ok := rawData["playlist_count"].(float64); ok {
		videoInfo.PlaylistCount = int(val)
	}
	if val, ok := rawData["playlist"].(string); ok {
		videoInfo.Playlist = val
	}
	if val, ok := rawData["playlist_id"].(string); ok {
		videoInfo.PlaylistID = val
	}
	if val, ok := rawData["playlist_title"].(string); ok {
		videoInfo.PlaylistTitle = val
	}
	if val, ok := rawData["playlist_uploader"].(string); ok {
		videoInfo.PlaylistUploader = val
	}
	if val, ok := rawData["playlist_uploader_id"].(string); ok {
		videoInfo.PlaylistUploaderID = val
	}
	if val, ok := rawData["playlist_channel"].(string); ok {
		videoInfo.PlaylistChannel = val
	}
	if val, ok := rawData["playlist_channel_id"].(string); ok {
		videoInfo.PlaylistChannelID = val
	}
	if val, ok := rawData["playlist_webpage_url"].(string); ok {
		videoInfo.PlaylistWebpageURL = val
	}
	if val, ok := rawData["playlist_index"].(float64); ok {
		videoInfo.PlaylistIndex = int(val)
	}
	if val, ok := rawData["n_entries"].(float64); ok {
		videoInfo.NEntries = int(val)
	}

	// 提取统计信息
	if val, ok := rawData["view_count"].(float64); ok {
		count := int64(val)
		videoInfo.ViewCount = &count
	}
	if val, ok := rawData["live_status"].(string); ok {
		videoInfo.LiveStatus = val
	}
	if val, ok := rawData["channel_is_verified"].(bool); ok {
		videoInfo.ChannelIsVerified = &val
	}

	// 提取提取器信息
	if val, ok := rawData["extractor"].(string); ok {
		videoInfo.Extractor = val
	}
	if val, ok := rawData["extractor_key"].(string); ok {
		videoInfo.ExtractorKey = val
	}

	// 提取时间戳
	if val, ok := rawData["timestamp"].(float64); ok {
		ts := int64(val)
		videoInfo.Timestamp = &ts
	}
	if val, ok := rawData["release_timestamp"].(float64); ok {
		ts := int64(val)
		videoInfo.ReleaseTimestamp = &ts
	}
	if val, ok := rawData["epoch"].(float64); ok {
		ts := int64(val)
		videoInfo.Epoch = &ts
	}

	// 提取其他
	if val, ok := rawData["availability"].(string); ok {
		videoInfo.Availability = val
	}

	return videoInfo
}

// copyFile 复制文件
func (s *downloadService) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("复制文件失败: %w", err)
	}

	return nil
}

// extractSubtitleURLs 从 rawData 中提取字幕URL（仅提取配置中指定的语言）
func (s *downloadService) extractSubtitleURLs(rawData map[string]interface{}, languages []string) map[string]string {
	subtitleURLs := make(map[string]string)

	if rawData == nil {
		return subtitleURLs
	}

	// 如果 languages 为空，不提取字幕URL
	if len(languages) == 0 {
		return subtitleURLs
	}

	// 创建一个语言代码的映射，用于快速查找
	langMap := make(map[string]bool)
	for _, lang := range languages {
		langMap[lang] = true
		// 也支持变体，例如 zh-Hans 匹配 zh
		if strings.Contains(lang, "-") {
			langMap[strings.Split(lang, "-")[0]] = true
		}
	}

	// 提取手动字幕 URL
	// yt-dlp 的 subtitles 格式：map[lang][]map[string]interface{}
	if subtitles, ok := rawData["subtitles"].(map[string]interface{}); ok {
		for lang, langData := range subtitles {
			// 检查是否是配置中指定的语言
			if !langMap[lang] && !langMap[strings.Split(lang, "-")[0]] {
				continue
			}
			// langData 可能是 []interface{}（多个格式）
			if formats, ok := langData.([]interface{}); ok {
				for _, format := range formats {
					if formatMap, ok := format.(map[string]interface{}); ok {
						if url, ok := formatMap["url"].(string); ok && url != "" {
							subtitleURLs[lang] = url
							break
						}
					}
				}
			}
		}
	}

	// 提取自动字幕 URL（优先级较低，如果手动字幕已存在则不覆盖）
	if autoSubtitles, ok := rawData["automatic_captions"].(map[string]interface{}); ok {
		for lang, langData := range autoSubtitles {
			// 检查是否是配置中指定的语言，且手动字幕不存在
			if _, exists := subtitleURLs[lang]; exists {
				continue
			}
			if !langMap[lang] && !langMap[strings.Split(lang, "-")[0]] {
				continue
			}
			// langData 可能是 []interface{}（多个格式）
			if formats, ok := langData.([]interface{}); ok {
				for _, format := range formats {
					if formatMap, ok := format.(map[string]interface{}); ok {
						if url, ok := formatMap["url"].(string); ok && url != "" {
							subtitleURLs[lang] = url
							break
						}
					}
				}
			}
		}
	}

	return subtitleURLs
}
