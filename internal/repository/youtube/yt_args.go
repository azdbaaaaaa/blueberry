package youtube

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
	concurrentFragments := 4
	sleepInterval := 60
	sleepRequests := 3
	sleepSubtitles := 2
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
	}
	args := []string{
		"--retries", strconv.Itoa(retries),
		"--fragment-retries", strconv.Itoa(fragmentRetries),
		"--skip-unavailable-fragments",
		"--sleep-interval", strconv.Itoa(sleepInterval),
		"--concurrent-fragments", strconv.Itoa(concurrentFragments),
		"--sleep-requests", strconv.Itoa(sleepRequests),
		"--sleep-subtitles", strconv.Itoa(sleepSubtitles),
	}
	return args
}

// BuildYtDlpBaseArgs builds common, non-dynamic args (output template, ipv4, thumbnails, info json, description).
func BuildYtDlpBaseArgs(videoDir string) []string {
	return []string{
		"-o", filepath.Join(videoDir, "%(id)s_%(height)sp.%(ext)s"),
		"--force-ipv4",
		"--write-thumbnail",
		"--convert-thumbnails", "jpg",
		"--embed-thumbnail",
		"--write-info-json",
		"--write-description",
	}
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
	return []string{"-f", "bv*[height<=1080]+ba/b[height<=1080]", "--merge-output-format", "mkv"}
}

// BuildYtDlpFormatArgsMinHeight builds strict min-height selector with mp4 merge.
func BuildYtDlpFormatArgsMinHeight(minHeight int) []string {
	if minHeight <= 0 {
		minHeight = 1080
	}
	return []string{"-f", fmt.Sprintf("bv*[height>=%d]+ba/b[height>=%d]", minHeight, minHeight), "--merge-output-format", "mkv"}
}
