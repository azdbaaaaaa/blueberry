package youtube

import (
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"blueberry/internal/config"
)

// BuildYtDlpStabilityArgs builds retry/fragment/sleep/concurrency args for yt-dlp from config.
// Centralized here to avoid scattering the same flags across different code paths.
func BuildYtDlpStabilityArgs(cfg *config.Config) []string {
	if cfg == nil {
		cfg = config.Get()
	}
	retries := 3
	fragmentRetries := 3
	concurrentFragments := 3 // 默认值 3（与 config.go 中的默认值保持一致）
	sleepInterval := 30      // 默认值 30 秒（与 config.go 中的默认值保持一致）
	sleepRequests := 3
	sleepSubtitles := 2
	bufferSize := "1M"
	fileAccessRetries := 5
	if cfg != nil {
		if cfg.YouTube.Retries > 0 {
			retries = cfg.YouTube.Retries
		}
		if cfg.YouTube.FragmentRetries > 0 {
			fragmentRetries = cfg.YouTube.FragmentRetries
		}
		if cfg.YouTube.ConcurrentFragments > 0 {
			concurrentFragments = cfg.YouTube.ConcurrentFragments
		}
		if cfg.YouTube.SleepIntervalSeconds > 0 {
			sleepInterval = cfg.YouTube.SleepIntervalSeconds
		}
		if cfg.YouTube.SleepRequestsSeconds > 0 {
			sleepRequests = cfg.YouTube.SleepRequestsSeconds
		}
		if cfg.YouTube.SleepSubtitlesSeconds > 0 {
			sleepSubtitles = cfg.YouTube.SleepSubtitlesSeconds
		}
		if cfg.YouTube.BufferSize != "" {
			bufferSize = cfg.YouTube.BufferSize
		}
		if cfg.YouTube.FileAccessRetries > 0 {
			fileAccessRetries = cfg.YouTube.FileAccessRetries
		}
	}
	// 为 sleep interval 添加 0%-50% 的随机变化
	// 使用当前时间作为随机种子，确保每次调用都有不同的随机值
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomFactor := 1.0 + rng.Float64()*0.5 // 1.0 到 1.5 之间的随机值
	sleepIntervalWithRandom := int(float64(sleepInterval) * randomFactor)

	args := []string{
		"--retries", strconv.Itoa(retries),
		"--fragment-retries", strconv.Itoa(fragmentRetries),
		"--skip-unavailable-fragments",
		"--sleep-interval", strconv.Itoa(sleepIntervalWithRandom),
		"--concurrent-fragments", strconv.Itoa(concurrentFragments),
		"--sleep-requests", strconv.Itoa(sleepRequests),
		"--sleep-subtitles", strconv.Itoa(sleepSubtitles),
		"--buffer-size", bufferSize,
		"--file-access-retries", strconv.Itoa(fileAccessRetries),
	}
	// 添加限速参数（如果配置了）
	if cfg != nil && cfg.YouTube.LimitRate != "" {
		args = append(args, "--limit-rate", cfg.YouTube.LimitRate)
	}
	return args
}

// BuildYtDlpBaseArgs builds common, non-dynamic args (output template, ipv6, thumbnails, info json, description).
func BuildYtDlpBaseArgs(videoDir string, cfg *config.Config) []string {
	if cfg == nil {
		cfg = config.Get()
	}
	args := []string{
		"-o", filepath.Join(videoDir, "%(id)s_%(height)sp.%(ext)s"),
		"--write-thumbnail",
		"--convert-thumbnails", "jpg",
		"--embed-thumbnail",
		"--write-info-json",
		"--write-description",
	}
	// 根据配置决定使用 IPv6 还是 IPv4
	if cfg != nil && cfg.YouTube.ForceIPv6 {
		args = append(args, "--force-ipv6")
	} else {
		args = append(args, "--force-ipv4")
	}
	return args
}

// BuildYtDlpCookiesArgs builds cookies-related args depending on availability and inclusion flag.
func BuildYtDlpCookiesArgs(includeCookies bool, cookiesFile, cookiesFromBrowser string) []string {
	if !includeCookies {
		return nil
	}
	if cookiesFile != "" {
		return []string{"--cookies", cookiesFile}
	}
	if cookiesFromBrowser != "" {
		return []string{"--cookies-from-browser", cookiesFromBrowser}
	}
	return nil
}

// BuildYtDlpSubtitleArgs builds subtitle args including conversion if ffmpeg is available.
func BuildYtDlpSubtitleArgs(languages []string) []string {
	args := []string{"--write-sub", "--write-auto-sub"}
	if len(languages) > 0 {
		args = append(args, "--sub-langs", strings.Join(languages, ","))
	} else {
		args = append(args, "--sub-langs", "all")
	}
	// Convert to SRT if ffmpeg exists
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		args = append(args, "--convert-subs", "srt")
	}
	return args
}

// BuildYtDlpFormatArgsBest1080 builds best mode capped at 1080p with mkv merge.
func BuildYtDlpFormatArgsBest1080() []string {
	return []string{"-f", "bv*[height<=1080]+ba/b[height<=1080]", "--merge-output-format", "mp4"}
}

// BuildYtDlpFormatArgsMinHeight builds strict min-height selector with mp4 merge.
func BuildYtDlpFormatArgsMinHeight(minHeight int) []string {
	if minHeight <= 0 {
		minHeight = 1080
	}
	return []string{"-f", fmt.Sprintf("bv*[height>=%d]+ba/b[height>=%d]", minHeight, minHeight), "--merge-output-format", "mp4"}
}
