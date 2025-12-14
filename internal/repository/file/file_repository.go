package file

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type uploadCounters struct {
	Date   string         `json:"date"`
	Counts map[string]int `json:"counts"`
}

func shortenErrorMessage(msg string) string {
	s := strings.TrimSpace(msg)
	if s == "" {
		return s
	}
	// 仅保留首行，避免长堆栈
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	// 限长，按 rune 截断，避免乱码
	const limit = 300
	runes := []rune(s)
	if len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return s
}

type Repository interface {
	FindVideoFile(dir string) (string, error)
	FindSubtitleFiles(dir string) ([]string, error)
	ExtractVideoTitleFromFile(videoPath string) string
	ExtractVideoID(url string) string
	ExtractChannelID(channelURL string) string
	EnsureChannelDir(channelID string) (string, error)
	EnsureVideoDir(channelID, videoID string) (string, error)
	VideoInfoExists(channelID, videoID string) bool
	VideoFileExists(channelID, videoID string) bool
	SubtitleFilesExist(channelID, videoID string, languages []string) bool
	FindVideoDirByID(channelID, videoID string) (string, error)
	LoadVideoInfo(videoDir string) (*VideoInfo, error)
	SaveVideoInfo(videoDir string, videoInfo *VideoInfo) error
	IsVideoDownloaded(videoDir string) bool
	IsSubtitlesDownloaded(videoDir string, languages []string) bool
	IsThumbnailDownloaded(videoDir string) bool
	MarkVideoDownloaded(videoDir string) error
	MarkVideoDownloadedWithPath(videoDir, videoPath string) error

	MarkVideoDownloading(videoDir string, videoURL string) error
	MarkVideoFailed(videoDir string, errorMsg string) error
	InitializeDownloadStatus(videoDir string, videoURL string, subtitleURLs map[string]string, subtitleLanguages []string, thumbnailURL string) error
	MarkSubtitlesDownloaded(videoDir string, languages []string) error
	MarkSubtitlesDownloadedWithPaths(videoDir string, languages []string, subtitlePaths map[string]string, subtitleURLs map[string]string) error
	MarkSubtitleFailed(videoDir string, lang string, errorMsg string) error
	MarkThumbnailDownloaded(videoDir string) error
	MarkThumbnailDownloadedWithPath(videoDir, thumbnailPath string, thumbnailURL string) error
	ChannelInfoExists(channelID string) bool
	SaveChannelInfo(channelID string, videos []map[string]interface{}) error
	LoadChannelInfo(channelID string) ([]map[string]interface{}, error)
	SanitizeTitle(title string) string
	EnsureVideoDirByTitle(channelID, title string) (string, error)
	SavePendingDownloads(channelID string, pending *PendingDownloads) error
	LoadPendingDownloads(channelID string) (*PendingDownloads, error)
	UpdatePendingDownloadStatus(channelID, videoID string, resourceType string, status string, filePath string) error
	// 上传状态管理
	IsVideoUploaded(videoDir string) bool
	MarkVideoUploading(videoDir string) error
	MarkVideoUploaded(videoDir string, bilibiliAID string, bilibiliAccount string) error
	MarkVideoUploadFailed(videoDir string, errorMsg string) error
	FindCoverFile(videoDir string) (string, error)
	// 从 download_status.json 中提取字幕语言列表
	GetSubtitleLanguagesFromStatus(videoDir string) ([]string, error)
	// 账号上传计数
	GetTodayUploadCount(account string) (int, error)
	IncrementTodayUploadCount(account string) error
	LoadTodayUploadCounts() (map[string]int, error)
}

// VideoInfo 视频信息结构，用于保存到JSON文件
// 包含从 yt-dlp 获取的完整视频信息
type VideoInfo struct {
	// 基本信息
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	WebpageURL  string `json:"webpage_url,omitempty"`
	OriginalURL string `json:"original_url,omitempty"`

	// 时长和日期
	Duration       float64 `json:"duration,omitempty"`
	DurationString string  `json:"duration_string,omitempty"`
	UploadDate     string  `json:"upload_date,omitempty"`
	ReleaseYear    *int    `json:"release_year,omitempty"`

	// 描述
	Description string `json:"description,omitempty"`

	// 频道信息
	ChannelID   string `json:"channel_id,omitempty"`
	Channel     string `json:"channel,omitempty"`
	ChannelURL  string `json:"channel_url,omitempty"`
	Uploader    string `json:"uploader,omitempty"`
	UploaderID  string `json:"uploader_id,omitempty"`
	UploaderURL string `json:"uploader_url,omitempty"`

	// 播放列表信息
	PlaylistCount      int    `json:"playlist_count,omitempty"`
	Playlist           string `json:"playlist,omitempty"`
	PlaylistID         string `json:"playlist_id,omitempty"`
	PlaylistTitle      string `json:"playlist_title,omitempty"`
	PlaylistUploader   string `json:"playlist_uploader,omitempty"`
	PlaylistUploaderID string `json:"playlist_uploader_id,omitempty"`
	PlaylistChannel    string `json:"playlist_channel,omitempty"`
	PlaylistChannelID  string `json:"playlist_channel_id,omitempty"`
	PlaylistWebpageURL string `json:"playlist_webpage_url,omitempty"`
	PlaylistIndex      int    `json:"playlist_index,omitempty"`
	NEntries           int    `json:"n_entries,omitempty"`

	// 统计信息
	ViewCount         *int64 `json:"view_count,omitempty"`
	LiveStatus        string `json:"live_status,omitempty"`
	ChannelIsVerified *bool  `json:"channel_is_verified,omitempty"`

	// 缩略图
	Thumbnails []Thumbnail `json:"thumbnails,omitempty"`

	// 字幕信息（语言 -> 字幕URL的映射）
	Subtitles map[string]string `json:"subtitles"`

	// 提取器信息
	Extractor    string `json:"extractor,omitempty"`
	ExtractorKey string `json:"extractor_key,omitempty"`

	// 时间戳
	Timestamp        *int64 `json:"timestamp,omitempty"`
	ReleaseTimestamp *int64 `json:"release_timestamp,omitempty"`
	Epoch            *int64 `json:"epoch,omitempty"`

	// 其他
	Availability string                 `json:"availability,omitempty"`
	RawData      map[string]interface{} `json:"raw_data,omitempty"` // 保存原始完整数据作为备用
}

