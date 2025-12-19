package youtube

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"blueberry/internal/config"
	"blueberry/internal/repository/file"
	"blueberry/pkg/logger"
	"blueberry/pkg/subtitle"
)

var ErrBotDetection = errors.New("bot detection")

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

	// 使用视频ID创建目录（不再使用标题）
	videoDir, err := d.fileRepo.EnsureVideoDir(channelID, videoID)
	if err != nil {
		return nil, err
	}

	// 下载策略顺序：
	// 0) 最小化 + best 格式（bestvideo+bestaudio/best） → 1) web 无 cookies（严格 min_height） → 2) 休眠3s后 web 带 cookies（严格 min_height） → 3) 休眠3s后 android 无 cookies（严格 min_height，可配置关闭）
	type tryConf struct {
		client        string
		includeCookie bool
		sleepBefore   time.Duration
		minimal       bool
		useBest       bool
	}
	tries := []tryConf{
		// 优先使用 cookies（更稳定，减少风控），统一使用 best 选择器
		{client: "web", includeCookie: true, sleepBefore: 0, useBest: true},
		// 然后再尝试无 cookies
		{client: "web", includeCookie: false, sleepBefore: 3 * time.Second, useBest: true},
	}
	// 按配置决定是否添加 android 回退
	if cfg := config.Get(); cfg == nil || !cfg.YouTube.DisableAndroidFallback {
		tries = append(tries, tryConf{client: "android", includeCookie: false, sleepBefore: 3 * time.Second, useBest: true})
	} else {
		logger.Info().Msg("已启用 youtube.disable_android_fallback，跳过 android 回退策略")
	}

	var lastErr error
	var lastOutput string
	for i, t := range tries {
		if t.sleepBefore > 0 {
			time.Sleep(t.sleepBefore)
		}
		var args []string
		// 统一使用 bestvideo+bestaudio/best，避免触发更深风控，由下载结果再判断是否达到 1080p
		args = d.buildBestArgsWithClient(videoDir, videoURL, languages, t.client, t.includeCookie)
		logger.Debug().
			Int("strategy_index", i+1).
			Str("client", t.client).
			Bool("with_cookies", t.includeCookie).
			Str("command", "yt-dlp "+strings.Join(args, " ")).
			Msg("执行下载命令（按策略）")

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
				// 清理旧的 .frame.srt 文件（不再需要帧格式转换）
				d.cleanupFrameSrtFiles(videoDir)
				// 如果下载的是 VTT 格式，尝试转换为 SRT
				result.SubtitlePaths = d.convertVTTToSRTIfNeeded(videoDir, result.SubtitlePaths)
				// 去除字幕文件名中的分辨率标识（例如: id_1080p.en.srt -> id.en.srt）
				result.SubtitlePaths = d.stripResolutionFromSubtitleFilenames(videoDir, result.SubtitlePaths)
				// 检查字幕时间轴重叠（受配置开关控制）
				if cfg := config.Get(); cfg != nil && cfg.Subtitles.AutoFixOverlap {
					for _, subPath := range result.SubtitlePaths {
						if err := d.validateSubtitleOverlap(subPath); err != nil {
							logger.Warn().
								Str("subtitle_path", subPath).
								Err(err).
								Msg("字幕时间轴重叠检查失败")
						}
					}
				} else {
					logger.Debug().Msg("自动修复字幕时间轴重叠已禁用")
				}
				// 重命名字幕文件为 {video_id}_{lang}.{ext} 格式（暂时禁用）
				// result.SubtitlePaths = d.renameSubtitlesToIDFormat(videoDir, videoID, result.SubtitlePaths)
			}

			result.VideoTitle = d.fileRepo.ExtractVideoTitleFromFile(videoFile)
			// 从文件名解析高度，标记是否至少为 1080p
			if err := d.markHas1080p(videoDir, videoFile); err != nil {
				logger.Warn().Err(err).Msg("标记 has_1080p 失败（忽略）")
			}
			return result, nil
		}

		lastErr = err
		lastOutput = outputStr

		// 检查是否是认证错误（bot detection）
		if strings.Contains(outputStr, "Sign in to confirm you're not a bot") ||
			strings.Contains(outputStr, "confirm you're not a bot") ||
			strings.Contains(outputStr, "authentication") {
			// 不立即中止，尝试下一种策略
			logger.Warn().
				Int("strategy_index", i+1).
				Str("client", t.client).
				Bool("with_cookies", t.includeCookie).
				Msg("检测到 bot detection，继续尝试下一种策略")
			continue
		}

		// 获取退出码（若可用）
		exitCode := -1
		if ee, ok := err.(*exec.ExitError); ok && ee.ProcessState != nil {
			exitCode = ee.ProcessState.ExitCode()
		}
		logger.Warn().
			Int("strategy_index", i+1).
			Str("client", t.client).
			Bool("with_cookies", t.includeCookie).
			Int("exit_code", exitCode).
			Str("output_preview", previewForLog(outputStr, 800)).
			Err(err).
			Msg("下载失败，继续尝试下一种策略")
	}

	// 兜底：若是“无法提取 player response”，尝试最小化参数再试一次
	if strings.Contains(lastOutput, "Failed to extract any player response") {
		minHeight := 1080
		if cfg := config.Get(); cfg != nil && cfg.YouTube.MinHeight > 0 {
			minHeight = cfg.YouTube.MinHeight
		}
		minArgs := d.buildMinimalArgs(videoDir, videoURL, languages, minHeight, true)
		logger.Info().Msg("尝试使用最小化参数进行兜底下载")
		cmd := exec.CommandContext(ctx, "yt-dlp", minArgs...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			// 成功，返回结果
			videoFile, errFind := d.fileRepo.FindVideoFile(videoDir)
			if errFind != nil {
				return nil, fmt.Errorf("查找视频文件失败: %w", errFind)
			}
			result.VideoPath = videoFile
			if subtitleFiles, err := d.fileRepo.FindSubtitleFiles(videoDir); err == nil {
				result.SubtitlePaths = subtitleFiles
				d.cleanupFrameSrtFiles(videoDir)
				result.SubtitlePaths = d.convertVTTToSRTIfNeeded(videoDir, result.SubtitlePaths)
			}
			result.VideoTitle = d.fileRepo.ExtractVideoTitleFromFile(videoFile)
			return result, nil
		}
		// 覆盖最后输出，便于日志定位
		lastErr = err
		lastOutput = string(output)
	}

	// 所有尝试都失败了，但在返回错误前，检查视频文件是否已经成功下载
	// 某些情况下（如缺少 ffprobe），yt-dlp 可能返回错误但实际已下载视频
	videoFile, errFind := d.fileRepo.FindVideoFile(videoDir)
	if errFind == nil && videoFile != "" {
		// 视频文件存在，认为下载成功（即使 yt-dlp 返回了错误）
		logger.Warn().
			Str("video_url", videoURL).
			Str("video_dir", videoDir).
			Str("video_file", videoFile).
			Str("output", previewForLog(lastOutput, 200)).
			Msg("yt-dlp 返回错误，但检测到视频文件已存在，视为下载成功")
		result.VideoPath = videoFile
		if subtitleFiles, err := d.fileRepo.FindSubtitleFiles(videoDir); err == nil {
			result.SubtitlePaths = subtitleFiles
			d.cleanupFrameSrtFiles(videoDir)
			result.SubtitlePaths = d.convertVTTToSRTIfNeeded(videoDir, result.SubtitlePaths)
		}
		result.VideoTitle = d.fileRepo.ExtractVideoTitleFromFile(videoFile)
		// 从文件名解析高度，标记是否至少为 1080p
		if err := d.markHas1080p(videoDir, videoFile); err != nil {
			logger.Warn().Err(err).Msg("标记 has_1080p 失败（忽略）")
		}
		return result, nil
	}

	// 所有尝试都失败了，且视频文件不存在
	logger.Error().
		Str("video_url", videoURL).
		Str("video_dir", videoDir).
		Str("output", lastOutput).
		Err(lastErr).
		Msg("下载失败，已重试所有次数")
	return nil, fmt.Errorf("下载失败: %w, 输出: %s", lastErr, lastOutput)
}

