package youtube

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"blueberry/internal/repository/file"
	"blueberry/pkg/logger"
	"blueberry/pkg/subtitle"
)

type Downloader interface {
	DownloadVideo(ctx context.Context, channelID, videoURL string, languages []string, title string) (*DownloadResult, error)
}

type DownloadResult struct {
	VideoPath     string
	VideoTitle    string
	SubtitlePaths []string
	Error         error
}

type downloader struct {
	fileRepo           file.Repository
	cookiesFromBrowser string
	cookiesFile        string
}

func NewDownloader(fileRepo file.Repository, cookiesFromBrowser, cookiesFile string) Downloader {
	return &downloader{
		fileRepo:           fileRepo,
		cookiesFromBrowser: cookiesFromBrowser,
		cookiesFile:        cookiesFile,
	}
}

func (d *downloader) DownloadVideo(ctx context.Context, channelID, videoURL string, languages []string, title string) (*DownloadResult, error) {
	result := &DownloadResult{
		SubtitlePaths: make([]string, 0),
	}

	videoID := d.fileRepo.ExtractVideoID(videoURL)

	// 如果提供了 title，使用 title 创建目录；否则使用 videoID（兼容旧逻辑）
	var videoDir string
	var err error
	if title != "" {
		videoDir, err = d.fileRepo.EnsureVideoDirByTitle(channelID, title)
	} else {
		videoDir, err = d.fileRepo.EnsureVideoDir(channelID, videoID)
	}
	if err != nil {
		return nil, err
	}

	args := d.buildDownloadArgs(videoDir, videoURL, languages)

	// 记录完整的命令（用于调试）
	fullCommand := "yt-dlp " + strings.Join(args, " ")
	logger.Debug().
		Str("command", fullCommand).
		Str("video_url", videoURL).
		Str("video_dir", videoDir).
		Msg("执行下载命令")

	// 智能重试机制：区分认证错误和网络错误
	maxRetries := 3
	var lastErr error
	var lastOutput string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		output, err := cmd.CombinedOutput()
		outputStr := string(output)

		if err == nil {
			// 成功，返回结果
			videoFile, err := d.fileRepo.FindVideoFile(videoDir)
			if err != nil {
				return nil, fmt.Errorf("查找视频文件失败: %w", err)
			}
			result.VideoPath = videoFile

			subtitleFiles, err := d.fileRepo.FindSubtitleFiles(videoDir)
			if err == nil {
				result.SubtitlePaths = subtitleFiles
				// 如果下载的是 VTT 格式，尝试转换为 SRT
				result.SubtitlePaths = d.convertVTTToSRTIfNeeded(videoDir, result.SubtitlePaths)
				// 将毫秒格式的 SRT 转换为帧格式（保存到新文件）
				result.SubtitlePaths = d.convertSRTToFrameFormatIfNeeded(videoDir, result.SubtitlePaths)
			}

			result.VideoTitle = d.fileRepo.ExtractVideoTitleFromFile(videoFile)
			return result, nil
		}

		lastErr = err
		lastOutput = outputStr

		// 检查是否是认证错误（bot detection）
		if strings.Contains(outputStr, "Sign in to confirm you're not a bot") ||
			strings.Contains(outputStr, "confirm you're not a bot") ||
			strings.Contains(outputStr, "authentication") {
			// 认证错误，重试通常没用，但可以尝试不同的策略
			logger.Warn().
				Int("attempt", attempt).
				Int("max_retries", maxRetries).
				Str("video_url", videoURL).
				Msg("检测到认证错误（bot detection），可能需要更新 cookies")

			// 如果是最后一次尝试，直接返回错误
			if attempt == maxRetries {
				break
			}

			// 尝试不同的 player client（仅最后一次重试时）
			if attempt == maxRetries-1 {
				// 修改 extractor-args，尝试只使用 web 客户端
				for i, arg := range args {
					if arg == "--extractor-args" && i+1 < len(args) {
						args[i+1] = "youtube:player_client=web"
						logger.Info().Msg("尝试使用 web 客户端重试")
						break
					}
				}
			}

			// 添加延迟（指数退避）
			delay := time.Duration(attempt*2) * time.Second
			logger.Info().Dur("delay", delay).Msg("等待后重试")
			time.Sleep(delay)
			continue
		}

		// 其他错误（网络错误等），可以重试
		logger.Warn().
			Int("attempt", attempt).
			Int("max_retries", maxRetries).
			Str("video_url", videoURL).
			Err(err).
			Msg("下载失败，准备重试")

		// 指数退避延迟
		delay := time.Duration(attempt*2) * time.Second
		time.Sleep(delay)
	}

	// 所有重试都失败了
	logger.Error().
		Str("command", fullCommand).
		Str("video_url", videoURL).
		Str("video_dir", videoDir).
		Str("output", lastOutput).
		Err(lastErr).
		Msg("下载失败，已重试所有次数")
	return nil, fmt.Errorf("下载失败: %w, 输出: %s", lastErr, lastOutput)

	videoFile, err := d.fileRepo.FindVideoFile(videoDir)
	if err != nil {
		return nil, fmt.Errorf("查找视频文件失败: %w", err)
	}
	result.VideoPath = videoFile

	subtitleFiles, err := d.fileRepo.FindSubtitleFiles(videoDir)
	if err == nil {
		result.SubtitlePaths = subtitleFiles
		// 如果下载的是 VTT 格式，尝试转换为 SRT
		result.SubtitlePaths = d.convertVTTToSRTIfNeeded(videoDir, result.SubtitlePaths)
	}

	result.VideoTitle = d.fileRepo.ExtractVideoTitleFromFile(videoFile)

	return result, nil
}

