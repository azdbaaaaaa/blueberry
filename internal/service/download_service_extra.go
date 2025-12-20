package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"blueberry/internal/config"
	"blueberry/internal/repository/youtube"
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

	// 计算有效的 offset/limit（命令行覆盖配置）
	offset := 0
	limit := 0
	if s.cfg.YouTube.OffsetOverride != 0 || s.cfg.YouTube.LimitOverride != 0 {
		offset = s.cfg.YouTube.OffsetOverride
		limit = s.cfg.YouTube.LimitOverride
	} else {
		offset = channel.Offset
		limit = channel.Limit
	}
	if offset < 0 {
		offset = 0
	}
	start := offset
	end := len(videoMaps)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	if start > len(videoMaps) {
		start = len(videoMaps)
	}
	if start < end {
		videoMaps = videoMaps[start:end]
	} else {
		videoMaps = []map[string]interface{}{}
	}

	logger.Info().
		Int("count", len(videoMaps)).
		Int("offset", offset).
		Int("limit", limit).
		Msg("从文件加载视频列表（已应用 offset/limit）")

	// 获取频道语言配置
	languages := s.getChannelLanguages(channel)

	// 检查是否在休息期间
	if inRest, restUntil, err := s.fileManager.IsInRestPeriod(); err == nil {
		if inRest {
			logger.Info().
				Time("rest_until", restUntil).
				Dur("remaining", time.Until(restUntil)).
				Msg("检测到正在休息期间，等待休息结束")
			s.sleepUntilRestEnd(ctx, restUntil)
			// 休息结束后，计数器已重置，继续下载
			logger.Info().Msg("休息结束，继续下载")
		} else {
			// 加载当前下载计数
			count, countErr := s.fileManager.GetTodayDownloadCount()
			if countErr == nil {
				logger.Info().
					Int("current_count", count).
					Int("limit", s.getDownloadLimit()).
					Msg("开始下载，当前下载计数")
			}
		}
	} else {
		logger.Warn().Err(err).Msg("检查休息状态失败")
	}

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

		// 若之前被标记为“不可下载”，按配置决定是否跳过
		videoDir := filepath.Join(channelDir, videoID)
		if st, dl, errMsg, e := s.fileManager.GetDownloadVideoStatus(videoDir); e == nil {
			if !s.cfg.YouTube.ForceDownloadUndownloadable && st == "failed" && !dl &&
				(strings.Contains(errMsg, "不可下载") || strings.Contains(errMsg, "未找到可用格式")) {
				logger.Warn().
					Str("video_id", videoID).
					Str("video_dir", videoDir).
					Str("error", errMsg).
					Msg("此前标记为不可下载，按配置跳过此视频（可开启 youtube.force_download_undownloadable 强制下载）")
				continue
			}
		}

		// 检查是否达到下载限制（每N个视频后休息）
		if s.isDownloadLimitReached() {
			logger.Warn().
				Int("current_count", s.dailyDownloadCount).
				Int("limit", s.getDownloadLimit()).
				Msg("达到下载限制，准备休息")
			s.restAfterLimit(ctx)
			// 休息结束后，计数器已重置，继续下载
			logger.Info().Msg("休息结束，继续下载下一个视频")
		}

		// 使用统一的下载逻辑
		// 判断调用前后是否真的触发了下载（用于决定是否添加间隔）
		downloadedBefore := s.fileManager.IsVideoDownloaded(videoDir)
		if err := s.downloadVideoAndSaveInfo(ctx, channelID, videoID, title, url, languages, videoMap); err != nil {
			// 检查是否是 bot detection 错误
			if s.isBotDetectionError(err) {
				s.handleBotDetection(ctx)
			}
			logger.Error().
				Str("video_id", videoID).
				Str("title", title).
				Err(err).
				Msg("下载视频失败，继续处理下一个")
			continue
		}

		// 如果成功下载了新视频，增加计数器
		downloadedAfter := s.fileManager.IsVideoDownloaded(videoDir)
		if downloadedAfter && !downloadedBefore {
			s.incrementDownloadCounter(ctx)
		}

		// 在下载每个视频后添加延迟，避免触发 429 错误
		// 字幕下载已经通过 --sleep-subtitles 参数添加了延迟，这里再添加一个整体延迟
		// 使用配置的 sleep_interval_seconds 作为基础值，加上 0%-50% 的随机变化
		if i < len(videoMaps)-1 && downloadedAfter && !downloadedBefore {
			baseDelay := 3 * time.Second // 默认值
			if s.cfg != nil && s.cfg.YouTube.SleepIntervalSeconds > 0 {
				baseDelay = time.Duration(s.cfg.YouTube.SleepIntervalSeconds) * time.Second
			}
			// 添加 0%-50% 的随机变化
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			randomFactor := 1.0 + rng.Float64()*0.5 // 1.0 到 1.5 之间的随机值
			delay := time.Duration(float64(baseDelay) * randomFactor)
			logger.Debug().Dur("delay", delay).Dur("base_delay", baseDelay).Float64("random_factor", randomFactor).Msg("下载完成，等待后继续下一个视频")
			time.Sleep(delay)
		}
	}

	return nil
}