func (d *downloader) buildDownloadArgs(videoDir, videoURL string, languages []string, playerClient string, includeCookies bool) []string {
	args := []string{}
	args = append(args, BuildYtDlpBaseArgs(videoDir)...)

	// 不设置 UA/Referer/额外 headers，使用默认行为
	// 如存在 Node，声明 JS runtime，提升兼容性
	// if _, err := exec.LookPath("node"); err == nil {
	// 	args = append(args, "--js-runtimes", "node")
	// }

	// 添加 cookies（按策略控制）
	if includeCookies {
		cookiesPath := d.cookiesFile
		if cookiesPath != "" && !filepath.IsAbs(cookiesPath) {
			if absPath, err := filepath.Abs(cookiesPath); err == nil {
				cookiesPath = absPath
				logger.Info().Str("original", d.cookiesFile).Str("resolved", cookiesPath).Msg("解析 cookies 文件路径")
			} else {
				logger.Error().Str("path", d.cookiesFile).Err(err).Msg("无法解析 cookies 文件路径，使用原始路径")
			}
		}
		if cookiesPath != "" {
			if fileInfo, err := os.Stat(cookiesPath); err != nil {
				logger.Error().Str("path", cookiesPath).Err(err).Msg("cookies 文件不存在或无法访问，下载可能失败")
			} else {
				logger.Info().Str("path", cookiesPath).Int64("size", fileInfo.Size()).Msg("cookies 文件存在")
			}
		}
		args = append(args, BuildYtDlpCookiesArgs(true, cookiesPath, d.cookiesFromBrowser)...)
	}

	// 字幕参数统一管理
	args = append(args, BuildYtDlpSubtitleArgs(languages)...)

	// 添加重试和错误处理参数，提高下载成功率
	args = append(args, BuildYtDlpStabilityArgs(config.Get())...)

	// 严格最低分辨率（从配置读取），达不到则失败
	minHeight := 1080
	if cfg := config.Get(); cfg != nil && cfg.YouTube.MinHeight > 0 {
		minHeight = cfg.YouTube.MinHeight
	}
	args = append(args, BuildYtDlpFormatArgsMinHeight(minHeight)...)

	// 稳定性参数在 BuildYtDlpStabilityArgs 中统一管理

	args = append(args, videoURL)

	return args
}