// Thumbnail 缩略图信息
type Thumbnail struct {
	URL    string `json:"url"`
	Height int    `json:"height,omitempty"`
	Width  int    `json:"width,omitempty"`
}

// PendingDownloads 待下载资源状态
type PendingDownloads struct {
	ChannelID   string                 `json:"channel_id"`
	ChannelURL  string                 `json:"channel_url"`
	GeneratedAt int64                  `json:"generated_at"`
	Videos      []PendingVideoDownload `json:"videos"`
}

// PendingVideoDownload 单个视频的待下载资源
type PendingVideoDownload struct {
	VideoID   string                           `json:"video_id"`
	Title     string                           `json:"title"`
	VideoURL  string                           `json:"video_url"`
	Video     PendingResourceStatus            `json:"video"`
	Subtitles map[string]PendingResourceStatus `json:"subtitles"` // 语言代码 -> 状态
	Thumbnail PendingResourceStatus            `json:"thumbnail"`
}

// PendingResourceStatus 资源下载状态
type PendingResourceStatus struct {
	Status       string `json:"status"`        // pending, downloading, completed, failed, skipped
	URL          string `json:"url,omitempty"` // 资源URL（如果有）
	FilePath     string `json:"file_path,omitempty"`
	DownloadedAt int64  `json:"downloaded_at,omitempty"`
	Error        string `json:"error,omitempty"`
}

type repository struct {
	outputDir string
}

func NewRepository(outputDir string) Repository {
	return &repository{
		outputDir: outputDir,
	}
}

func (r *repository) FindVideoFile(dir string) (string, error) {
	extensions := []string{".mp4", ".mkv", ".webm", ".flv"}

	for _, ext := range extensions {
		matches, err := filepath.Glob(filepath.Join(dir, "*"+ext))
		if err != nil {
			continue
		}
		if len(matches) > 0 {
			return matches[0], nil
		}
	}

	return "", fmt.Errorf("未找到视频文件")
}

func (r *repository) FindSubtitleFiles(dir string) ([]string, error) {
	// 优先查找 SRT 格式（转换后的格式），如果没有则查找 VTT 格式
	extensions := []string{".srt", ".vtt", ".ass"}
	var files []string

	for _, ext := range extensions {
		matches, err := filepath.Glob(filepath.Join(dir, "*"+ext))
		if err != nil {
			continue
		}
		files = append(files, matches...)
	}

	return files, nil
}

func (r *repository) ExtractVideoTitleFromFile(videoPath string) string {
	base := filepath.Base(videoPath)
	ext := filepath.Ext(base)
	title := strings.TrimSuffix(base, ext)
	return title
}

func (r *repository) ExtractVideoID(url string) string {
	if strings.Contains(url, "watch?v=") {
		parts := strings.Split(url, "watch?v=")
		if len(parts) > 1 {
			id := strings.Split(parts[1], "&")[0]
			return strings.Split(id, "#")[0]
		}
	}
	if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		if len(parts) > 1 {
			return strings.Split(parts[1], "?")[0]
		}
	}
	return ""
}

func (r *repository) ExtractChannelID(channelURL string) string {
	// 从频道URL中提取频道ID或标识符
	// 例如: https://www.youtube.com/@channelname/videos -> channelname
	// 或者: https://www.youtube.com/channel/UCxxxxx -> UCxxxxx

	if strings.Contains(channelURL, "/@") {
		// 处理 @channelname 格式
		parts := strings.Split(channelURL, "/@")
		if len(parts) > 1 {
			channelPart := strings.Split(parts[1], "/")[0]
			// URL解码处理
			return strings.TrimSpace(channelPart)
		}
	}

	if strings.Contains(channelURL, "/channel/") {
		// 处理 /channel/UCxxxxx 格式
		parts := strings.Split(channelURL, "/channel/")
		if len(parts) > 1 {
			return strings.Split(parts[1], "/")[0]
		}
	}

	// 如果无法提取，使用URL的hash作为标识
	return strings.ReplaceAll(strings.ReplaceAll(channelURL, "/", "_"), ":", "_")
}

func (r *repository) EnsureChannelDir(channelID string) (string, error) {
	channelDir := filepath.Join(r.outputDir, channelID)
	if err := os.MkdirAll(channelDir, 0755); err != nil {
		return "", fmt.Errorf("创建频道目录失败: %w", err)
	}
	return channelDir, nil
}