// incrementDownloadCounter 增加下载计数器（持久化到文件）
func (s *downloadService) incrementDownloadCounter(ctx context.Context) {
	// 先获取当前计数
	oldCount, _ := s.fileManager.GetTodayDownloadCount()

	err := s.fileManager.IncrementTodayDownloadCount()
	if err != nil {
		logger.Warn().Err(err).Msg("保存下载计数失败，使用内存计数器")
		s.dailyDownloadCount++
		logger.Info().
			Int("old_count", oldCount).
			Int("new_count", s.dailyDownloadCount).
			Int("limit", s.getDownloadLimit()).
			Msg("下载计数更新（内存计数器）")
	} else {
		count, loadErr := s.fileManager.GetTodayDownloadCount()
		if loadErr == nil {
			s.dailyDownloadCount = count
			remaining := s.getDownloadLimit() - s.dailyDownloadCount
			logger.Info().
				Int("old_count", oldCount).
				Int("new_count", s.dailyDownloadCount).
				Int("limit", s.getDownloadLimit()).
				Int("remaining", remaining).
				Msg("下载计数更新（已保存到文件）")
		} else {
			s.dailyDownloadCount++
			logger.Warn().Err(loadErr).Msg("加载下载计数失败，使用内存计数器")
			logger.Info().
				Int("old_count", oldCount).
				Int("new_count", s.dailyDownloadCount).
				Int("limit", s.getDownloadLimit()).
				Msg("下载计数更新（内存计数器）")
		}
	}

	// 检查是否达到限制
	if s.isDownloadLimitReached() {
		s.restAfterLimit(ctx)
	}
}

// isDownloadLimitReached 检查是否达到下载限制（每N个视频后休息）
func (s *downloadService) isDownloadLimitReached() bool {
	limit := s.getDownloadLimit()
	if limit <= 0 {
		logger.Debug().Int("limit", limit).Msg("下载限制已禁用")
		return false // 0 或负数表示不限制
	}
	// 优先从文件加载，确保准确性
	count, err := s.fileManager.GetTodayDownloadCount()
	if err != nil {
		logger.Warn().Err(err).Msg("加载下载计数失败，使用内存计数器")
		// 如果加载失败，使用内存计数器
		reached := s.dailyDownloadCount >= limit
		if reached {
			logger.Info().
				Int("count", s.dailyDownloadCount).
				Int("limit", limit).
				Msg("达到下载限制（使用内存计数器）")
		}
		return reached
	}
	// 更新内存计数器
	s.dailyDownloadCount = count
	reached := count >= limit
	if reached {
		logger.Info().
			Int("count", count).
			Int("limit", limit).
			Msg("达到下载限制（从文件加载）")
	} else {
		logger.Debug().
			Int("count", count).
			Int("limit", limit).
			Int("remaining", limit-count).
			Msg("检查下载限制：未达到")
	}
	return reached
}