// choosePlayerClient 通过 yt-dlp --list-formats 预探测可用的 player_client
// 优先 android，若 android 不可用则回退 web；都不可用返回错误
func (d *downloader) choosePlayerClient(ctx context.Context, videoURL string) (string, string, error) {
	candidates := []string{"android", "web"}
	var lastOut string
	for _, client := range candidates {
		args := []string{
			"--force-ipv6",
			"--list-formats",
			"--extractor-args", fmt.Sprintf("youtube:player_client=%s", client),
		}
		// cookies
		if d.cookiesFile != "" {
			cookiesPath := d.cookiesFile
			if !filepath.IsAbs(cookiesPath) {
				if absPath, err := filepath.Abs(cookiesPath); err == nil {
					cookiesPath = absPath
				}
			}
			args = append(args, "--cookies", cookiesPath)
		} else if d.cookiesFromBrowser != "" {
			args = append(args, "--cookies-from-browser", d.cookiesFromBrowser)
		}
		args = append(args, videoURL)
		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		output, err := cmd.CombinedOutput()
		outStr := string(output)
		lastOut = outStr
		if err != nil {
			logger.Warn().
				Str("client", client).
				Err(err).
				Str("output_preview", previewForLog(outStr, 600)).
				Msg("list-formats 失败，尝试下一个 client")
			continue
		}
		// 简单可用性判定：包含“Available formats”且出现 mp4/m3u8/dash 关键字
		if strings.Contains(outStr, "Available formats") &&
			(strings.Contains(outStr, " mp4 ") || strings.Contains(outStr, "m3u8") || strings.Contains(outStr, "dash")) {
			// 额外记录若出现 SABR 提示
			if strings.Contains(outStr, "SABR") || strings.Contains(outStr, "nsig extraction failed") {
				logger.Warn().Str("client", client).Msg("list-formats 提示 SABR 或 nsig 警告，仍尝试该 client")
			}
			return client, outStr, nil
		}
		logger.Warn().
			Str("client", client).
			Str("output_preview", previewForLog(outStr, 600)).
			Msg("list-formats 未发现可用格式，尝试下一个 client")
	}
	return "", lastOut, fmt.Errorf("no usable formats for android/web")
}