func (r *repository) EnsureVideoDir(channelID, videoID string) (string, error) {
	videoDir := filepath.Join(r.outputDir, channelID, videoID)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return "", fmt.Errorf("创建视频目录失败: %w", err)
	}
	return videoDir, nil
}

func (r *repository) VideoInfoExists(channelID, videoID string) bool {
	videoDir := filepath.Join(r.outputDir, channelID, videoID)
	infoPath := filepath.Join(videoDir, "video_info.json")
	_, err := os.Stat(infoPath)
	return err == nil
}

func (r *repository) VideoFileExists(channelID, videoID string) bool {
	// 使用 FindVideoDirByID 查找正确的目录（可能是基于 title 的）
	videoDir, _ := r.FindVideoDirByID(channelID, videoID)
	return r.IsVideoDownloaded(videoDir)
}

func (r *repository) SubtitleFilesExist(channelID, videoID string, languages []string) bool {
	// 使用 FindVideoDirByID 查找正确的目录（可能是基于 title 的）
	videoDir, _ := r.FindVideoDirByID(channelID, videoID)
	subtitleFiles, err := r.FindSubtitleFiles(videoDir)
	if err != nil || len(subtitleFiles) == 0 {
		return false
	}

	// 如果 languages 为空，只要有字幕文件就认为存在
	if len(languages) == 0 {
		return len(subtitleFiles) > 0
	}

	// 检查是否有所需语言的字幕文件
	// 提取已存在的字幕文件的语言代码
	existingLangs := make(map[string]bool)
	for _, subFile := range subtitleFiles {
		base := filepath.Base(subFile)
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)

		// 尝试从文件名中提取语言代码（例如：title.en.vtt 或 title.zh-Hans.vtt）
		for _, lang := range languages {
			if strings.Contains(name, "."+lang) || strings.Contains(name, "-"+lang) {
				existingLangs[lang] = true
			}
		}
	}

	// 检查是否所有需要的语言都存在
	for _, lang := range languages {
		if !existingLangs[lang] {
			return false
		}
	}

	return true
}

func (r *repository) LoadVideoInfo(videoDir string) (*VideoInfo, error) {
	infoPath := filepath.Join(videoDir, "video_info.json")

	data, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, fmt.Errorf("读取视频信息失败: %w", err)
	}

	var videoInfo VideoInfo
	if err := json.Unmarshal(data, &videoInfo); err != nil {
		return nil, fmt.Errorf("解析视频信息失败: %w", err)
	}

	return &videoInfo, nil
}

func (r *repository) SaveVideoInfo(videoDir string, videoInfo *VideoInfo) error {
	infoPath := filepath.Join(videoDir, "video_info.json")

	data, err := json.MarshalIndent(videoInfo, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化视频信息失败: %w", err)
	}

	if err := os.WriteFile(infoPath, data, 0644); err != nil {
		return fmt.Errorf("保存视频信息失败: %w", err)
	}

	return nil
}

func (r *repository) ChannelInfoExists(channelID string) bool {
	channelDir := filepath.Join(r.outputDir, channelID)
	infoPath := filepath.Join(channelDir, "channel_info.json")
	_, err := os.Stat(infoPath)
	return err == nil
}

func (r *repository) SaveChannelInfo(channelID string, videos []map[string]interface{}) error {
	channelDir := filepath.Join(r.outputDir, channelID)
	if err := os.MkdirAll(channelDir, 0755); err != nil {
		return fmt.Errorf("创建频道目录失败: %w", err)
	}

	infoPath := filepath.Join(channelDir, "channel_info.json")
	data, err := json.MarshalIndent(videos, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化频道信息失败: %w", err)
	}

	if err := os.WriteFile(infoPath, data, 0644); err != nil {
		return fmt.Errorf("保存频道信息失败: %w", err)
	}

	return nil
}

func (r *repository) LoadChannelInfo(channelID string) ([]map[string]interface{}, error) {
	channelDir := filepath.Join(r.outputDir, channelID)
	infoPath := filepath.Join(channelDir, "channel_info.json")

	data, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, fmt.Errorf("读取频道信息失败: %w", err)
	}

	var videos []map[string]interface{}
	if err := json.Unmarshal(data, &videos); err != nil {
		return nil, fmt.Errorf("解析频道信息失败: %w", err)
	}

	return videos, nil
}