// getDownloadLimit 获取下载限制（每N个视频后休息）
func (s *downloadService) getDownloadLimit() int {
	if s.cfg != nil && s.cfg.YouTube.VideoLimitBeforeRest > 0 {
		return s.cfg.YouTube.VideoLimitBeforeRest
	}
	return 60 // 默认值
}

// restAfterLimit 达到限制后休息
func (s *downloadService) restAfterLimit(ctx context.Context) {
	// 获取休息时长配置
	restBase := 120 // 默认2小时 = 120分钟
	if s.cfg != nil && s.cfg.YouTube.VideoLimitRestDuration > 0 {
		restBase = s.cfg.YouTube.VideoLimitRestDuration
	}

	// 生成随机休息时间：基础时间 + 0-10% 的随机变化
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomFactor := 1.0 + rng.Float64()*0.1 // 1.0 到 1.1 之间的随机值
	restMinutes := int(float64(restBase) * randomFactor)
	restDuration := time.Duration(restMinutes) * time.Minute
	restUntil := time.Now().Add(restDuration)

	logger.Warn().
		Int("download_count", s.dailyDownloadCount).
		Int("limit", s.getDownloadLimit()).
		Int("rest_base_minutes", restBase).
		Int("rest_minutes", restMinutes).
		Dur("rest_duration", restDuration).
		Time("rest_until", restUntil).
		Msg("达到下载限制，开始休息")

	// 保存休息时间到文件
	if err := s.fileManager.SetDownloadRestUntil(restUntil, restMinutes); err != nil {
		logger.Warn().Err(err).Msg("保存休息时间失败")
	} else {
		logger.Info().
			Time("rest_until", restUntil).
			Int("rest_minutes", restMinutes).
			Msg("休息时间已保存到文件")
	}

	// 休息
	timer := time.NewTimer(restDuration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		logger.Info().Msg("休息被取消")
		return
	case <-timer.C:
		// 验证计数器已重置
		count, countErr := s.fileManager.GetTodayDownloadCount()
		if countErr == nil {
			logger.Info().
				Int("reset_count", count).
				Msg("休息结束，下载计数器已重置，继续下载")
		} else {
			logger.Info().Err(countErr).Msg("休息结束，继续下载")
		}
		// 计数器已在 SetDownloadRestUntil 中重置
		return
	}
}

// sleepUntilRestEnd 休眠到休息结束时间
func (s *downloadService) sleepUntilRestEnd(ctx context.Context, restUntil time.Time) {
	duration := time.Until(restUntil)
	if duration <= 0 {
		logger.Info().
			Time("rest_until", restUntil).
			Msg("休息时间已过，继续下载")
		// 确保计数器已重置
		if err := s.fileManager.ResetTodayDownloadCount(); err != nil {
			logger.Warn().Err(err).Msg("重置下载计数器失败")
		} else {
			logger.Info().Msg("已重置下载计数器")
		}
		return
	}

	logger.Info().
		Dur("sleep_duration", duration).
		Time("wake_up_time", restUntil).
		Str("duration_hours", fmt.Sprintf("%.2f", duration.Hours())).
		Msg("检测到休息期间，等待休息结束")

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		logger.Info().Msg("休息被取消")
		return
	case <-timer.C:
		// 验证计数器已重置
		count, countErr := s.fileManager.GetTodayDownloadCount()
		if countErr == nil {
			logger.Info().
				Int("reset_count", count).
				Time("rest_until", restUntil).
				Msg("休息结束，下载计数器已重置，继续下载")
		} else {
			logger.Info().
				Time("rest_until", restUntil).
				Msg("休息结束，继续下载")
		}
		return
	}
}

// isBotDetectionError 检查错误是否是 bot detection
func (s *downloadService) isBotDetectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "bot detection") ||
		strings.Contains(errStr, "sign in to confirm you're not a bot") ||
		strings.Contains(errStr, "confirm you're not a bot") ||
		strings.Contains(errStr, "authentication") ||
		errors.Is(err, youtube.ErrBotDetection)
}