func previewForLog(s string, limit int) string {
	if limit <= 0 || s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

// markHas1080p 依据文件名中的高度后缀或 ffprobe 结果，标记是否至少达到 1080p
func (d *downloader) markHas1080p(videoDir string, videoPath string) error {
	height := 0
	base := filepath.Base(videoPath)
	// 解析形如 *_1080p.*
	if idx := strings.LastIndex(base, "_"); idx >= 0 {
		rest := base[idx+1:]
		if j := strings.Index(rest, "p."); j > 0 {
			hstr := rest[:j]
			if n, err := strconv.Atoi(hstr); err == nil {
				height = n
			}
		}
	}
	// 若解析失败，可选：使用 ffprobe（仅当可用）
	if height == 0 {
		if _, err := exec.LookPath("ffprobe"); err == nil {
			cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=height", "-of", "csv=p=0", videoPath)
			if out, err := cmd.CombinedOutput(); err == nil {
				txt := strings.TrimSpace(string(out))
				if n, err := strconv.Atoi(txt); err == nil {
					height = n
				}
			}
		}
	}
	return d.fileRepo.SetVideoHas1080p(videoDir, height >= 1080)
}

// buildMinimalArgs 构建最小化的下载参数（用于失败兜底重试）
func (d *downloader) buildMinimalArgs(videoDir, videoURL string, languages []string, minHeight int, includeCookies bool) []string {
	args := []string{}
	args = append(args, BuildYtDlpBaseArgs(videoDir)...)
	if includeCookies {
		cookiesPath := d.cookiesFile
		if cookiesPath != "" && !filepath.IsAbs(cookiesPath) {
			if absPath, err := filepath.Abs(cookiesPath); err == nil {
				cookiesPath = absPath
			}
		}
		args = append(args, BuildYtDlpCookiesArgs(true, cookiesPath, d.cookiesFromBrowser)...)
	}
	args = append(args, BuildYtDlpSubtitleArgs(languages)...)
	if minHeight <= 0 {
		minHeight = 1080
	}
	args = append(args, BuildYtDlpFormatArgsMinHeight(minHeight)...)
	args = append(args, videoURL)
	return args
}

// buildBestArgs 构建使用 bestvideo+bestaudio/best 的下载参数（最小化 headers）
func (d *downloader) buildBestArgs(videoDir, videoURL string, languages []string) []string {
	args := []string{}
	args = append(args, BuildYtDlpBaseArgs(videoDir)...)
	args = append(args, BuildYtDlpSubtitleArgs(languages)...)
	args = append(args, BuildYtDlpStabilityArgs(config.Get())...)
	args = append(args, BuildYtDlpFormatArgsBest1080()...)
	args = append(args, videoURL)
	return args
}

// buildBestArgsWithClient best 格式下载，按 client/是否带 cookies 构建 UA/headers
func (d *downloader) buildBestArgsWithClient(videoDir, videoURL string, languages []string, playerClient string, includeCookies bool) []string {
	args := []string{}
	args = append(args, BuildYtDlpBaseArgs(videoDir)...)
	// 不设置 UA/Referer/额外 headers，使用默认行为
	// cookies
	if includeCookies {
		cookiesPath := d.cookiesFile
		if cookiesPath != "" && !filepath.IsAbs(cookiesPath) {
			if absPath, err := filepath.Abs(cookiesPath); err == nil {
				cookiesPath = absPath
			}
		}
		args = append(args, BuildYtDlpCookiesArgs(true, cookiesPath, d.cookiesFromBrowser)...)
	}
	// 字幕
	args = append(args, BuildYtDlpSubtitleArgs(languages)...)
	// 重试与片段/延迟参数
	args = append(args, BuildYtDlpStabilityArgs(config.Get())...)
	// 指定格式与容器
	args = append(args, BuildYtDlpFormatArgsBest1080()...)
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

// convertSRTToFrameFormatIfNeeded 如果需要，将毫秒格式的 SRT 转换为帧格式（直接覆盖原文件）
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
					// 将 .frame.srt 文件重命名为 .srt，覆盖原文件
					// 先删除原文件
					if err := os.Remove(path); err != nil {
						logger.Warn().
							Str("original_path", path).
							Err(err).
							Msg("删除原毫秒格式文件失败")
					}
					// 将 .frame.srt 重命名为 .srt
					finalPath := strings.TrimSuffix(frameSrtPath, ".frame.srt") + ".srt"
					if err := os.Rename(frameSrtPath, finalPath); err != nil {
						logger.Warn().
							Str("frame_path", frameSrtPath).
							Str("final_path", finalPath).
							Err(err).
							Msg("重命名帧格式文件失败，保留 .frame.srt 文件")
						convertedPaths = append(convertedPaths, frameSrtPath)
					} else {
						logger.Info().
							Str("original_path", path).
							Str("final_path", finalPath).
							Msg("毫秒格式 SRT 已转换为帧格式并覆盖原文件")
						convertedPaths = append(convertedPaths, finalPath)
						hasConverted = true
					}
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

// renameSubtitlesToIDFormat 将字幕文件重命名为 {video_id}_{lang}.{ext} 格式
// 输入格式可能是：{video_id}.{lang}.{ext} 或 {video_id}.{lang}.frame.srt
func (d *downloader) renameSubtitlesToIDFormat(videoDir, videoID string, subtitlePaths []string) []string {
	var renamedPaths []string

	for _, subtitlePath := range subtitlePaths {
		base := filepath.Base(subtitlePath)

		// 处理可能的 .frame.srt 后缀
		var ext string
		var nameWithoutExt string
		if strings.HasSuffix(strings.ToLower(base), ".frame.srt") {
			ext = ".frame.srt"
			nameWithoutExt = strings.TrimSuffix(base, ext)
		} else {
			ext = filepath.Ext(base)
			nameWithoutExt = strings.TrimSuffix(base, ext)
		}

		// 解析文件名，提取语言代码
		// yt-dlp 下载的字幕格式通常是：{video_id}.{lang}.{ext}
		// 例如：-QO7F45J32w.en.srt -> parts = ["-QO7F45J32w", "en"]
		parts := strings.Split(nameWithoutExt, ".")
		if len(parts) < 2 {
			// 如果无法解析，保持原文件名
			logger.Warn().Str("subtitle_path", subtitlePath).Msg("无法解析字幕文件名格式，保持原文件名")
			renamedPaths = append(renamedPaths, subtitlePath)
			continue
		}

		// 最后一个部分应该是语言代码
		// 如果格式是 {video_id}.{lang}.frame，则倒数第二个部分是语言代码
		lang := ""
		if len(parts) >= 2 {
			// 检查是否是 .frame 格式
			if len(parts) >= 3 && parts[len(parts)-1] == "frame" {
				lang = parts[len(parts)-2]
			} else {
				lang = parts[len(parts)-1]
			}
		}

		if lang == "" || lang == videoID {
			// 如果语言代码为空或等于视频ID，说明解析失败
			logger.Warn().Str("subtitle_path", subtitlePath).Str("lang", lang).Msg("无法提取语言代码，保持原文件名")
			renamedPaths = append(renamedPaths, subtitlePath)
			continue
		}

		// 构建新文件名：{video_id}_{lang}.{ext}
		// 如果扩展名是 .frame.srt，改为 .srt（因为转换后应该已经是 .srt 格式）
		finalExt := ext
		if ext == ".frame.srt" {
			finalExt = ".srt"
		}
		newName := fmt.Sprintf("%s_%s%s", videoID, lang, finalExt)
		newPath := filepath.Join(videoDir, newName)

		// 如果新文件已存在，先删除
		if _, err := os.Stat(newPath); err == nil {
			if err := os.Remove(newPath); err != nil {
				logger.Warn().Str("path", newPath).Err(err).Msg("删除已存在的字幕文件失败")
			}
		}

		// 重命名文件
		if err := os.Rename(subtitlePath, newPath); err != nil {
			logger.Warn().
				Str("old_path", subtitlePath).
				Str("new_path", newPath).
				Err(err).
				Msg("重命名字幕文件失败，保持原文件名")
			renamedPaths = append(renamedPaths, subtitlePath)
		} else {
			logger.Info().
				Str("old_path", subtitlePath).
				Str("new_path", newPath).
				Str("lang", lang).
				Msg("字幕文件已重命名为 {video_id}_{lang}.{ext} 格式")
			renamedPaths = append(renamedPaths, newPath)
		}
	}

	return renamedPaths
}

// validateSubtitleOverlap 检查字幕文件中的时间轴重叠，如果发现重叠则自动修复
func (d *downloader) validateSubtitleOverlap(subtitlePath string) error {
	// 只检查 SRT 文件
	if !strings.HasSuffix(strings.ToLower(subtitlePath), ".srt") {
		return nil
	}

	// 读取整个文件
	file, err := os.Open(subtitlePath)
	if err != nil {
		return fmt.Errorf("打开字幕文件失败: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取字幕文件失败: %w", err)
	}

	// 时间戳正则：支持毫秒格式 00:00:00,000 --> 00:00:00,000 和帧格式 00:00:00:00 --> 00:00:00:00
	timePattern := regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2})([,:])(\d{2,3})\s*-->\s*(\d{2}):(\d{2}):(\d{2})([,:])(\d{2,3})`)

	type timeEntry struct {
		startTime     float64
		endTime       float64
		lineIndex     int
		isMillisecond bool
		matches       []string
	}

	var entries []timeEntry

	// 解析所有时间戳
	for i, line := range lines {
		if timePattern.MatchString(line) {
			matches := timePattern.FindStringSubmatch(line)
			if len(matches) >= 11 {
				// 解析开始时间
				startH, _ := strconv.Atoi(matches[1])
				startM, _ := strconv.Atoi(matches[2])
				startS, _ := strconv.Atoi(matches[3])
				startMsOrFrame, _ := strconv.Atoi(matches[5])

				// 解析结束时间
				endH, _ := strconv.Atoi(matches[6])
				endM, _ := strconv.Atoi(matches[7])
				endS, _ := strconv.Atoi(matches[8])
				endMsOrFrame, _ := strconv.Atoi(matches[10])

				var startTime, endTime float64
				isMillisecond := matches[4] == ","

				// 判断是毫秒格式还是帧格式
				if isMillisecond {
					// 毫秒格式
					startTime = float64(startH)*3600 + float64(startM)*60 + float64(startS) + float64(startMsOrFrame)/1000.0
					endTime = float64(endH)*3600 + float64(endM)*60 + float64(endS) + float64(endMsOrFrame)/1000.0
				} else {
					// 帧格式（假设30fps）
					frameRate := 30.0
					startTime = float64(startH)*3600 + float64(startM)*60 + float64(startS) + float64(startMsOrFrame)/frameRate
					endTime = float64(endH)*3600 + float64(endM)*60 + float64(endS) + float64(endMsOrFrame)/frameRate
				}

				entries = append(entries, timeEntry{
					startTime:     startTime,
					endTime:       endTime,
					lineIndex:     i,
					isMillisecond: isMillisecond,
					matches:       matches,
				})
			}
		}
	}

	// 检查并修复重叠
	hasOverlap := false
	for i := 0; i < len(entries)-1; i++ {
		current := &entries[i]
		next := &entries[i+1]

		// 检查是否重叠：当前条目的结束时间 > 下一个条目的开始时间
		if current.endTime > next.startTime {
			hasOverlap = true
			// 修复重叠：将当前条目的结束时间调整为下一个条目的开始时间减去一个很小的间隔（10毫秒）
			// 确保至少保留10毫秒的间隔
			minGap := 0.01 // 10毫秒
			newEndTime := next.startTime - minGap

			// 确保新的结束时间不早于开始时间
			if newEndTime <= current.startTime {
				newEndTime = current.startTime + minGap
			}

			current.endTime = newEndTime

			// 更新文件中的对应行
			line := lines[current.lineIndex]
			newTimeStr := formatTimeRangeForLine(current.startTime, current.endTime, current.isMillisecond)
			lines[current.lineIndex] = timePattern.ReplaceAllString(line, newTimeStr)

			logger.Info().
				Str("subtitle_path", subtitlePath).
				Int("line", current.lineIndex+1).
				Str("old_time", extractTimeFromLine(line)).
				Str("new_time", newTimeStr).
				Msg("自动修复字幕时间轴重叠")
		}
	}

	// 如果有重叠并已修复，写回文件
	if hasOverlap {
		// 创建备份文件
		backupPath := subtitlePath + ".backup"
		if err := copyFile(subtitlePath, backupPath); err != nil {
			logger.Warn().Err(err).Msg("创建备份文件失败")
		} else {
			logger.Info().Str("backup_path", backupPath).Msg("已创建字幕文件备份")
		}

		// 写回修复后的内容
		outputFile, err := os.Create(subtitlePath)
		if err != nil {
			return fmt.Errorf("创建字幕文件失败: %w", err)
		}
		defer outputFile.Close()

		writer := bufio.NewWriter(outputFile)
		for _, line := range lines {
			if _, err := writer.WriteString(line + "\n"); err != nil {
				return fmt.Errorf("写入字幕文件失败: %w", err)
			}
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("刷新字幕文件失败: %w", err)
		}

		logger.Info().
			Str("subtitle_path", subtitlePath).
			Msg("已自动修复字幕时间轴重叠并保存")
	}

	return nil
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// extractTimeFromLine 从行中提取时间戳字符串
func extractTimeFromLine(line string) string {
	timePattern := regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2})([,:])(\d{2,3})\s*-->\s*(\d{2}):(\d{2}):(\d{2})([,:])(\d{2,3})`)
	matches := timePattern.FindStringSubmatch(line)
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// formatTimeRangeForLine 格式化时间范围用于替换行中的时间戳
func formatTimeRangeForLine(start, end float64, isMillisecond bool) string {
	startH := int(start) / 3600
	startM := (int(start) % 3600) / 60
	startS := int(start) % 60

	endH := int(end) / 3600
	endM := (int(end) % 3600) / 60
	endS := int(end) % 60

	if isMillisecond {
		startMs := int((start - float64(int(start))) * 1000)
		endMs := int((end - float64(int(end))) * 1000)
		return fmt.Sprintf("%02d:%02d:%02d,%03d --> %02d:%02d:%02d,%03d",
			startH, startM, startS, startMs,
			endH, endM, endS, endMs)
	} else {
		// 帧格式（假设30fps）
		frameRate := 30.0
		startFrame := int((start - float64(int(start))) * frameRate)
		endFrame := int((end - float64(int(end))) * frameRate)
		return fmt.Sprintf("%02d:%02d:%02d:%02d --> %02d:%02d:%02d:%02d",
			startH, startM, startS, startFrame,
			endH, endM, endS, endFrame)
	}
}