// FindVideoDirByID 根据 videoID 查找视频目录
// 首先尝试从 channel_info.json 中查找对应的 title，然后使用 title 查找目录
// 如果找不到，返回基于 videoID 的目录路径（兼容旧数据）
func (r *repository) FindVideoDirByID(channelID, videoID string) (string, error) {
	// 首先尝试从 channel_info.json 中查找对应的 title
	channelDir := filepath.Join(r.outputDir, channelID)
	channelInfoPath := filepath.Join(channelDir, "channel_info.json")

	if data, err := os.ReadFile(channelInfoPath); err == nil {
		var videos []map[string]interface{}
		if err := json.Unmarshal(data, &videos); err == nil {
			// 查找匹配的 videoID
			for _, video := range videos {
				if vid, ok := video["id"].(string); ok && vid == videoID {
					if title, ok := video["title"].(string); ok && title != "" {
						// 使用 title 查找目录
						sanitizedTitle := r.SanitizeTitle(title)
						videoDir := filepath.Join(r.outputDir, channelID, sanitizedTitle)
						if _, err := os.Stat(videoDir); err == nil {
							return videoDir, nil
						}
					}
					break
				}
			}
		}
	}

	// 如果找不到，尝试遍历所有目录，查找包含 video_info.json 且 ID 匹配的目录
	entries, err := os.ReadDir(channelDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			videoDir := filepath.Join(channelDir, entry.Name())
			infoPath := filepath.Join(videoDir, "video_info.json")
			if data, err := os.ReadFile(infoPath); err == nil {
				var videoInfo VideoInfo
				if err := json.Unmarshal(data, &videoInfo); err == nil {
					if videoInfo.ID == videoID {
						return videoDir, nil
					}
				}
			}
		}
	}

	// 如果都找不到，返回基于 videoID 的目录路径（兼容旧数据）
	videoDir := filepath.Join(r.outputDir, channelID, videoID)
	return videoDir, nil
}

// SanitizeTitle 清理标题，使其可以作为目录名
// 移除或替换不允许在文件名中使用的字符
func (r *repository) SanitizeTitle(title string) string {
	// 替换不允许的字符
	invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	sanitized := title
	for _, char := range invalidChars {
		sanitized = strings.ReplaceAll(sanitized, char, "_")
	}

	// 移除首尾空格和点
	sanitized = strings.TrimSpace(sanitized)
	sanitized = strings.Trim(sanitized, ".")

	// 移除控制字符和换行符
	sanitized = strings.ReplaceAll(sanitized, "\n", "_")
	sanitized = strings.ReplaceAll(sanitized, "\r", "_")
	sanitized = strings.ReplaceAll(sanitized, "\t", "_")

	// 限制长度（避免路径过长）
	maxLen := 200
	if len(sanitized) > maxLen {
		sanitized = sanitized[:maxLen]
	}

	// 如果清理后为空，返回默认值
	if sanitized == "" {
		sanitized = "untitled"
	}

	return sanitized
}

// IsVideoDownloaded 检查视频是否已下载完成
func (r *repository) IsVideoDownloaded(videoDir string) bool {
	// 检查下载状态文件
	statusFile := filepath.Join(videoDir, "download_status.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		// 文件不存在，说明未下载
		return false
	}

	var status map[string]interface{}
	if err := json.Unmarshal(data, &status); err != nil {
		return false
	}

	// 检查视频下载状态
	if video, ok := status["video"].(map[string]interface{}); ok {
		if downloaded, ok := video["downloaded"].(bool); ok {
			return downloaded
		}
	}
	// 兼容旧格式（直接是 bool）
	if video, ok := status["video"].(bool); ok {
		return video
	}
	return false
}

// IsSubtitlesDownloaded 检查字幕是否已下载完成
func (r *repository) IsSubtitlesDownloaded(videoDir string, languages []string) bool {
	// 检查下载状态文件
	statusFile := filepath.Join(videoDir, "download_status.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		return false
	}

	var status map[string]interface{}
	if err := json.Unmarshal(data, &status); err != nil {
		return false
	}

	// 如果 languages 为空，检查是否有任何字幕
	if len(languages) == 0 {
		if subtitles, ok := status["subtitles"].(map[string]interface{}); ok {
			for _, subData := range subtitles {
				// 新格式：map[string]interface{}
				if subMap, ok := subData.(map[string]interface{}); ok {
					// 检查 status 字段，如果状态是 "failed" 或 "pending"，不算已下载
					if statusStr, ok := subMap["status"].(string); ok {
						if statusStr == "failed" || statusStr == "pending" {
							continue
						}
					}
					if downloaded, ok := subMap["downloaded"].(bool); ok && downloaded {
						return true
					}
				}
				// 兼容旧格式：直接是 bool
				if downloadedBool, ok := subData.(bool); ok && downloadedBool {
					return true
				}
			}
		}
		return false
	}

	// 检查所有需要的语言是否都已下载
	if subtitles, ok := status["subtitles"].(map[string]interface{}); ok {
		for _, lang := range languages {
			subData, exists := subtitles[lang]
			if !exists {
				return false
			}
			// 新格式：map[string]interface{}
			if subMap, ok := subData.(map[string]interface{}); ok {
				// 检查 status 字段，如果状态是 "failed" 或 "pending"，需要重新下载
				if statusStr, ok := subMap["status"].(string); ok {
					if statusStr == "failed" || statusStr == "pending" {
						return false
					}
				}
				// 检查 downloaded 字段
				if downloaded, ok := subMap["downloaded"].(bool); !ok || !downloaded {
					return false
				}
			} else if downloadedBool, ok := subData.(bool); ok {
				// 兼容旧格式：直接是 bool
				if !downloadedBool {
					return false
				}
			} else {
				return false
			}
		}
		return true
	}

	return false
}

// IsThumbnailDownloaded 检查缩略图是否已下载完成
func (r *repository) IsThumbnailDownloaded(videoDir string) bool {
	// 检查下载状态文件
	statusFile := filepath.Join(videoDir, "download_status.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		return false
	}

	var status map[string]interface{}
	if err := json.Unmarshal(data, &status); err != nil {
		return false
	}

	// 检查缩略图下载状态
	if thumbnail, ok := status["thumbnail"].(map[string]interface{}); ok {
		if downloaded, ok := thumbnail["downloaded"].(bool); ok {
			return downloaded
		}
	}
	// 兼容旧格式（直接是 bool）
	if thumbnail, ok := status["thumbnail"].(bool); ok {
		return thumbnail
	}
	return false
}