func (d *downloader) buildDownloadArgs(videoDir, videoURL string, languages []string) []string {
	args := []string{
		"-o", filepath.Join(videoDir, "%(title)s.%(ext)s"),
		"--no-warnings",
		// 使用 extractor-args 指定多个客户端，提高成功率
		// 优先使用 android 客户端（更不容易被检测）
		"--extractor-args", "youtube:player_client=android,ios,web",
		// 添加 User-Agent 模拟真实浏览器（使用最新的 Chrome）
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		// 添加 referer，模拟从 YouTube 页面访问
		"--referer", "https://www.youtube.com/",
		// 添加额外的 HTTP 头，模拟真实浏览器
		"--add-header", "Accept-Language:en-US,en;q=0.9",
		"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
		"--add-header", "Accept-Encoding:gzip, deflate, br",
		"--add-header", "DNT:1",
		"--add-header", "Connection:keep-alive",
		"--add-header", "Upgrade-Insecure-Requests:1",
		// 不指定 --format，让 yt-dlp 使用默认格式选择策略
		// yt-dlp 默认会选择最佳可用格式，自动处理各种情况
	}

	// 添加 cookies 支持（优先使用 cookies 文件，因为服务器上可能没有浏览器）
	if d.cookiesFile != "" {
		// 将相对路径转换为绝对路径，避免工作目录变化导致找不到文件
		cookiesPath := d.cookiesFile
		if !filepath.IsAbs(cookiesPath) {
			// 如果是相对路径，需要相对于当前工作目录解析
			// 注意：这里假设 cookies 文件在项目根目录或当前工作目录
			// 如果需要更精确的路径解析，可以在初始化时传入项目根目录
			if absPath, err := filepath.Abs(cookiesPath); err == nil {
				cookiesPath = absPath
				logger.Info().Str("original", d.cookiesFile).Str("resolved", cookiesPath).Msg("解析 cookies 文件路径")
			} else {
				logger.Error().Str("path", d.cookiesFile).Err(err).Msg("无法解析 cookies 文件路径，使用原始路径")
			}
		}
		// 检查文件是否存在（使用 INFO 级别，确保能看到）
		if fileInfo, err := os.Stat(cookiesPath); err != nil {
			logger.Error().Str("path", cookiesPath).Err(err).Msg("cookies 文件不存在或无法访问，下载可能失败")
		} else {
			logger.Info().Str("path", cookiesPath).Int64("size", fileInfo.Size()).Msg("cookies 文件存在")
		}
		args = append(args, "--cookies", cookiesPath)
	} else if d.cookiesFromBrowser != "" {
		logger.Info().Str("browser", d.cookiesFromBrowser).Msg("使用浏览器 cookies")
		args = append(args, "--cookies-from-browser", d.cookiesFromBrowser)
	} else {
		logger.Warn().Msg("未配置 cookies，某些视频可能无法下载")
	}

	// yt-dlp 支持通过 --sub-langs 指定多个语言（逗号分隔）
	// 也支持 --write-sub 和 --write-auto-sub 来下载手动和自动生成的字幕
	if len(languages) > 0 {
		args = append(args, "--write-sub", "--write-auto-sub")
		// 使用 --sub-langs 参数，多个语言用逗号分隔
		subLangs := strings.Join(languages, ",")
		args = append(args, "--sub-langs", subLangs)
	} else {
		// 如果 languages 为空，下载所有可用字幕
		args = append(args, "--write-sub", "--write-auto-sub", "--sub-langs", "all")
	}

	// 将字幕转换为 SRT 格式（更通用，兼容性更好）
	// YouTube 默认提供 VTT 格式，但 SRT 格式更广泛支持
	// 注意：转换需要 ffmpeg，如果系统没有安装 ffmpeg，则不转换，直接使用 VTT 格式
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		args = append(args, "--convert-subs", "srt")
		logger.Debug().Msg("检测到 ffmpeg，将字幕转换为 SRT 格式")
	} else {
		logger.Warn().Msg("未检测到 ffmpeg，将使用 VTT 格式字幕（无需转换）")
	}

	// 添加重试和错误处理参数，提高下载成功率
	args = append(args,
		"--retries", "3", // 重试 3 次
		"--fragment-retries", "3", // 片段重试 3 次
		"--skip-unavailable-fragments", // 跳过不可用的片段
	)

	// 添加请求延迟参数，降低被反爬虫检测的风险
	// --sleep-requests: 在请求之间延迟（秒），避免请求过于频繁
	args = append(args, "--sleep-requests", "1")
	// --sleep-interval: 在下载间隔之间延迟（秒），模拟真实用户行为
	args = append(args, "--sleep-interval", "2")
	// --sleep-subtitles: 在下载字幕之间延迟（秒），避免字幕请求过于频繁
	args = append(args, "--sleep-subtitles", "1")

	args = append(args, videoURL)

	return args
}

