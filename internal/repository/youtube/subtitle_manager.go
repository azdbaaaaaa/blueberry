package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type SubtitleManager interface {
	ListSubtitles(ctx context.Context, videoURL string, languages []string) (*VideoSubtitleInfo, error)
	RenameSubtitlesForAID(subtitlePaths []string, aid string, outputDir string) ([]string, error)
}

type SubtitleInfo struct {
	Language string
	URL      string
	Ext      string
}

type VideoSubtitleInfo struct {
	VideoURL     string
	SubtitleURLs []SubtitleInfo
}

type subtitleManager struct {
	cookiesFromBrowser string
	cookiesFile        string
}

func NewSubtitleManager(cookiesFromBrowser, cookiesFile string) SubtitleManager {
	return &subtitleManager{
		cookiesFromBrowser: cookiesFromBrowser,
		cookiesFile:        cookiesFile,
	}
}

func (s *subtitleManager) ListSubtitles(ctx context.Context, videoURL string, languages []string) (*VideoSubtitleInfo, error) {
	args := []string{
		"--dump-json",
		"--no-warnings",
		"--skip-download",
		"--extractor-args", "youtube:player_client=android,ios,web",
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		"--referer", "https://www.youtube.com/",
		"--add-header", "Accept-Language:en-US,en;q=0.9",
	}

	// 添加 cookies 支持（优先使用 cookies 文件，因为服务器上可能没有浏览器）
	if s.cookiesFile != "" {
		args = append(args, "--cookies", s.cookiesFile)
	} else if s.cookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", s.cookiesFromBrowser)
	}

	args = append(args, videoURL)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("获取字幕信息失败: %w, 输出: %s", err, string(output))
	}

	var videoInfo map[string]interface{}
	if err := json.Unmarshal(output, &videoInfo); err != nil {
		return nil, fmt.Errorf("解析视频信息失败: %w", err)
	}

	result := &VideoSubtitleInfo{
		VideoURL:     videoURL,
		SubtitleURLs: make([]SubtitleInfo, 0),
	}

	subtitles, hasSubtitles := videoInfo["subtitles"].(map[string]interface{})
	autoSubtitles, hasAutoSubtitles := videoInfo["automatic_captions"].(map[string]interface{})

	if !hasSubtitles && !hasAutoSubtitles {
		return result, nil
	}

	processSubtitles := func(subs map[string]interface{}) {
		if len(languages) > 0 {
			for _, lang := range languages {
				if langSubs, exists := subs[lang].([]interface{}); exists {
					for _, sub := range langSubs {
						if subMap, ok := sub.(map[string]interface{}); ok {
							subInfo := SubtitleInfo{
								Language: lang,
							}
							if url, ok := subMap["url"].(string); ok {
								subInfo.URL = url
							}
							if ext, ok := subMap["ext"].(string); ok {
								subInfo.Ext = ext
							}
							if subInfo.URL != "" {
								result.SubtitleURLs = append(result.SubtitleURLs, subInfo)
							}
						}
					}
				}
			}
		} else {
			for lang, langSubs := range subs {
				if subs, ok := langSubs.([]interface{}); ok {
					for _, sub := range subs {
						if subMap, ok := sub.(map[string]interface{}); ok {
							subInfo := SubtitleInfo{
								Language: lang,
							}
							if url, ok := subMap["url"].(string); ok {
								subInfo.URL = url
							}
							if ext, ok := subMap["ext"].(string); ok {
								subInfo.Ext = ext
							}
							if subInfo.URL != "" {
								result.SubtitleURLs = append(result.SubtitleURLs, subInfo)
							}
						}
					}
				}
			}
		}
	}

	if hasSubtitles {
		processSubtitles(subtitles)
	}
	if hasAutoSubtitles {
		processSubtitles(autoSubtitles)
	}

	return result, nil
}

func (s *subtitleManager) RenameSubtitlesForAID(subtitlePaths []string, aid string, outputDir string) ([]string, error) {
	if aid == "" {
		return subtitlePaths, fmt.Errorf("aid不能为空")
	}

	renamedPaths := make([]string, 0, len(subtitlePaths))

	for _, subtitlePath := range subtitlePaths {
		lang := s.extractLanguageFromSubtitleFile(subtitlePath)
		if lang == "" {
			lang = "unknown"
		}

		ext := filepath.Ext(subtitlePath)
		newName := fmt.Sprintf("%s_%s%s", aid, lang, ext)
		newPath := filepath.Join(outputDir, newName)

		if err := s.copyFile(subtitlePath, newPath); err != nil {
			return nil, fmt.Errorf("复制字幕文件失败: %w", err)
		}

		renamedPaths = append(renamedPaths, newPath)
	}

	return renamedPaths, nil
}

func (s *subtitleManager) extractLanguageFromSubtitleFile(filePath string) string {
	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	patterns := []string{
		`\.([a-z]{2,3})$`,
		`\.([a-z]{2,3})-[a-z]+$`,
		`-([a-z]{2,3})$`,
		`-([a-z]{2,3})-[a-z]+$`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(name)
		if len(matches) > 1 {
			lang := matches[1]
			if lang == "zh" {
				return "zh"
			}
			return lang
		}
	}

	if strings.Contains(strings.ToLower(name), "chinese") || strings.Contains(strings.ToLower(name), "zh") {
		return "zh"
	}

	return ""
}

func (s *subtitleManager) copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	if err := os.WriteFile(dst, input, 0644); err != nil {
		return err
	}

	return nil
}