// MarkVideoDownloaded 标记视频已下载完成
func (r *repository) MarkVideoDownloaded(videoDir string) error {
	return r.MarkVideoDownloadedWithPath(videoDir, "")
}

// MarkVideoDownloadedWithPath 标记视频已下载完成，并记录文件路径
func (r *repository) MarkVideoDownloadedWithPath(videoDir, videoPath string) error {
	return r.updateDownloadStatus(videoDir, func(status map[string]interface{}) {
		if status["video"] == nil {
			status["video"] = make(map[string]interface{})
		}
		video, ok := status["video"].(map[string]interface{})
		if !ok {
			// 兼容旧格式，如果是 bool，转换为 map
			video = make(map[string]interface{})
			status["video"] = video
		}
		video["status"] = "completed"
		video["resource_type"] = "video"
		video["downloaded"] = true
		video["downloaded_at"] = time.Now().Unix()
		if videoPath != "" {
			video["file_path"] = videoPath
		}
	})
}

// MarkVideoDownloading 标记视频开始下载（重置失败状态，允许重新下载）
func (r *repository) MarkVideoDownloading(videoDir string, videoURL string) error {
	return r.updateDownloadStatus(videoDir, func(status map[string]interface{}) {
		if status["video"] == nil {
			status["video"] = make(map[string]interface{})
		}
		video, ok := status["video"].(map[string]interface{})
		if !ok {
			video = make(map[string]interface{})
			status["video"] = video
		}
		// 重置状态，允许重新下载（即使之前失败过）
		video["status"] = "downloading"
		video["downloaded"] = false
		video["resource_type"] = "video"
		// 清除之前的错误信息
		delete(video, "error")
		delete(video, "failed_at")
		if videoURL != "" {
			video["url"] = videoURL
		}
	})
}

// InitializeDownloadStatus 初始化下载状态文件，包含所有资源的 URL（从 video_info.json 或 rawData 中读取）
// subtitleLanguages: 需要下载的字幕语言列表（即使没有URL，也会保存语言列表）
func (r *repository) InitializeDownloadStatus(videoDir string, videoURL string, subtitleURLs map[string]string, subtitleLanguages []string, thumbnailURL string) error {
	return r.updateDownloadStatus(videoDir, func(status map[string]interface{}) {
		// 初始化视频状态
		if status["video"] == nil {
			status["video"] = make(map[string]interface{})
		}
		video, ok := status["video"].(map[string]interface{})
		if !ok {
			video = make(map[string]interface{})
			status["video"] = video
		}
		// 如果还没有状态，设置为 pending
		// 注意：如果之前失败过（status == "failed"），不覆盖，保持失败状态以便重新下载
		if _, hasStatus := video["status"]; !hasStatus {
			video["status"] = "pending"
			video["downloaded"] = false
			video["resource_type"] = "video"
		} else if status, ok := video["status"].(string); ok && status == "failed" {
			// 如果之前失败过，确保 downloaded 为 false，允许重新下载
			video["downloaded"] = false
		}
		if videoURL != "" {
			video["url"] = videoURL
		}

		// 初始化字幕状态
		if status["subtitles"] == nil {
			status["subtitles"] = make(map[string]interface{})
		}
		subtitles, ok := status["subtitles"].(map[string]interface{})
		if !ok {
			subtitles = make(map[string]interface{})
			status["subtitles"] = subtitles
		}

		// 首先，为所有有URL的字幕设置状态
		for lang, url := range subtitleURLs {
			if subtitles[lang] == nil {
				subtitles[lang] = make(map[string]interface{})
			}
			sub, ok := subtitles[lang].(map[string]interface{})
			if !ok {
				sub = make(map[string]interface{})
				subtitles[lang] = sub
			}
			// 如果还没有状态，设置为 pending
			if _, hasStatus := sub["status"]; !hasStatus {
				sub["status"] = "pending"
				sub["downloaded"] = false
				sub["resource_type"] = "subtitle"
			}
			if url != "" {
				sub["url"] = url
			}
		}

		// 然后，为所有配置的语言（即使没有URL）也设置状态
		// 这样后续下载时可以根据语言列表来下载
		for _, lang := range subtitleLanguages {
			// 如果已经有URL了，跳过（上面已经处理了）
			if _, hasURL := subtitleURLs[lang]; hasURL {
				continue
			}
			// 如果没有URL，也创建一个条目，标记为 pending，但没有 url
			if subtitles[lang] == nil {
				subtitles[lang] = make(map[string]interface{})
			}
			sub, ok := subtitles[lang].(map[string]interface{})
			if !ok {
				sub = make(map[string]interface{})
				subtitles[lang] = sub
			}
			// 如果还没有状态，设置为 pending
			if _, hasStatus := sub["status"]; !hasStatus {
				sub["status"] = "pending"
				sub["downloaded"] = false
				sub["resource_type"] = "subtitle"
				// 不设置 url，表示需要后续下载时获取
			}
		}

		// 初始化缩略图状态
		if thumbnailURL != "" {
			if status["thumbnail"] == nil {
				status["thumbnail"] = make(map[string]interface{})
			}
			thumbnail, ok := status["thumbnail"].(map[string]interface{})
			if !ok {
				thumbnail = make(map[string]interface{})
				status["thumbnail"] = thumbnail
			}
			// 如果还没有状态，设置为 pending
			if _, hasStatus := thumbnail["status"]; !hasStatus {
				thumbnail["status"] = "pending"
				thumbnail["downloaded"] = false
				thumbnail["resource_type"] = "thumbnail"
			}
			thumbnail["url"] = thumbnailURL
		}
	})
}