// formatTimeRange 格式化时间范围用于错误消息
func formatTimeRange(start, end float64) string {
	startH := int(start) / 3600
	startM := (int(start) % 3600) / 60
	startS := int(start) % 60
	startMs := int((start - float64(int(start))) * 1000)

	endH := int(end) / 3600
	endM := (int(end) % 3600) / 60
	endS := int(end) % 60
	endMs := int((end - float64(int(end))) * 1000)

	return fmt.Sprintf("%02d:%02d:%02d,%03d --> %02d:%02d:%02d,%03d",
		startH, startM, startS, startMs,
		endH, endM, endS, endMs)
}

// cleanupFrameSrtFiles 清理旧的 .frame.srt 文件（不再需要帧格式转换）
func (d *downloader) cleanupFrameSrtFiles(videoDir string) {
	entries, err := os.ReadDir(videoDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".frame.srt") {
			frameSrtPath := filepath.Join(videoDir, entry.Name())
			// 检查是否有对应的 .srt 文件（没有 .frame 后缀）
			normalSrtName := strings.TrimSuffix(entry.Name(), ".frame.srt") + ".srt"
			normalSrtPath := filepath.Join(videoDir, normalSrtName)

			// 如果对应的 .srt 文件存在，删除 .frame.srt 文件
			if _, err := os.Stat(normalSrtPath); err == nil {
				if err := os.Remove(frameSrtPath); err == nil {
					logger.Info().
						Str("frame_srt_path", frameSrtPath).
						Msg("已清理旧的 .frame.srt 文件")
				}
			}
		}
	}
}