// convertVTTToSRTIfNeeded 如果需要，将 VTT 字幕转换为 SRT
// 如果系统没有 ffmpeg，yt-dlp 会下载 VTT 格式，这里我们手动转换
func (d *downloader) convertVTTToSRTIfNeeded(videoDir string, subtitlePaths []string) []string {
	// 检查是否有 ffmpeg（如果有，yt-dlp 应该已经转换了）
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		// 有 ffmpeg，yt-dlp 应该已经转换了，直接返回
		return subtitlePaths
	}

	// 没有 ffmpeg，检查是否有 VTT 文件需要转换
	var convertedPaths []string
	var hasVTT bool

	for _, path := range subtitlePaths {
		if strings.HasSuffix(strings.ToLower(path), ".vtt") {
			hasVTT = true
			// 转换为 SRT
			srtPath, err := subtitle.ConvertVTTToSRT(path)
			if err != nil {
				logger.Warn().
					Str("vtt_path", path).
					Err(err).
					Msg("VTT 转 SRT 失败，保留原文件")
				convertedPaths = append(convertedPaths, path)
			} else {
				logger.Info().
					Str("vtt_path", path).
					Str("srt_path", srtPath).
					Msg("VTT 已转换为 SRT")
				convertedPaths = append(convertedPaths, srtPath)
				// 可选：删除原 VTT 文件
				// if err := os.Remove(path); err != nil {
				// 	logger.Warn().Str("path", path).Err(err).Msg("删除 VTT 文件失败")
				// }
			}
		} else {
			// 已经是 SRT 或其他格式，直接添加
			convertedPaths = append(convertedPaths, path)
		}
	}

	if hasVTT {
		logger.Info().
			Int("converted_count", len(convertedPaths)).
			Msg("已使用纯 Go 实现将 VTT 转换为 SRT（无需 ffmpeg）")
	}

	return convertedPaths
}

// convertSRTToFrameFormatIfNeeded 如果需要，将毫秒格式的 SRT 转换为帧格式（保存到新文件）
func (d *downloader) convertSRTToFrameFormatIfNeeded(videoDir string, subtitlePaths []string) []string {
	var convertedPaths []string
	var hasConverted bool

	for _, path := range subtitlePaths {
		if strings.HasSuffix(strings.ToLower(path), ".srt") {
			// 检查是否是毫秒格式
			if subtitle.IsMillisecondFormat(path) {
				// 转换为帧格式（保存到新文件，文件名添加 .frame 后缀）
				frameSrtPath, err := subtitle.ConvertSRTToFrameFormat(path, 30.0)
				if err != nil {
					logger.Warn().
						Str("srt_path", path).
						Err(err).
						Msg("转换 SRT 为帧格式失败，保留原文件")
					convertedPaths = append(convertedPaths, path)
				} else {
					logger.Info().
						Str("original_path", path).
						Str("frame_path", frameSrtPath).
						Msg("毫秒格式 SRT 已转换为帧格式（保存到新文件）")
					convertedPaths = append(convertedPaths, frameSrtPath)
					hasConverted = true
				}
			} else {
				// 已经是帧格式，直接添加
				convertedPaths = append(convertedPaths, path)
			}
		} else {
			// 其他格式，直接添加
			convertedPaths = append(convertedPaths, path)
		}
	}

	if hasConverted {
		logger.Info().
			Int("converted_count", len(convertedPaths)).
			Msg("已将毫秒格式 SRT 转换为帧格式")
	}

	return convertedPaths
}