// MarkVideoFailed 标记视频下载失败
func (r *repository) MarkVideoFailed(videoDir string, errorMsg string) error {
	return r.updateDownloadStatus(videoDir, func(status map[string]interface{}) {
		if status["video"] == nil {
			status["video"] = make(map[string]interface{})
		}
		video, ok := status["video"].(map[string]interface{})
		if !ok {
			video = make(map[string]interface{})
			status["video"] = video
		}
		video["status"] = "failed"
		video["downloaded"] = false
		video["resource_type"] = "video"
		if errorMsg != "" {
			video["error"] = shortenErrorMessage(errorMsg)
		}
		video["failed_at"] = time.Now().Unix()
	})
}

// MarkSubtitlesDownloaded 标记字幕已下载完成
// languages: 已下载的字幕语言列表
// subtitlePaths: 字幕文件路径映射（可选，key 为语言代码，value 为文件路径）
func (r *repository) MarkSubtitlesDownloaded(videoDir string, languages []string) error {
	return r.MarkSubtitlesDownloadedWithPaths(videoDir, languages, nil, nil)
}

// MarkSubtitlesDownloadedWithPaths 标记字幕已下载完成，并记录文件路径和URL
func (r *repository) MarkSubtitlesDownloadedWithPaths(videoDir string, languages []string, subtitlePaths map[string]string, subtitleURLs map[string]string) error {
	return r.updateDownloadStatus(videoDir, func(status map[string]interface{}) {
		if status["subtitles"] == nil {
			status["subtitles"] = make(map[string]interface{})
		}
		subtitles, ok := status["subtitles"].(map[string]interface{})
		if !ok {
			subtitles = make(map[string]interface{})
			status["subtitles"] = subtitles
		}
		for _, lang := range languages {
			subData := subtitles[lang]
			var sub map[string]interface{}
			if subMap, ok := subData.(map[string]interface{}); ok {
				sub = subMap
			} else {
				// 兼容旧格式，如果是 bool，转换为 map
				sub = make(map[string]interface{})
				subtitles[lang] = sub
			}
			sub["status"] = "completed"
			sub["resource_type"] = "subtitle"
			sub["downloaded"] = true
			sub["downloaded_at"] = time.Now().Unix()
			// 清除之前失败的痕迹
			delete(sub, "error")
			delete(sub, "failed_at")
			if subtitlePaths != nil {
				if path, ok := subtitlePaths[lang]; ok {
					sub["file_path"] = path
				}
			}
			if subtitleURLs != nil {
				if url, ok := subtitleURLs[lang]; ok {
					sub["url"] = url
				}
			}
		}
	})
}

// MarkThumbnailDownloaded 标记缩略图已下载完成
// thumbnailPath: 缩略图文件路径（可选，用于记录文件位置）
func (r *repository) MarkThumbnailDownloaded(videoDir string) error {
	return r.MarkThumbnailDownloadedWithPath(videoDir, "", "")
}

// MarkThumbnailDownloadedWithPath 标记缩略图已下载完成，并记录文件路径和URL
func (r *repository) MarkThumbnailDownloadedWithPath(videoDir, thumbnailPath string, thumbnailURL string) error {
	return r.updateDownloadStatus(videoDir, func(status map[string]interface{}) {
		if status["thumbnail"] == nil {
			status["thumbnail"] = make(map[string]interface{})
		}
		thumbnail, ok := status["thumbnail"].(map[string]interface{})
		if !ok {
			// 兼容旧格式，如果是 bool，转换为 map
			thumbnail = make(map[string]interface{})
			status["thumbnail"] = thumbnail
		}
		thumbnail["status"] = "completed"
		thumbnail["resource_type"] = "thumbnail"
		thumbnail["downloaded"] = true
		thumbnail["downloaded_at"] = time.Now().Unix()
		if thumbnailPath != "" {
			thumbnail["file_path"] = thumbnailPath
		}
		if thumbnailURL != "" {
			thumbnail["url"] = thumbnailURL
		}
	})
}

// MarkSubtitleFailed 标记字幕下载失败
func (r *repository) MarkSubtitleFailed(videoDir string, lang string, errorMsg string) error {
	return r.updateDownloadStatus(videoDir, func(status map[string]interface{}) {
		if status["subtitles"] == nil {
			status["subtitles"] = make(map[string]interface{})
		}
		subtitles, ok := status["subtitles"].(map[string]interface{})
		if !ok {
			subtitles = make(map[string]interface{})
			status["subtitles"] = subtitles
		}

		subData := subtitles[lang]
		var sub map[string]interface{}
		if subMap, ok := subData.(map[string]interface{}); ok {
			sub = subMap
		} else {
			// 兼容旧格式，如果是 bool，转换为 map
			sub = make(map[string]interface{})
			subtitles[lang] = sub
		}

		sub["status"] = "failed"
		sub["downloaded"] = false
		sub["resource_type"] = "subtitle"
		if errorMsg != "" {
			sub["error"] = shortenErrorMessage(errorMsg)
		}
		sub["failed_at"] = time.Now().Unix()
	})
}