// stripResolutionFromSubtitleFilenames 去除字幕文件名中的分辨率标识（例如: id_1080p.en.srt -> id.en.srt）
func (d *downloader) stripResolutionFromSubtitleFilenames(videoDir string, subtitlePaths []string) []string {
	newPaths := make([]string, 0, len(subtitlePaths))
	re := regexp.MustCompile(`_(\d{3,5})p(\.[A-Za-z-]+\.(srt|vtt))$`)
	for _, p := range subtitlePaths {
		base := filepath.Base(p)
		dir := filepath.Dir(p)
		if re.MatchString(base) {
			newBase := re.ReplaceAllString(base, `$2`)
			newPath := filepath.Join(dir, newBase)
			// 如果目标已存在，先删除
			if _, err := os.Stat(newPath); err == nil {
				_ = os.Remove(newPath)
			}
			// 重命名文件
			if err := os.Rename(p, newPath); err != nil {
				logger.Warn().
					Str("old_path", p).
					Str("new_path", newPath).
					Err(err).
					Msg("重命名字幕文件去除分辨率标识失败，保留原文件名")
				newPaths = append(newPaths, p)
			} else {
				logger.Info().
					Str("old_path", p).
					Str("new_path", newPath).
					Msg("已去除字幕文件名中的分辨率标识")
				newPaths = append(newPaths, newPath)
			}
		} else {
			newPaths = append(newPaths, p)
		}
	}
	return newPaths
}