// handleBotDetection 处理 bot detection，累计计数并在达到阈值时休息
func (s *downloadService) handleBotDetection(ctx context.Context) {
	// 获取触发阈值配置
	threshold := 10 // 默认10次
	if s.cfg != nil && s.cfg.YouTube.BotDetectionThreshold > 0 {
		threshold = s.cfg.YouTube.BotDetectionThreshold
	}

	// 从文件加载当前计数
	currentCount, err := s.fileManager.GetBotDetectionCount()
	if err != nil {
		logger.Warn().Err(err).Msg("加载 bot detection 计数失败，使用内存计数器")
		s.botDetectionCount++
		currentCount = s.botDetectionCount
	} else {
		// 增加计数并保存到文件
		if err := s.fileManager.IncrementBotDetectionCount(); err != nil {
			logger.Warn().Err(err).Msg("保存 bot detection 计数失败，使用内存计数器")
			s.botDetectionCount++
			currentCount = s.botDetectionCount
		} else {
			// 重新加载以获取最新值
			if newCount, loadErr := s.fileManager.GetBotDetectionCount(); loadErr == nil {
				currentCount = newCount
				s.botDetectionCount = newCount
			} else {
				s.botDetectionCount++
				currentCount = s.botDetectionCount
			}
		}
	}

	logger.Warn().
		Int("bot_detection_count", currentCount).
		Int("threshold", threshold).
		Msg("检测到 bot detection，累计计数")

	// 达到阈值时，休息
	if currentCount >= threshold {
		// 获取休息时长配置
		restBase := 60 // 默认1小时 = 60分钟
		if s.cfg != nil && s.cfg.YouTube.BotDetectionRestDuration > 0 {
			restBase = s.cfg.YouTube.BotDetectionRestDuration
		}

		// 生成随机休息时间：基础时间 + 0-10% 的随机变化
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		randomFactor := 1.0 + rng.Float64()*0.1 // 1.0 到 1.1 之间的随机值
		restMinutes := int(float64(restBase) * randomFactor)
		restDuration := time.Duration(restMinutes) * time.Minute

		logger.Warn().
			Int("bot_detection_count", currentCount).
			Int("threshold", threshold).
			Int("rest_base_minutes", restBase).
			Int("rest_minutes", restMinutes).
			Dur("rest_duration", restDuration).
			Msg("Bot detection 累计达到阈值，开始休息")

		// 休息
		timer := time.NewTimer(restDuration)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			logger.Info().Msg("休息被取消")
			return
		case <-timer.C:
			logger.Info().Msg("休息结束，重置 bot detection 计数器")
			// 重置计数器（保存到文件）
			if err := s.fileManager.ResetBotDetectionCount(); err != nil {
				logger.Warn().Err(err).Msg("重置 bot detection 计数失败")
			} else {
				s.botDetectionCount = 0
			}
			return
		}
	}
}

// sleepUntilNextDay 休眠到第二天
func sleepUntilNextDay(ctx context.Context) {
	now := time.Now()
	// 计算明天的 00:00:00
	nextDay := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	duration := nextDay.Sub(now)

	logger.Info().
		Dur("sleep_duration", duration).
		Time("wake_up_time", nextDay).
		Msg("已达到每日下载限制，休眠到第二天")

	// 使用 context 的 Done channel 来支持取消
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		logger.Info().Msg("休眠被取消")
		return
	case <-timer.C:
		logger.Info().Msg("休眠结束，新的一天开始")
		return
	}
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

	// 若之前被标记为“不可下载”，按配置决定是否跳过（单视频目录模式）
	if st, dl, errMsg, e := s.fileManager.GetDownloadVideoStatus(videoDir); e == nil {
		if !s.cfg.YouTube.ForceDownloadUndownloadable && st == "failed" && !dl &&
			(strings.Contains(errMsg, "不可下载") || strings.Contains(errMsg, "未找到可用格式")) {
			logger.Warn().
				Str("video_id", videoID).
				Str("video_dir", videoDir).
				Str("error", errMsg).
				Msg("此前标记为不可下载，按配置跳过此视频（可开启 youtube.force_download_undownloadable 强制下载）")
			return nil
		}
	}

	return s.downloadVideoAndSaveInfo(ctx, channelID, videoID, videoInfo.Title, videoURL, languages, rawData)
}