// updateDownloadStatus 更新下载状态文件
func (r *repository) updateDownloadStatus(videoDir string, updateFunc func(map[string]interface{})) error {
	statusFile := filepath.Join(videoDir, "download_status.json")

	// 确保视频目录存在
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return fmt.Errorf("创建视频目录失败: %w", err)
	}

	// 读取现有状态
	status := make(map[string]interface{})
	if data, err := os.ReadFile(statusFile); err == nil {
		json.Unmarshal(data, &status)
	}

	// 确保 subtitles 字段存在
	if status["subtitles"] == nil {
		status["subtitles"] = make(map[string]interface{})
	}

	// 更新状态
	updateFunc(status)

	// 保存状态
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化下载状态失败: %w", err)
	}

	if err := os.WriteFile(statusFile, data, 0644); err != nil {
		return fmt.Errorf("保存下载状态失败: %w", err)
	}

	return nil
}

// EnsureVideoDirByTitle 根据标题创建视频目录
func (r *repository) EnsureVideoDirByTitle(channelID, title string) (string, error) {
	sanitizedTitle := r.SanitizeTitle(title)
	videoDir := filepath.Join(r.outputDir, channelID, sanitizedTitle)
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return "", fmt.Errorf("创建视频目录失败: %w", err)
	}
	return videoDir, nil
}

// SavePendingDownloads 保存待下载资源状态
func (r *repository) SavePendingDownloads(channelID string, pending *PendingDownloads) error {
	channelDir := filepath.Join(r.outputDir, channelID)
	statusFile := filepath.Join(channelDir, "pending_downloads.json")

	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化待下载状态失败: %w", err)
	}

	if err := os.WriteFile(statusFile, data, 0644); err != nil {
		return fmt.Errorf("保存待下载状态失败: %w", err)
	}

	return nil
}

// LoadPendingDownloads 加载待下载资源状态
func (r *repository) LoadPendingDownloads(channelID string) (*PendingDownloads, error) {
	channelDir := filepath.Join(r.outputDir, channelID)
	statusFile := filepath.Join(channelDir, "pending_downloads.json")

	data, err := os.ReadFile(statusFile)
	if err != nil {
		return nil, err
	}

	var pending PendingDownloads
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, fmt.Errorf("解析待下载状态失败: %w", err)
	}

	return &pending, nil
}

// UpdatePendingDownloadStatus 更新待下载资源状态
func (r *repository) UpdatePendingDownloadStatus(channelID, videoID, resourceType, status, filePath string) error {
	pending, err := r.LoadPendingDownloads(channelID)
	if err != nil {
		// 如果文件不存在，不更新
		return nil
	}

	// 查找对应的视频
	for i := range pending.Videos {
		if pending.Videos[i].VideoID == videoID {
			switch resourceType {
			case "video":
				pending.Videos[i].Video.Status = status
				if filePath != "" {
					pending.Videos[i].Video.FilePath = filePath
				}
				if status == "completed" {
					pending.Videos[i].Video.DownloadedAt = time.Now().Unix()
				}
			case "thumbnail":
				pending.Videos[i].Thumbnail.Status = status
				if filePath != "" {
					pending.Videos[i].Thumbnail.FilePath = filePath
				}
				if status == "completed" {
					pending.Videos[i].Thumbnail.DownloadedAt = time.Now().Unix()
				}
			default:
				// 字幕（resourceType 是语言代码）
				if pending.Videos[i].Subtitles == nil {
					pending.Videos[i].Subtitles = make(map[string]PendingResourceStatus)
				}
				subStatus := pending.Videos[i].Subtitles[resourceType]
				subStatus.Status = status
				if filePath != "" {
					subStatus.FilePath = filePath
				}
				if status == "completed" {
					subStatus.DownloadedAt = time.Now().Unix()
				}
				pending.Videos[i].Subtitles[resourceType] = subStatus
			}
			break
		}
	}

	return r.SavePendingDownloads(channelID, pending)
}

// IsVideoUploaded 检查视频是否已上传到B站
func (r *repository) IsVideoUploaded(videoDir string) bool {
	statusFile := filepath.Join(videoDir, "upload_status.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		// 文件不存在，说明未上传
		return false
	}

	var status map[string]interface{}
	if err := json.Unmarshal(data, &status); err != nil {
		return false
	}

	// 检查上传状态
	if uploadStatus, ok := status["status"].(string); ok {
		return uploadStatus == "completed"
	}

	// 兼容旧格式（直接是 bool）
	if uploaded, ok := status["uploaded"].(bool); ok {
		return uploaded
	}

	return false
}

// MarkVideoUploading 标记视频开始上传
func (r *repository) MarkVideoUploading(videoDir string) error {
	return r.updateUploadStatus(videoDir, func(status map[string]interface{}) {
		status["status"] = "uploading"
		status["uploaded"] = false
		status["started_at"] = time.Now().Unix()
		// 清除之前的错误信息
		delete(status, "error")
		delete(status, "failed_at")
		delete(status, "bilibili_aid")
	})
}

