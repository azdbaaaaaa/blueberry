package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Parser interface {
	ExtractVideosFromChannel(ctx context.Context, channelURL string) ([]Video, error)
	CheckInstalled() error
}

type Video struct {
	// 基本信息
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	WebpageURL  string `json:"webpage_url"`
	OriginalURL string `json:"original_url"`

	// 时长和日期
	Duration       float64 `json:"duration"` // yt-dlp 返回的是浮点数
	DurationString string  `json:"duration_string"`
	UploadDate     string  `json:"upload_date"`
	ReleaseYear    *int    `json:"release_year"`

	// 描述
	Description string `json:"description"`

	// 频道信息
	ChannelID   string `json:"channel_id"`
	Channel     string `json:"channel"`
	ChannelURL  string `json:"channel_url"`
	Uploader    string `json:"uploader"`
	UploaderID  string `json:"uploader_id"`
	UploaderURL string `json:"uploader_url"`

	// 播放列表信息
	PlaylistCount      int    `json:"playlist_count"`
	Playlist           string `json:"playlist"`
	PlaylistID         string `json:"playlist_id"`
	PlaylistTitle      string `json:"playlist_title"`
	PlaylistUploader   string `json:"playlist_uploader"`
	PlaylistUploaderID string `json:"playlist_uploader_id"`
	PlaylistChannel    string `json:"playlist_channel"`
	PlaylistChannelID  string `json:"playlist_channel_id"`
	PlaylistWebpageURL string `json:"playlist_webpage_url"`
	PlaylistIndex      int    `json:"playlist_index"`
	NEntries           int    `json:"n_entries"`

	// 统计信息
	ViewCount         *int64 `json:"view_count"`
	LiveStatus        string `json:"live_status"`
	ChannelIsVerified *bool  `json:"channel_is_verified"`

	// 时间戳
	Timestamp        *int64 `json:"timestamp"`
	ReleaseTimestamp *int64 `json:"release_timestamp"`
	Epoch            *int64 `json:"epoch"`

	// 其他
	Availability string `json:"availability"`

	// 保存原始完整数据
	RawData map[string]interface{} `json:"-"`
}

type parser struct {
	cookiesFromBrowser string
	cookiesFile        string
}

func NewParser(cookiesFromBrowser, cookiesFile string) Parser {
	return &parser{
		cookiesFromBrowser: cookiesFromBrowser,
		cookiesFile:        cookiesFile,
	}
}

func (p *parser) ExtractVideosFromChannel(ctx context.Context, channelURL string) ([]Video, error) {
	args := []string{
		"--flat-playlist",
		"--dump-json",
		"--no-warnings",
		"--extractor-args", "youtube:player_client=android,ios,web",
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		"--referer", "https://www.youtube.com/",
		"--add-header", "Accept-Language:en-US,en;q=0.9",
		"--add-header", "Accept:text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
	}

	// 添加 cookies 支持（优先使用 cookies 文件，因为服务器上可能没有浏览器）
	if p.cookiesFile != "" {
		args = append(args, "--cookies", p.cookiesFile)
	} else if p.cookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", p.cookiesFromBrowser)
	}

	args = append(args, channelURL)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)

	// 使用 CombinedOutput 以便在错误时拿到 stderr，方便排查网络/登录问题
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("执行yt-dlp失败: exit status %v, 输出: %s", err, string(output))
	}

	lines := strings.Split(string(output), "\n")
	var videos []Video
	totalLines := len(lines)
	emptyLines := 0
	parseErrors := 0
	skippedByType := 0
	missingFields := 0
	validVideos := 0

	fmt.Printf("[DEBUG] ExtractVideosFromChannel: 开始解析，总行数=%d\n", totalLines)

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			emptyLines++
			continue
		}

		// 解析为 map 先检查 _type，只处理实际的视频条目
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			parseErrors++
			if i < 3 { // 只打印前3个错误，避免日志过多
				fmt.Printf("[DEBUG] 第%d行 JSON解析失败: %v\n", i+1, err)
			}
			continue
		}

		// 检查 _type

		if entryType, ok := data["_type"].(string); ok {
			if entryType != "url" {
				skippedByType++
				if i < 3 {
					fmt.Printf("[DEBUG] 第%d行 跳过类型: _type=%s\n", i+1, entryType)
				}
				continue
			}
		} else {
			// 如果没有 _type 字段，也跳过（可能是其他格式的数据）
			skippedByType++
			if i < 3 {
				fmt.Printf("[DEBUG] 第%d行 没有_type字段\n", i+1)
			}
			continue
		}

		// 先解析为 map 保存原始数据
		var rawData map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rawData); err != nil {
			parseErrors++
			if i < 3 {
				fmt.Printf("[DEBUG] 第%d行 原始数据解析失败: %v\n", i+1, err)
			}
			continue
		}

		// 解析为 Video 结构
		var video Video
		if err := json.Unmarshal([]byte(line), &video); err != nil {
			parseErrors++
			if i < 3 {
				fmt.Printf("[DEBUG] 第%d行 Video结构解析失败: %v\n", i+1, err)
			}
			continue
		}

		// 保存原始完整数据
		video.RawData = rawData

		// 检查必要字段
		if video.ID == "" || video.Title == "" {
			missingFields++
			if i < 3 {
				fmt.Printf("[DEBUG] 第%d行 缺少字段: ID=%q, Title=%q\n", i+1, video.ID, video.Title)
			}
			continue
		}

		// 如果 URL 为空，使用 ID 构建
		if video.URL == "" {
			video.URL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", video.ID)
		}

		videos = append(videos, video)
		validVideos++
		if validVideos <= 3 {
			fmt.Printf("[DEBUG] 第%d行 成功解析视频: ID=%s, Title=%s\n", i+1, video.ID, video.Title)
		}
	}

	// 输出调试信息
	fmt.Printf("[DEBUG] ExtractVideosFromChannel: 总行数=%d, 空行=%d, 解析错误=%d, 跳过类型=%d, 缺少字段=%d, 有效视频=%d\n",
		totalLines, emptyLines, parseErrors, skippedByType, missingFields, validVideos)

	return videos, nil
}

func (p *parser) CheckInstalled() error {
	_, err := exec.LookPath("yt-dlp")
	if err != nil {
		return fmt.Errorf("yt-dlp未安装，请先安装yt-dlp: https://github.com/yt-dlp/yt-dlp")
	}
	return nil
}