// MarkVideoUploaded 标记视频上传完成
func (r *repository) MarkVideoUploaded(videoDir string, bilibiliAID string, bilibiliAccount string) error {
	return r.updateUploadStatus(videoDir, func(status map[string]interface{}) {
		status["status"] = "completed"
		status["uploaded"] = true
		status["bilibili_aid"] = bilibiliAID
		if bilibiliAccount != "" {
			status["bilibili_account"] = bilibiliAccount
		}
		status["completed_at"] = time.Now().Unix()
		// 清除错误信息
		delete(status, "error")
		delete(status, "failed_at")
	})
}

// MarkVideoUploadFailed 标记视频上传失败
func (r *repository) MarkVideoUploadFailed(videoDir string, errorMsg string) error {
	return r.updateUploadStatus(videoDir, func(status map[string]interface{}) {
		status["status"] = "failed"
		status["uploaded"] = false
		if errorMsg != "" {
			status["error"] = shortenErrorMessage(errorMsg)
		}
		status["failed_at"] = time.Now().Unix()
	})
}

// updateUploadStatus 更新上传状态文件
func (r *repository) updateUploadStatus(videoDir string, updateFunc func(map[string]interface{})) error {
	// 确保视频目录存在
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return fmt.Errorf("创建视频目录失败: %w", err)
	}

	statusFile := filepath.Join(videoDir, "upload_status.json")

	// 读取现有状态（如果存在）
	var status map[string]interface{}
	data, err := os.ReadFile(statusFile)
	if err == nil {
		if err := json.Unmarshal(data, &status); err != nil {
			// 如果解析失败，创建新状态
			status = make(map[string]interface{})
		}
	} else {
		// 文件不存在，创建新状态
		status = make(map[string]interface{})
	}

	// 更新时间戳
	status["updated_at"] = time.Now().Unix()

	// 调用更新函数
	updateFunc(status)

	// 保存状态
	data, err = json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化上传状态失败: %w", err)
	}

	if err := os.WriteFile(statusFile, data, 0644); err != nil {
		return fmt.Errorf("保存上传状态失败: %w", err)
	}

	return nil
}

// FindCoverFile 查找封面图文件（cover.{ext}，支持多种图片格式）
func (r *repository) FindCoverFile(videoDir string) (string, error) {
	// 尝试查找 cover.{ext} 格式的文件（支持多种扩展名）
	possibleExtensions := []string{".jpg", ".jpeg", ".png", ".webp", ".gif"}
	for _, ext := range possibleExtensions {
		coverPath := filepath.Join(videoDir, "cover"+ext)
		if _, err := os.Stat(coverPath); err == nil {
			return coverPath, nil
		}
	}
	return "", fmt.Errorf("未找到封面图文件")
}

// GetSubtitleLanguagesFromStatus 从 download_status.json 中提取字幕语言列表
func (r *repository) GetSubtitleLanguagesFromStatus(videoDir string) ([]string, error) {
	statusFile := filepath.Join(videoDir, "download_status.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		return nil, fmt.Errorf("读取下载状态文件失败: %w", err)
	}

	var status map[string]interface{}
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("解析下载状态文件失败: %w", err)
	}

	var languages []string
	if subtitles, ok := status["subtitles"].(map[string]interface{}); ok {
		for lang := range subtitles {
			languages = append(languages, lang)
		}
	}

	return languages, nil
}

// ---------- 账号上传计数（按天） ----------

func (r *repository) countersFile() string {
	globalDir := filepath.Join(r.outputDir, ".global")
	_ = os.MkdirAll(globalDir, 0755)
	return filepath.Join(globalDir, "upload_counters.json")
}

func (r *repository) loadCountersRaw() (*uploadCounters, error) {
	path := r.countersFile()
	data, err := os.ReadFile(path)
	if err != nil {
		// 不存在则初始化
		return &uploadCounters{
			Date:   time.Now().Format("2006-01-02"),
			Counts: map[string]int{},
		}, nil
	}
	var uc uploadCounters
	if err := json.Unmarshal(data, &uc); err != nil {
		return &uploadCounters{
			Date:   time.Now().Format("2006-01-02"),
			Counts: map[string]int{},
		}, nil
	}
	// 跨天则重置
	today := time.Now().Format("2006-01-02")
	if uc.Date != today {
		uc = uploadCounters{
			Date:   today,
			Counts: map[string]int{},
		}
	}
	return &uc, nil
}

func (r *repository) saveCountersRaw(uc *uploadCounters) error {
	path := r.countersFile()
	data, err := json.MarshalIndent(uc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (r *repository) LoadTodayUploadCounts() (map[string]int, error) {
	uc, err := r.loadCountersRaw()
	if err != nil {
		return nil, err
	}
	// 拷贝一份返回
	m := make(map[string]int, len(uc.Counts))
	for k, v := range uc.Counts {
		m[k] = v
	}
	return m, nil
}

func (r *repository) GetTodayUploadCount(account string) (int, error) {
	uc, err := r.loadCountersRaw()
	if err != nil {
		return 0, err
	}
	return uc.Counts[account], nil
}

func (r *repository) IncrementTodayUploadCount(account string) error {
	uc, err := r.loadCountersRaw()
	if err != nil {
		return err
	}
	uc.Counts[account] = uc.Counts[account] + 1
	return r.saveCountersRaw(uc)
}
