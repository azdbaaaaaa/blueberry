package bilibili

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"blueberry/internal/config"
	"blueberry/pkg/logger"
	"blueberry/pkg/subtitle"
)

// httpUploader 基于 HTTP 请求的 B站上传器实现
type httpUploader struct {
	baseURL            string
	cookiesFromBrowser string
	cookiesFile        string
	httpClient         *http.Client
	cookies            []Cookie
	csrfToken          string
	// Derived from preupload
	uposEndpointHost string
	uposChunkSize    int64
	uposPutQuery     string
}

// NewHTTPUploader 创建基于 HTTP 的上传器
func NewHTTPUploader(baseURL, cookiesFromBrowser, cookiesFile string) Uploader {
	return &httpUploader{
		baseURL:            baseURL,
		cookiesFromBrowser: cookiesFromBrowser,
		cookiesFile:        cookiesFile,
		httpClient: &http.Client{
			Timeout: 0, // 不设置全局超时，使用 context 控制超时
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
				DisableKeepAlives:   false,
				MaxIdleConnsPerHost: 5,
			},
		},
	}
}

// UploadVideo 上传视频（HTTP 实现）
func (u *httpUploader) UploadVideo(ctx context.Context, videoPath, videoTitle, videoDesc string, subtitlePaths []string, account config.Account) (*UploadResult, error) {
	result := &UploadResult{}

	// 确定使用的 cookies 配置（优先账号级别，否则全局）
	cookiesFile := account.CookiesFile
	if cookiesFile == "" {
		cookiesFile = u.cookiesFile
	}

	// 加载 cookies
	if err := u.loadCookies(cookiesFile); err != nil {
		return nil, fmt.Errorf("加载 cookies 失败: %w", err)
	}

	// 提取 CSRF token
	if err := u.extractCSRFToken(); err != nil {
		return nil, fmt.Errorf("提取 CSRF token 失败: %w", err)
	}

	// 先访问上传页面，确保会话有效并获取上传权限
	// 这一步很重要，因为 B站可能需要先访问页面才能获得上传权限
	if err := u.visitUploadPage(ctx); err != nil {
		logger.Warn().Err(err).Msg("访问上传页面失败，继续尝试上传")
		// 不返回错误，继续尝试上传
	}

	// 0. 清理路径中的转义字符（处理 shell 转义）
	videoPath = u.cleanPath(videoPath)
	for i := range subtitlePaths {
		subtitlePaths[i] = u.cleanPath(subtitlePaths[i])
	}

	// 1. 在开始上传前检查所需文件是否存在
	videoDir := filepath.Dir(videoPath)
	actualVideoPath, err := u.validateRequiredFiles(videoPath, subtitlePaths, videoDir)
	if err != nil {
		return nil, fmt.Errorf("文件检查失败: %w", err)
	}

	// 1.1 先上传封面图；封面失败则直接跳过该视频
	var coverURL string
	{
		// 查找封面文件路径（优先：与视频同名的 .jpg → cover.{jpg|jpeg|png|webp|gif} → thumbnail.jpg）
		dir := filepath.Dir(videoPath)
		coverPath := ""
		// 1) 与视频同名的 jpg（来自 yt-dlp --convert-thumbnails jpg）
		base := strings.TrimSuffix(actualVideoPath, filepath.Ext(actualVideoPath))
		candidate := base + ".jpg"
		if _, statErr := os.Stat(candidate); statErr == nil {
			coverPath = candidate
			logger.Debug().Str("path", candidate).Msg("使用与视频同名的 JPG 缩略图作为封面图（优先）")
		}
		// 2) cover.{jpg|jpeg|png|webp|gif}
		if coverPath == "" {
			for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
				p := filepath.Join(dir, "cover"+ext)
				if _, statErr := os.Stat(p); statErr == nil {
					coverPath = p
					break
				}
			}
		}
		// 3) thumbnail.jpg
		if coverPath == "" {
			thumb := filepath.Join(dir, "thumbnail.jpg")
			if _, statErr := os.Stat(thumb); statErr == nil {
				coverPath = thumb
				logger.Debug().Str("path", thumb).Msg("使用 thumbnail.jpg 作为封面图（回退）")
			}
		}
		if coverPath == "" {
			return nil, fmt.Errorf("未找到封面图文件（需要与视频同名的 .jpg，或 cover.{jpg|jpeg|png|webp|gif}，或 thumbnail.jpg）")
		}
		coverURL, err = u.uploadCover(ctx, coverPath, actualVideoPath)
		if err != nil {
			// 明确日志并跳过该视频
			logger.Error().Err(err).Str("video_path", actualVideoPath).Msg("封面图上传失败，跳过该视频")
			return nil, fmt.Errorf("封面图上传失败，跳过该视频: %w", err)
		}
		logger.Info().Str("cover_url", coverURL).Msg("封面图上传完成（优先）")
	}

	// 2. 先上传字幕（如失败仅警告继续）
	var subtitleURL string
	if len(subtitlePaths) > 0 {
		const maxSubtitleRetries = 3
		var lastSubErr error
		for attempt := 1; attempt <= maxSubtitleRetries; attempt++ {
			subtitleURL, lastSubErr = u.uploadSubtitles(ctx, subtitlePaths)
			if lastSubErr == nil {
				logger.Info().Int("attempt", attempt).Str("subtitle_url", subtitleURL).Msg("字幕上传完成")
				break
			}
			logger.Warn().
				Int("attempt", attempt).
				Int("max_retries", maxSubtitleRetries).
				Err(lastSubErr).
				Strs("subtitle_paths", subtitlePaths).
				Msg("上传字幕失败，将重试")
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		if lastSubErr != nil {
			logger.Warn().
				Err(lastSubErr).
				Strs("subtitle_paths", subtitlePaths).
				Msg("上传字幕失败，已用尽重试。继续上传其他内容（视频和封面图）。如需字幕，请手动检查并修复后重新上传")
		}
	}

	// 3. 上传视频（使用实际找到的文件路径）
	filename, err := u.uploadVideo(ctx, actualVideoPath)
	if err != nil {
		return nil, fmt.Errorf("上传视频失败: %w", err)
	}
	logger.Info().Str("filename", filename).Msg("视频上传完成")

	// 5. 发布视频
	// 注意：发布时 filename 不应该包含 .mp4 后缀
	publishFilename := filename
	if strings.HasSuffix(publishFilename, ".mp4") {
		publishFilename = strings.TrimSuffix(publishFilename, ".mp4")
		logger.Debug().
			Str("original", filename).
			Str("publish", publishFilename).
			Msg("移除 .mp4 后缀用于发布")
	}
	aid, err := u.publishVideo(ctx, publishFilename, videoTitle, coverURL, subtitleURL, videoDesc)
	if err != nil {
		return nil, fmt.Errorf("发布视频失败: %w", err)
	}

	result.Success = true
	result.AID = aid
	result.VideoID = aid

	logger.Info().
		Str("bilibili_aid", aid).
		Str("filename", filename).
		Str("title", videoTitle).
		Msg("视频发布成功，上传流程完成")

	return result, nil
}

// CheckLoginStatus 检查登录状态（HTTP 实现）
func (u *httpUploader) CheckLoginStatus(ctx context.Context) (bool, error) {
	// 加载 cookies
	if err := u.loadCookies(u.cookiesFile); err != nil {
		return false, err
	}

	// 尝试访问需要登录的页面
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/archive/new", u.baseURL), nil)
	if err != nil {
		return false, err
	}

	u.setCookies(req)
	u.setHeaders(req)

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// 如果返回 200 且能访问上传页面，说明已登录
	return resp.StatusCode == 200, nil
}

// visitUploadPage 访问上传页面，确保会话有效并获取上传权限
func (u *httpUploader) visitUploadPage(ctx context.Context) error {
	// 访问 studio.bilibili.tv 的上传页面
	uploadPageURL := "https://studio.bilibili.tv/archive/new"

	req, err := http.NewRequestWithContext(ctx, "GET", uploadPageURL, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	u.setCookies(req)
	u.setHeaders(req)

	// 设置正确的 Referer 和 Origin
	req.Header.Set("Referer", "https://studio.bilibili.tv/")
	req.Header.Set("Origin", "https://studio.bilibili.tv")

	logger.Debug().Str("url", uploadPageURL).Msg("访问上传页面以获取上传权限")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("访问上传页面失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Warn().
			Int("status_code", resp.StatusCode).
			Str("response", string(bodyBytes)).
			Msg("访问上传页面返回非200状态码")
		return fmt.Errorf("访问上传页面失败: HTTP %d", resp.StatusCode)
	}

	logger.Debug().Msg("成功访问上传页面，已获取上传权限")
	return nil
}

// maskCookieHeader 掩码 Cookie header 中的敏感信息（用于日志）
func maskCookieHeader(cookieHeader string) string {
	if cookieHeader == "" {
		return ""
	}
	// 简单处理：只显示前20个字符和后10个字符
	if len(cookieHeader) > 30 {
		return cookieHeader[:20] + "..." + cookieHeader[len(cookieHeader)-10:]
	}
	return cookieHeader
}

// loadCookies 加载 cookies 文件
func (u *httpUploader) loadCookies(cookiesPath string) error {
	if cookiesPath == "" {
		return fmt.Errorf("cookies 文件路径为空")
	}

	// 解析为绝对路径
	if !filepath.IsAbs(cookiesPath) {
		if absPath, err := filepath.Abs(cookiesPath); err == nil {
			cookiesPath = absPath
		}
	}

	// 复用现有的 cookies 解析逻辑
	uploader := &uploader{} // 临时创建 uploader 以使用其解析方法
	cookies, err := uploader.parseCookiesFile(cookiesPath)
	if err != nil {
		return fmt.Errorf("解析 cookies 文件失败: %w", err)
	}

	u.cookies = cookies
	logger.Info().Int("count", len(cookies)).Str("path", cookiesPath).Msg("已加载 cookies")
	return nil
}

// extractCSRFToken 从 cookies 中提取 CSRF token
func (u *httpUploader) extractCSRFToken() error {
	for _, cookie := range u.cookies {
		if cookie.Name == "csrf" || cookie.Name == "bili_jct" {
			u.csrfToken = cookie.Value
			logger.Info().Str("csrf", previewForLog(u.csrfToken, 10)).Msg("已提取 CSRF token")
			return nil
		}
	}
	return fmt.Errorf("未找到 CSRF token（查找 csrf 或 bili_jct cookie）")
}

// setCookies 设置请求的 cookies
func (u *httpUploader) setCookies(req *http.Request) {
	// 对于跨域请求（如上传到 OSS），需要手动构建 Cookie header
	// 因为 req.AddCookie 可能不会为不同域名的请求设置 cookies
	var cookieParts []string
	for _, cookie := range u.cookies {
		// 对于 bilibili 相关的域名，都应该设置 cookies
		// 特别是上传到 upos-cs-upcdntxa.bilivideo.com 时，需要 bilibili.tv 的 cookies
		cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", cookie.Name, cookie.Value))
	}

	if len(cookieParts) > 0 {
		req.Header.Set("Cookie", strings.Join(cookieParts, "; "))
	}

	// 同时也尝试使用 AddCookie（作为备用，虽然可能不会生效）
	for _, cookie := range u.cookies {
		req.AddCookie(&http.Cookie{
			Name:   cookie.Name,
			Value:  cookie.Value,
			Domain: cookie.Domain,
			Path:   cookie.Path,
		})
	}
}

// setHeaders 设置请求头
func (u *httpUploader) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36")

	// 根据请求 URL 设置正确的 Referer 和 Origin
	requestURL := req.URL.String()
	if strings.Contains(requestURL, "upos-cs-upcdntxa.bilivideo.com") {
		// 上传到 OSS 的请求，使用 studio.bilibili.tv 作为 Referer（注意：是根路径，不是 /archive/new）
		req.Header.Set("Referer", "https://studio.bilibili.tv/")
		req.Header.Set("Origin", "https://studio.bilibili.tv")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	} else if strings.Contains(requestURL, "api.bilibili.tv") {
		// API 请求，使用 studio.bilibili.tv（注意：Referer 是根路径，不是 /archive/new）
		req.Header.Set("Referer", "https://studio.bilibili.tv/")
		req.Header.Set("Origin", "https://studio.bilibili.tv")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	} else {
		// 其他请求，使用配置的 baseURL
		req.Header.Set("Referer", fmt.Sprintf("%s/archive/new", u.baseURL))
		req.Header.Set("Origin", u.baseURL)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	}
}

// buildAPIURL 构建 API URL（包含必需的 query 参数）
func (u *httpUploader) buildAPIURL(endpoint string) string {
	baseURL := "https://api.bilibili.tv"
	if strings.HasPrefix(endpoint, "http") {
		baseURL = ""
	}

	apiURL := baseURL + endpoint
	parsedURL, err := url.Parse(apiURL)
	if err != nil {
		return apiURL
	}

	query := parsedURL.Query()
	query.Set("lang_id", "3")
	query.Set("platform", "web")
	query.Set("lang", "en_US")
	query.Set("s_locale", "en_US")
	query.Set("timezone", "GMT+08:00")
	query.Set("csrf", u.csrfToken)

	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}

// uploadVideo 上传视频（分块上传）
func (u *httpUploader) uploadVideo(ctx context.Context, videoPath string) (string, error) {
	file, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("打开视频文件失败: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("获取文件信息失败: %w", err)
	}

	fileSize := fileInfo.Size()
	filename := generateFilename(filepath.Base(videoPath))

	logger.Info().
		Str("filename", filename).
		Int64("size", fileSize).
		Msg("开始上传视频")

	// 0. 调用 preupload API 获取上传配置和认证信息
	uposAuth, actualFilename, err := u.getUposAuth(ctx, filename, fileSize)
	if err != nil {
		logger.Warn().Err(err).Msg("获取上传认证信息失败，尝试不使用 X-Upos-Auth")
		// 不返回错误，继续尝试上传，使用原始文件名
		actualFilename = filename
	} else if actualFilename != "" {
		// 使用 preupload 返回的实际文件名
		filename = actualFilename
		logger.Debug().Str("actual_filename", filename).Msg("使用 preupload 返回的文件名")
	}

	// 选择上传主机与分块大小（来自 preupload 返回的 endpoint 与 chunk_size）
	baseUposHost := u.uposEndpointHost
	if baseUposHost == "" {
		baseUposHost = "https://upos-cs-upcdntxa.bilivideo.com"
	}
	chunkSize := u.uposChunkSize
	if chunkSize <= 0 {
		// 默认使用 22,020,096 字节（与 preupload 常见返回一致）
		chunkSize = 22020096
	}

	logger.Info().
		Str("upload_host", baseUposHost).
		Int64("chunk_size", chunkSize).
		Str("filename_initial", filename).
		Msg("即将初始化分片上传")

	// 1. 初始化上传
	uploadURL := fmt.Sprintf("%s/iupever/%s?uploads&output=json", baseUposHost, filename)
	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, nil)
	if err != nil {
		return "", err
	}

	// 设置 cookies 和 headers（初始化上传也需要认证）
	u.setCookies(req)
	u.setHeaders(req)

	// 上传到 OSS 的请求需要额外的 headers
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	// 设置 X-Upos-Auth header（如果获取到了）
	if uposAuth != "" {
		req.Header.Set("X-Upos-Auth", uposAuth)
		logger.Debug().Str("x_upos_auth", previewForLog(uposAuth, 50)).Msg("已设置 X-Upos-Auth header")
	} else {
		logger.Warn().Msg("未设置 X-Upos-Auth header，可能影响上传")
	}

	// 记录请求信息以便调试（使用 Info 级别，确保能看到）
	logger.Info().
		Str("url", uploadURL).
		Str("referer", req.Header.Get("Referer")).
		Str("origin", req.Header.Get("Origin")).
		Str("cookie_header", maskCookieHeader(req.Header.Get("Cookie"))).
		Str("x_upos_auth", maskCookieHeader(uposAuth)).
		Int("cookie_count", len(u.cookies)).
		Msg("初始化上传请求")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("初始化上传失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体以便调试
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取初始化响应失败: %w", err)
	}

	// 检查状态码
	if resp.StatusCode != 200 {
		logger.Error().
			Int("status_code", resp.StatusCode).
			Str("response", string(bodyBytes)).
			Msg("初始化上传返回非200状态码")
		return "", fmt.Errorf("初始化上传失败: HTTP %d, 响应: %s", resp.StatusCode, string(bodyBytes))
	}

	// 解析响应
	// 解析响应（初始化上传 API 直接返回数据，格式：{"OK":1,"bucket":"iupever","key":"/filename.mp4","upload_id":"..."}）
	var initResp map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &initResp); err != nil {
		logger.Error().
			Err(err).
			Str("response", string(bodyBytes)).
			Msg("解析初始化响应失败")
		return "", fmt.Errorf("解析初始化响应失败: %w, 响应: %s", err, string(bodyBytes))
	}

	// 检查 OK 字段
	if ok, _ := initResp["OK"].(float64); ok != 1 {
		logger.Error().
			Str("response", string(bodyBytes)).
			Msg("初始化上传返回 OK != 1")
		return "", fmt.Errorf("初始化上传失败: OK=%v, 响应: %s", ok, string(bodyBytes))
	}

	// 获取 upload_id（注意：是下划线，不是驼峰）
	uploadID := ""
	if id, ok := initResp["upload_id"].(string); ok {
		uploadID = id
	} else if id, ok := initResp["uploadId"].(string); ok {
		// 兼容驼峰格式
		uploadID = id
	}

	if uploadID == "" {
		logger.Error().
			Str("response", string(bodyBytes)).
			Msg("初始化响应中 upload_id 为空")
		return "", fmt.Errorf("初始化响应中 upload_id 为空，响应: %s", string(bodyBytes))
	}

	// 可选字段：bucket/key（若返回则记录，便于追踪）
	bucket := ""
	if b, ok := initResp["bucket"].(string); ok {
		bucket = b
	}
	key := ""
	if k, ok := initResp["key"].(string); ok {
		key = k
	}

	logger.Info().
		Str("upload_id", uploadID).
		Str("bucket", bucket).
		Str("key", key).
		Msg("已初始化上传")

	// 2. 分块上传
	chunks := int((fileSize + chunkSize - 1) / chunkSize)

	for chunk := 0; chunk < chunks; chunk++ {
		start := int64(chunk) * chunkSize
		end := start + chunkSize
		if end > fileSize {
			end = fileSize
		}

		partNumber := chunk + 1
		chunkData := make([]byte, end-start)
		if _, err := file.ReadAt(chunkData, start); err != nil {
			return "", fmt.Errorf("读取分块 %d 失败: %w", partNumber, err)
		}

		chunkURL := fmt.Sprintf("%s/iupever/%s?partNumber=%d&uploadId=%s&chunk=%d&chunks=%d&size=%d&start=%d&end=%d&total=%d",
			baseUposHost, filename, partNumber, uploadID, chunk, chunks, end-start, start, end, fileSize)

		req, err := http.NewRequestWithContext(ctx, "PUT", chunkURL, bytes.NewReader(chunkData))
		if err != nil {
			return "", fmt.Errorf("创建分块上传请求失败: %w", err)
		}

		// 设置 headers（分块上传需要 X-Upos-Auth 和正确的 headers）
		u.setHeaders(req)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(chunkData)))

		// 设置 X-Upos-Auth header（每个分块上传都需要）
		if uposAuth != "" {
			req.Header.Set("X-Upos-Auth", uposAuth)
		} else {
			logger.Warn().Int("chunk", partNumber).Msg("分块上传时未设置 X-Upos-Auth header")
		}

		// 添加重试机制（网络错误与非200/非204响应均重试）
		var resp *http.Response
		maxRetries := 3
		backoffBase := 1
		if cfg := config.Get(); cfg != nil {
			if cfg.Bilibili.ChunkUploadRetries > 0 {
				maxRetries = cfg.Bilibili.ChunkUploadRetries
			}
			if cfg.Bilibili.ChunkRetryBackoffSeconds > 0 {
				backoffBase = cfg.Bilibili.ChunkRetryBackoffSeconds
			}
		}
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			resp, err = u.httpClient.Do(req)
			if err == nil && resp != nil && (resp.StatusCode == 200 || resp.StatusCode == 204) {
				resp.Body.Close()
				lastErr = nil
				break
			}
			// 生成错误信息
			var statusCode int
			var bodyPreview string
			if resp != nil {
				statusCode = resp.StatusCode
				bodyBytes, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				bodyPreview = string(bodyBytes)
			}
			if err != nil {
				lastErr = err
			} else {
				lastErr = fmt.Errorf("HTTP %d, 响应: %s", statusCode, bodyPreview)
			}
			// 重试或失败退出
			if attempt < maxRetries {
				waitTime := time.Duration(attempt*backoffBase) * time.Second
				logger.Warn().
					Int("chunk", partNumber).
					Int("attempt", attempt).
					Int("max", maxRetries).
					Dur("wait", waitTime).
					Err(lastErr).
					Msg("分块上传失败，准备重试")
				time.Sleep(waitTime)
				// 重新创建请求（因为 body 已经被读取）
				req, _ = http.NewRequestWithContext(ctx, "PUT", chunkURL, bytes.NewReader(chunkData))
				u.setHeaders(req)
				req.Header.Set("Content-Type", "application/octet-stream")
				req.Header.Set("Content-Length", fmt.Sprintf("%d", len(chunkData)))
				if uposAuth != "" {
					req.Header.Set("X-Upos-Auth", uposAuth)
				}
				continue
			}
			// 已用尽重试
			return "", fmt.Errorf("上传分块 %d 失败（已重试 %d 次）: %w", partNumber, maxRetries, lastErr)
		}

		logger.Info().
			Int("chunk", partNumber).
			Int("total", chunks).
			Int64("size", end-start).
			Msg("分块上传完成")

		// 在每个分块上传之间添加短暂延迟，避免连接被关闭
		if chunk < chunks-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 2.5 可选：调用 UPOS 完成接口，获取 bucket/key/location（仅用于日志校验）
	// 解析 profile 值（来自 preupload put_query）
	profileVal := "iup/bup"
	if u.uposPutQuery != "" {
		if vals, err := url.ParseQuery(u.uposPutQuery); err == nil {
			if pv := vals.Get("profile"); pv != "" {
				profileVal = pv
			}
		}
	}
	finalizeURL := fmt.Sprintf("%s/iupever/%s?output=json&name=%s&profile=%s&uploadId=%s&biz_id=&biz=UGC",
		baseUposHost, filename, url.QueryEscape(filepath.Base(videoPath)), url.QueryEscape(profileVal), uploadID)
	if req, err := http.NewRequestWithContext(ctx, "POST", finalizeURL, nil); err == nil {
		u.setHeaders(req)
		if uposAuth != "" {
			req.Header.Set("X-Upos-Auth", uposAuth)
		}
		if resp, err := u.httpClient.Do(req); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				var fin map[string]any
				if json.Unmarshal(body, &fin) == nil {
					logger.Info().
						Str("finalize_url", finalizeURL).
						Str("bucket", fmt.Sprint(fin["bucket"])).
						Str("key", fmt.Sprint(fin["key"])).
						Str("location", fmt.Sprint(fin["location"])).
						Msg("UPOS 完成上传（bucket/key/location）")
				} else {
					logger.Info().
						Str("finalize_url", finalizeURL).
						Str("response", previewForLog(string(body), 300)).
						Msg("UPOS 完成上传返回（解析失败，原样预览）")
				}
			} else {
				logger.Warn().
					Int("status", resp.StatusCode).
					Str("finalize_url", finalizeURL).
					Str("response", previewForLog(string(body), 300)).
					Msg("UPOS 完成上传返回非200（忽略继续）")
			}
		} else {
			logger.Warn().Err(err).Str("finalize_url", finalizeURL).Msg("UPOS 完成上传请求失败（忽略继续）")
		}
	}

	// 3. 完成上传
	completeURL := u.buildAPIURL("/intl/videoup/web2/uploading")
	formData := url.Values{}
	formData.Set("filename", filename)

	// 对完成上传增加重试（处理网络抖动 EOF 等）
	maxRetries := 3
	var completeErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err = http.NewRequestWithContext(ctx, "POST", completeURL, strings.NewReader(formData.Encode()))
		if err != nil {
			return "", err
		}
		u.setCookies(req)
		u.setHeaders(req)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err = u.httpClient.Do(req)
		if err == nil && resp != nil && resp.StatusCode == 200 {
			defer resp.Body.Close()
			logger.Info().Int("attempt", attempt).Msg("完成上传成功")
			goto COMPLETE_OK
		}

		// 记录错误详情
		status := 0
		bodyPreview := ""
		if resp != nil {
			status = resp.StatusCode
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			bodyPreview = previewForLog(string(b), 500)
		}
		logger.Warn().
			Int("attempt", attempt).
			Int("max_retries", maxRetries).
			Int("status", status).
			Err(err).
			Str("body", bodyPreview).
			Msg("完成上传失败，准备重试")

		completeErr = err
		// 指数退避
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	if completeErr != nil {
		return "", fmt.Errorf("完成上传失败: %w", completeErr)
	}
	return "", fmt.Errorf("完成上传失败")
COMPLETE_OK:

	logger.Info().Str("filename", filename).Msg("视频上传完成")
	return filename, nil
}

// getUposAuth 调用 preupload API 获取上传认证信息
func (u *httpUploader) getUposAuth(ctx context.Context, filename string, fileSize int64) (string, string, error) {
	// 构建 preupload API URL
	preuploadURL := u.buildAPIURL("/preupload")
	parsedURL, err := url.Parse(preuploadURL)
	if err != nil {
		return "", "", fmt.Errorf("解析 preupload URL 失败: %w", err)
	}

	query := parsedURL.Query()
	query.Set("name", filename)
	query.Set("size", fmt.Sprintf("%d", fileSize))
	query.Set("r", "upos")
	query.Set("profile", "iup/bup")
	query.Set("ssl", "0")
	query.Set("version", "2.10.0")
	query.Set("build", "2100000")
	query.Set("biz", "UGC")
	parsedURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", parsedURL.String(), nil)
	if err != nil {
		return "", "", fmt.Errorf("创建 preupload 请求失败: %w", err)
	}

	u.setCookies(req)
	u.setHeaders(req)

	logger.Debug().Str("url", parsedURL.String()).Msg("调用 preupload API")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("调用 preupload API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Warn().
			Int("status_code", resp.StatusCode).
			Str("response", string(bodyBytes)).
			Msg("preupload API 返回非200状态码")
		return "", "", fmt.Errorf("preupload API 返回 HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("读取 preupload 响应失败: %w", err)
	}

	// 解析响应（preupload API 直接返回数据，没有 code/data 包装）
	var preuploadResp map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &preuploadResp); err != nil {
		logger.Warn().
			Err(err).
			Str("response", string(bodyBytes)).
			Msg("解析 preupload 响应失败")
		return "", "", fmt.Errorf("解析 preupload 响应失败: %w", err)
	}

	// 检查 OK 字段
	if ok, _ := preuploadResp["OK"].(float64); ok != 1 {
		logger.Warn().
			Str("response", string(bodyBytes)).
			Msg("preupload API 返回 OK != 1")
		return "", "", fmt.Errorf("preupload API 返回失败: OK=%v", ok)
	}

	// 直接从根级别获取 auth 字段
	auth := ""
	if authVal, ok := preuploadResp["auth"].(string); ok && authVal != "" {
		// auth 字段中可能包含转义的字符（\u0026 是 &），需要解码
		auth = strings.ReplaceAll(authVal, "\\u0026", "&")
		logger.Info().Str("auth", previewForLog(auth, 50)).Msg("从 preupload 响应获取到 auth")
	} else {
		// 如果没有 auth 字段，记录警告
		logger.Warn().
			Str("response", string(bodyBytes)).
			Msg("preupload 响应中没有 auth 字段")
		return "", "", fmt.Errorf("preupload 响应中没有 auth 字段")
	}

	// 记录并保存 endpoint 和 chunk_size、put_query
	if ep, ok := preuploadResp["endpoint"].(string); ok && ep != "" {
		if strings.HasPrefix(ep, "//") {
			ep = "https:" + ep
		} else if !strings.HasPrefix(ep, "http") {
			ep = "https://" + ep
		}
		u.uposEndpointHost = strings.TrimRight(ep, "/")
	}
	if cs, ok := preuploadResp["chunk_size"].(float64); ok && cs > 0 {
		u.uposChunkSize = int64(cs)
	}
	if pq, ok := preuploadResp["put_query"].(string); ok {
		u.uposPutQuery = pq
	}
	logger.Info().
		Str("endpoint", u.uposEndpointHost).
		Int64("chunk_size", u.uposChunkSize).
		Str("put_query", u.uposPutQuery).
		Msg("preupload 返回的上传参数")

	// 从 upos_uri 中提取实际文件名
	actualFilename := filename
	if uposURI, ok := preuploadResp["upos_uri"].(string); ok && uposURI != "" {
		// upos_uri 格式：upos://iupever/n251209ad16g3917krfoey36fea2g002.mp4
		// 提取文件名部分
		if strings.HasPrefix(uposURI, "upos://iupever/") {
			actualFilename = strings.TrimPrefix(uposURI, "upos://iupever/")
			logger.Info().
				Str("upos_uri", uposURI).
				Str("server_filename", actualFilename).
				Msg("preupload 指定的服务端文件名")
		}
	}

	// 可选：记录 endpoints 数组（候选上传域名）
	if eps, ok := preuploadResp["endpoints"].([]interface{}); ok && len(eps) > 0 {
		list := make([]string, 0, len(eps))
		for _, e := range eps {
			if s, ok2 := e.(string); ok2 {
				list = append(list, s)
			}
		}
		if len(list) > 0 {
			logger.Info().Strs("endpoints", list).Msg("preupload 提供的候选上传域名")
		}
	}

	return auth, actualFilename, nil
}

// uploadSubtitles 上传字幕
func (u *httpUploader) uploadSubtitles(ctx context.Context, subtitlePaths []string) (string, error) {
	if len(subtitlePaths) == 0 {
		return "", nil
	}

	// 使用第一个字幕文件（通常是最主要的语言）
	srtPath := subtitlePaths[0]
	for _, path := range subtitlePaths {
		if strings.HasSuffix(strings.ToLower(path), ".srt") {
			srtPath = path
			break
		}
	}

	logger.Info().
		Str("srt_path", srtPath).
		Strs("all_subtitle_paths", subtitlePaths).
		Msg("开始上传字幕")

	// 1. 将 SRT 转换为 B站 JSON 格式
	entries, err := subtitle.ConvertSRTToBilibiliJSON(srtPath)
	if err != nil {
		return "", fmt.Errorf("转换字幕格式失败: %w", err)
	}

	// 2. 暂时跳过字幕合法性检查，直接进入上传流程
	if len(entries) == 0 {
		logger.Warn().Msg("字幕条目为空，跳过上传")
		return "", nil
	}
	logger.Info().
		Int("entry_count", len(entries)).
		Msg("已跳过字幕合法性检查（临时关闭）")

	// 3. 获取字幕直传 OSS 的临时凭证
	tokenURL := u.buildAPIURL("/intl/videoup/web2/upload/token?type=subtitle")
	tokenReq, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建字幕上传凭证请求失败: %w", err)
	}
	u.setCookies(tokenReq)
	u.setHeaders(tokenReq)
	logger.Info().
		Str("method", tokenReq.Method).
		Str("url", tokenReq.URL.String()).
		Str("referer", tokenReq.Header.Get("Referer")).
		Str("origin", tokenReq.Header.Get("Origin")).
		Msg("字幕上传凭证请求")
	tokenResp, err := u.httpClient.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("获取字幕上传凭证失败: %w", err)
	}
	defer tokenResp.Body.Close()
	tokenBody, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		return "", fmt.Errorf("读取字幕上传凭证响应失败: %w", err)
	}
	if tokenResp.StatusCode != 200 {
		return "", fmt.Errorf("获取字幕上传凭证失败: HTTP %d, body=%s", tokenResp.StatusCode, string(tokenBody))
	}
	var token struct {
		Code int            `json:"code"`
		Msg  string         `json:"message"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(tokenBody, &token); err != nil {
		return "", fmt.Errorf("解析字幕上传凭证失败: %w, body=%s", err, string(tokenBody))
	}
	if token.Code != 0 {
		return "", fmt.Errorf("字幕上传凭证返回错误: code=%d, message=%s, body=%s", token.Code, token.Msg, string(tokenBody))
	}
	extractStr := func(m map[string]any, keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if s, ok2 := v.(string); ok2 && s != "" {
					return s
				}
			}
		}
		return ""
	}
	host := extractStr(token.Data, "host", "Host")
	key := extractStr(token.Data, "key", "Key", "key_prefix", "keyPrefix", "dir", "Dir")
	accessKey := extractStr(token.Data, "OSSAccessKeyId", "accessid", "accessKeyId")
	policy := extractStr(token.Data, "policy", "Policy")
	signature := extractStr(token.Data, "signature", "Signature")
	successStatus := extractStr(token.Data, "success_action_status", "successActionStatus")
	if successStatus == "" {
		successStatus = "200"
	}
	if host == "" || accessKey == "" || policy == "" || signature == "" {
		return "", fmt.Errorf("字幕上传凭证字段缺失: host=%q key=%q accessKey=%q policy=%q signature=%q", host, key, accessKey, policy, signature)
	}
	// 如果 key 为空，尝试从 policy 的 starts-with 条件提取 key 前缀并拼接文件名
	// 如果 key 为空或看起来只是前缀（常见以 "_" 结尾或不包含 "subtitle-"），从 policy/或该前缀拼最终文件名
	if key == "" || strings.HasSuffix(key, "_") || !strings.Contains(key, "subtitle-") {
		type policyDoc struct {
			Expiration string        `json:"expiration"`
			Conditions []interface{} `json:"conditions"`
		}
		var doc policyDoc
		if decoded, err := base64.StdEncoding.DecodeString(policy); err == nil {
			_ = json.Unmarshal(decoded, &doc)
		} else if decoded, err2 := base64.RawStdEncoding.DecodeString(policy); err2 == nil {
			_ = json.Unmarshal(decoded, &doc)
		}
		prefix := ""
		for _, c := range doc.Conditions {
			// 期望形如 ["starts-with", "$key", "ugc/subtitle/1765..._hash_"]
			if arr, ok := c.([]interface{}); ok && len(arr) >= 3 {
				if s0, ok0 := arr[0].(string); ok0 && s0 == "starts-with" {
					if s1, ok1 := arr[1].(string); ok1 && (s1 == "$key" || s1 == "key") {
						if s2, ok2 := arr[2].(string); ok2 {
							prefix = s2
							break
						}
					}
				}
			}
		}
		ts := time.Now().UnixMilli()
		if prefix != "" {
			key = fmt.Sprintf("%ssubtitle-%d.json", prefix, ts)
		} else {
			// 如果 token 自身的 key 看起来像是前缀，优先使用它
			rawPrefix := extractStr(token.Data, "key", "Key", "key_prefix", "keyPrefix", "dir", "Dir")
			if rawPrefix != "" && (strings.HasSuffix(rawPrefix, "_") || !strings.Contains(rawPrefix, "subtitle-")) {
				key = fmt.Sprintf("%ssubtitle-%d.json", rawPrefix, ts)
			} else {
				key = fmt.Sprintf("ugc/subtitle/%d_%s_subtitle-%d.json", ts, generateHash(srtPath), ts)
			}
		}
		logger.Debug().Str("derived_key", key).Str("prefix", prefix).Msg("从 policy conditions 推导字幕 key")
	}
	if !strings.HasPrefix(host, "http") {
		host = "https://" + host
	}

	// 4. 构建字幕 JSON
	// 若同目录存在样式字幕 JSON，则优先使用并包含样式；否则从 SRT 生成样式 JSON。
	dir := filepath.Dir(srtPath)
	var subJSON []byte
	entryCount := 0
	usedStyled := false
	for _, name := range []string{"styled_subtitles.json", "subtitles_styled.json", "subtitles_rich.json"} {
		p := filepath.Join(dir, name)
		if _, statErr := os.Stat(p); statErr == nil {
			// 直接读取并使用用户提供的带样式 JSON（保持原格式：含 font_* 等顶层字段与 body[from/to/content]）
			raw, rErr := os.ReadFile(p)
			if rErr != nil {
				logger.Warn().Str("styled_json_path", p).Err(rErr).Msg("读取样式字幕JSON失败，回退使用 SRT 转换结果")
				break
			}
			// 尝试解析以获取条目数量与样式参数（用于日志）
			var styleMeta subtitle.StyledSubtitleJSON
			if jErr := json.Unmarshal(raw, &styleMeta); jErr == nil {
				entryCount = len(styleMeta.Body)
				subJSON = raw
				usedStyled = true
				logger.Info().
					Str("styled_json_path", p).
					Int("entry_count", entryCount).
					Float64("font_size", styleMeta.FontSize).
					Str("font_color", styleMeta.FontColor).
					Float64("background_alpha", styleMeta.BackgroundAlpha).
					Str("background_color", styleMeta.BackgroundColor).
					Str("stroke", styleMeta.Stroke).
					Msg("使用带样式的字幕 JSON（原样上传）")
				// 落盘调试文件（标准化缩进）
				if pretty, iErr := json.MarshalIndent(styleMeta, "", "  "); iErr == nil {
					debugPath := filepath.Join(dir, "subtitle_entries_full.json")
					_ = os.WriteFile(debugPath, pretty, 0644)
					logger.Info().Str("debug_path", debugPath).Msg("已保存完整字幕（带样式）用于调试")
				}
			} else {
				logger.Warn().Str("styled_json_path", p).Err(jErr).Msg("解析样式字幕JSON失败，回退使用 SRT 转换结果")
			}
			break
		}
	}
	if !usedStyled {
		// 从 SRT 生成带样式 JSON（包含 from/to/location 与全局样式）
		defaults := subtitle.StyledDefaults{
			FontSize:        0.4,
			FontColor:       "#FFFFFF",
			BackgroundAlpha: 0.5,
			BackgroundColor: "#9C27B0",
			Stroke:          "none",
			Location:        2,
		}
		if styled, cErr := subtitle.ConvertSRTToStyledJSON(srtPath, defaults); cErr == nil {
			if b, mErr := json.Marshal(styled); mErr == nil {
				subJSON = b
				entryCount = len(styled.Body)
				// 保存完整 entries 到文件（带样式）
				if pretty, iErr := json.MarshalIndent(styled, "", "  "); iErr == nil {
					debugPath := filepath.Join(dir, "subtitle_entries_full.json")
					_ = os.WriteFile(debugPath, pretty, 0644)
					logger.Info().Str("debug_path", debugPath).Msg("已保存完整字幕（SRT→带样式）用于调试")
				}
				usedStyled = true
				logger.Info().Int("entry_count", entryCount).Msg("已从 SRT 生成带样式字幕 JSON")
			} else {
				return "", fmt.Errorf("序列化样式字幕JSON失败: %w", mErr)
			}
		} else {
			return "", fmt.Errorf("从 SRT 生成样式字幕JSON失败: %w", cErr)
		}
	}

	// 5. 直传 OSS（multipart/form-data）
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("key", key)
	_ = writer.WriteField("OSSAccessKeyId", accessKey)
	_ = writer.WriteField("policy", policy)
	_ = writer.WriteField("signature", signature)
	_ = writer.WriteField("success_action_status", successStatus)
	// 确保 Content-Type 满足 policy 的 in 条件
	_ = writer.WriteField("Content-Type", "application/octet-stream")
	// 文件字段
	fileField, err := writer.CreateFormFile("file", "subtitle.json")
	if err != nil {
		return "", fmt.Errorf("创建字幕表单文件字段失败: %w", err)
	}
	if _, err := fileField.Write(subJSON); err != nil {
		return "", fmt.Errorf("写入字幕表单文件内容失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("关闭字幕表单失败: %w", err)
	}

	ossReq, err := http.NewRequestWithContext(ctx, "POST", host, &buf)
	if err != nil {
		return "", fmt.Errorf("创建字幕直传请求失败: %w", err)
	}
	ossReq.Header.Set("Content-Type", writer.FormDataContentType())
	// OSS 请求不强制需要 B站 Cookie，但保留通用 headers
	u.setHeaders(ossReq)
	// 打印字幕直传的请求详情（敏感字段做掩码）
	jsonMD5 := fmt.Sprintf("%x", md5.Sum(subJSON))
	policyPreview := previewForLog(policy, 48)
	signaturePreview := previewForLog(signature, 6)
	accessKeyPreview := previewForLog(accessKey, 6)
	formContentTypeField := "application/octet-stream"
	hostName := ""
	if ossReq.URL != nil {
		hostName = ossReq.URL.Host
	}
	logger.Info().
		Str("method", ossReq.Method).
		Str("url", ossReq.URL.String()).
		Str("host", hostName).
		Str("content_type", ossReq.Header.Get("Content-Type")).
		Int("content_length", buf.Len()).
		Str("form_key", key).
		Str("form_access_key_id_masked", accessKeyPreview).
		Str("form_policy_preview", policyPreview).
		Str("form_signature_masked", signaturePreview).
		Str("success_action_status", successStatus).
		Str("form_content_type_field", formContentTypeField).
		Int("subtitle_json_size", len(subJSON)).
		Str("subtitle_json_preview", previewForLog(string(subJSON), 400)).
		Str("subtitle_json_md5", jsonMD5).
		Str("referer", ossReq.Header.Get("Referer")).
		Str("origin", ossReq.Header.Get("Origin")).
		Msg("字幕直传请求详情")
	ossResp, err := u.httpClient.Do(ossReq)
	if err != nil {
		return "", fmt.Errorf("字幕直传失败: %w", err)
	}
	defer ossResp.Body.Close()
	ossBody, _ := io.ReadAll(ossResp.Body)
	if ossResp.StatusCode != 200 {
		return "", fmt.Errorf("字幕直传失败: HTTP %d, body=%s", ossResp.StatusCode, string(ossBody))
	}

	subtitleURL := key
	logger.Info().
		Str("subtitle_url", subtitleURL).
		Int("entry_count", len(entries)).
		Msg("字幕上传完成（直传 OSS）")
	return subtitleURL, nil
}

// checkSubtitleBatch 检查一批字幕条目的合法性
func (u *httpUploader) checkSubtitleBatch(ctx context.Context, checkURL string, batch []subtitle.BilibiliSubtitleEntry) ([]string, error) {
	checkData := map[string]interface{}{
		"subtitles": batch,
	}

	jsonData, err := json.Marshal(checkData)
	if err != nil {
		return nil, fmt.Errorf("序列化字幕数据失败: %w", err)
	}

	// 记录请求预览（限制长度）
	reqPreview := string(jsonData)
	if len(reqPreview) > 2000 {
		reqPreview = reqPreview[:2000] + "..."
	}
	logger.Debug().
		Int("batch_size", len(batch)).
		Int("request_size", len(jsonData)).
		Str("request_preview", reqPreview).
		Msg("字幕合法性检查请求（预览）")

	req, err := http.NewRequestWithContext(ctx, "POST", checkURL, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}

	u.setCookies(req)
	u.setHeaders(req)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("字幕合法性检查失败: 请求错误: %w (request_preview=%s)", err, previewForLog(string(jsonData), 1000))
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取字幕检查响应失败: %w", err)
	}

	if resp.StatusCode != 200 {
		logger.Error().
			Int("status_code", resp.StatusCode).
			Str("response", previewForLog(string(bodyBytes), 1000)).
			Str("request_preview", previewForLog(string(jsonData), 1000)).
			Msg("字幕合法性检查返回非200状态码")
		return nil, fmt.Errorf("字幕合法性检查失败: HTTP %d, 响应: %s", resp.StatusCode, string(bodyBytes))
	}

	var checkResult struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			HitIDs []string `json:"hit_ids"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &checkResult); err != nil {
		logger.Error().
			Err(err).
			Str("response", previewForLog(string(bodyBytes), 1000)).
			Str("request_preview", previewForLog(string(jsonData), 1000)).
			Msg("解析字幕检查响应失败")
		return nil, fmt.Errorf("解析检查响应失败: %w, 响应: %s", err, string(bodyBytes))
	}

	if checkResult.Code != 0 {
		logger.Error().
			Int("code", checkResult.Code).
			Str("message", checkResult.Message).
			Str("request_preview", previewForLog(string(jsonData), 1000)).
			Str("response_preview", previewForLog(string(bodyBytes), 1000)).
			Msg("字幕合法性检查返回错误")
		return nil, fmt.Errorf("字幕合法性检查失败: code=%d, message=%s", checkResult.Code, checkResult.Message)
	}

	return checkResult.Data.HitIDs, nil
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

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// previewForLog 返回用于日志预览的字符串，最长 limit 个“字符”（按 rune 截断），超出则追加 "..."
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

// generateHash 生成简单的哈希值（用于字幕文件名）
func generateHash(input string) string {
	hash := 0
	for _, char := range input {
		hash = hash*31 + int(char)
	}
	if hash < 0 {
		hash = -hash
	}
	return fmt.Sprintf("%x", hash)[:16] // 取前16位
}

// uploadCover 上传封面图
func (u *httpUploader) uploadCover(ctx context.Context, coverPath string, videoPath string) (string, error) {
	// 读取图片文件
	imageData, err := os.ReadFile(coverPath)
	if err != nil {
		return "", fmt.Errorf("读取封面图失败: %w", err)
	}

	// 检测文件类型（通过文件头判断）
	contentType := "image/jpeg" // 默认 JPEG
	if len(imageData) >= 4 {
		// PNG: 89 50 4E 47
		if imageData[0] == 0x89 && imageData[1] == 0x50 && imageData[2] == 0x4E && imageData[3] == 0x47 {
			contentType = "image/png"
		}
		// JPEG: FF D8 FF
		if imageData[0] == 0xFF && imageData[1] == 0xD8 && imageData[2] == 0xFF {
			contentType = "image/jpeg"
		}
		// WebP: 检查 RIFF...WEBP
		if len(imageData) >= 12 && string(imageData[0:4]) == "RIFF" && string(imageData[8:12]) == "WEBP" {
			contentType = "image/webp"
		}
	}

	// 转换为 base64
	base64Data := base64.StdEncoding.EncodeToString(imageData)
	coverData := fmt.Sprintf("data:%s;base64,%s", contentType, base64Data)

	logger.Debug().
		Str("cover_path", coverPath).
		Str("content_type", contentType).
		Int("file_size", len(imageData)).
		Msg("准备上传封面图")

	// 构建 multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	coverField, err := writer.CreateFormField("cover")
	if err != nil {
		return "", err
	}
	if _, err := coverField.Write([]byte(coverData)); err != nil {
		return "", err
	}
	writer.Close()

	// 发送请求
	apiURL := u.buildAPIURL("/intl/videoup/web2/cover")
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, &buf)
	if err != nil {
		return "", err
	}

	u.setCookies(req)
	u.setHeaders(req)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("上传封面图失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体以便调试
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取封面图上传响应失败: %w", err)
	}

	if resp.StatusCode != 200 {
		logger.Error().
			Int("status_code", resp.StatusCode).
			Str("response", string(bodyBytes)).
			Msg("封面图上传返回非200状态码")
		return "", fmt.Errorf("上传封面图失败: HTTP %d, 响应: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			URL string `json:"url"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		logger.Error().
			Err(err).
			Str("response", string(bodyBytes)).
			Msg("解析封面图上传响应失败")
		return "", fmt.Errorf("解析封面图上传响应失败: %w, 响应: %s", err, string(bodyBytes))
	}

	if result.Code != 0 {
		// 打印完整返回内容，便于排查问题
		logger.Error().
			Int("code", result.Code).
			Str("message", result.Message).
			Str("response_body", string(bodyBytes)).
			Msg("封面图上传返回错误")

		// 如果是尺寸相关错误（常见 -702），尝试自动调整尺寸后重试一次
		if result.Code == -702 {
			logger.Warn().Str("cover_path", coverPath).Msg("检测到封面尺寸不合规，尝试调整到 1280x720 并重试")
			resizedPath, contentType2, rerr := upscaleCoverTo1280x720(ctx, coverPath)
			if rerr != nil {
				return "", fmt.Errorf("封面图尺寸调整失败: %w", rerr)
			}

			// 重新读取并上传调整后的封面
			imageData2, r2 := os.ReadFile(resizedPath)
			if r2 != nil {
				return "", fmt.Errorf("读取调整后的封面图失败: %w", r2)
			}
			base64Data2 := base64.StdEncoding.EncodeToString(imageData2)
			coverData2 := fmt.Sprintf("data:%s;base64,%s", contentType2, base64Data2)

			var buf2 bytes.Buffer
			writer2 := multipart.NewWriter(&buf2)
			coverField2, c2 := writer2.CreateFormField("cover")
			if c2 != nil {
				return "", c2
			}
			if _, c2 = coverField2.Write([]byte(coverData2)); c2 != nil {
				return "", c2
			}
			writer2.Close()

			req2, r3 := http.NewRequestWithContext(ctx, "POST", u.buildAPIURL("/intl/videoup/web2/cover"), &buf2)
			if r3 != nil {
				return "", r3
			}
			u.setCookies(req2)
			u.setHeaders(req2)
			req2.Header.Set("Content-Type", writer2.FormDataContentType())

			resp2, r4 := u.httpClient.Do(req2)
			if r4 != nil {
				return "", fmt.Errorf("上传调整后的封面图失败: %w", r4)
			}
			defer resp2.Body.Close()
			bodyBytes2, _ := io.ReadAll(resp2.Body)
			if resp2.StatusCode != 200 {
				return "", fmt.Errorf("上传调整后的封面图失败: HTTP %d, 响应: %s", resp2.StatusCode, string(bodyBytes2))
			}
			var result2 struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    struct {
					URL string `json:"url"`
				} `json:"data"`
			}
			if jerr := json.Unmarshal(bodyBytes2, &result2); jerr != nil {
				return "", fmt.Errorf("解析调整后封面上传响应失败: %w, 响应: %s", jerr, string(bodyBytes2))
			}
			if result2.Code != 0 {
				logger.Error().Int("code", result2.Code).Str("message", result2.Message).Str("response_body", string(bodyBytes2)).Msg("调整后封面图上传返回错误")
				// 调整后仍失败，尝试从视频第一帧截取为封面再重试
				if videoPath != "" {
					logger.Warn().Str("video_path", videoPath).Msg("尝试从视频第一帧生成封面并重试上传")
					framePath, frameCT, ferr := extractCoverFromVideo(ctx, videoPath)
					if ferr == nil {
						if img, r := os.ReadFile(framePath); r == nil {
							data := base64.StdEncoding.EncodeToString(img)
							payload := fmt.Sprintf("data:%s;base64,%s", frameCT, data)
							var b bytes.Buffer
							w := multipart.NewWriter(&b)
							if f, e := w.CreateFormField("cover"); e == nil {
								_, _ = f.Write([]byte(payload))
							}
							_ = w.Close()
							req3, e3 := http.NewRequestWithContext(ctx, "POST", u.buildAPIURL("/intl/videoup/web2/cover"), &b)
							if e3 == nil {
								u.setCookies(req3)
								u.setHeaders(req3)
								req3.Header.Set("Content-Type", w.FormDataContentType())
								if resp3, doErr := u.httpClient.Do(req3); doErr == nil {
									defer resp3.Body.Close()
									body3, _ := io.ReadAll(resp3.Body)
									if resp3.StatusCode == 200 {
										var r3obj struct {
											Code    int    `json:"code"`
											Message string `json:"message"`
											Data    struct {
												URL string `json:"url"`
											} `json:"data"`
										}
										if json.Unmarshal(body3, &r3obj) == nil && r3obj.Code == 0 {
											logger.Info().Str("cover_url", r3obj.Data.URL).Str("frame_path", framePath).Msg("使用视频首帧作为封面上传成功")
											return r3obj.Data.URL, nil
										}
									}
								}
							}
						}
					}
				}
				return "", fmt.Errorf("上传封面图失败（重试）: code=%d, message=%s, response=%s", result2.Code, result2.Message, string(bodyBytes2))
			}
			logger.Info().Str("cover_url", result2.Data.URL).Str("resized_path", resizedPath).Msg("封面图调整后上传成功")
			return result2.Data.URL, nil
		}

		return "", fmt.Errorf("上传封面图失败: code=%d, message=%s, response=%s", result.Code, result.Message, string(bodyBytes))
	}

	return result.Data.URL, nil
}

// upscaleCoverTo1280x720 使用 ffmpeg 将封面图调整为 1280x720（保持比例，居中填充）
// 优先保持原格式（jpg/jpeg/png/webp），否则回退到 jpg。返回输出路径与 content-type。
func upscaleCoverTo1280x720(ctx context.Context, inPath string) (string, string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", "", fmt.Errorf("未检测到 ffmpeg，无法自动调整封面尺寸")
	}
	dir := filepath.Dir(inPath)
	ext := strings.ToLower(filepath.Ext(inPath))
	outExt := ext
	contentType := "image/jpeg"
	switch ext {
	case ".jpg", ".jpeg":
		outExt = ".jpg"
		contentType = "image/jpeg"
	case ".png":
		outExt = ".png"
		contentType = "image/png"
	case ".webp":
		outExt = ".webp"
		contentType = "image/webp"
	default:
		outExt = ".jpg"
		contentType = "image/jpeg"
	}
	outPath := filepath.Join(dir, "cover_1280x720"+outExt)
	// 保持比例缩放，pad 到 16:9，转为 jpeg
	// 说明：scale 阶段以较短边为准，pad 居中补边
	vf := "scale='if(gt(a,16/9),1280,-1)':'if(gt(a,16/9),-1,720)',pad=1280:720:(ow-iw)/2:(oh-ih)/2:color=black,format=yuv420p"
	args := []string{
		"-y", "-i", inPath,
		"-vf", vf,
		"-frames:v", "1",
		outPath,
	}
	// 根据输出类型设置质量参数
	switch outExt {
	case ".jpg":
		args = append([]string{"-y", "-i", inPath, "-vf", vf, "-frames:v", "1", "-q:v", "2"}, outPath)[0:]
	case ".png":
		args = append([]string{"-y", "-i", inPath, "-vf", vf, "-frames:v", "1"}, outPath)[0:]
	case ".webp":
		// 质量参数，若不支持会被 ffmpeg 忽略
		args = append([]string{"-y", "-i", inPath, "-vf", vf, "-frames:v", "1", "-quality", "85"}, outPath)[0:]
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		// 若保持原格式失败，回退到 jpg 再试一次
		if outExt != ".jpg" {
			outExt = ".jpg"
			contentType = "image/jpeg"
			outPath = filepath.Join(dir, "cover_1280x720.jpg")
			args = []string{"-y", "-i", inPath, "-vf", vf, "-frames:v", "1", "-q:v", "2", outPath}
			if out2, err2 := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err2 != nil {
				return "", "", fmt.Errorf("ffmpeg 调整封面失败: %w, output=%s; fallback=%v, out2=%s", err, string(out), err2, string(out2))
			}
		} else {
			return "", "", fmt.Errorf("ffmpeg 调整封面失败: %w, output=%s", err, string(out))
		}
	}
	return outPath, contentType, nil
}

// extractCoverFromVideo 从视频第一帧生成 1280x720 的封面（带 padding），输出 jpeg
func extractCoverFromVideo(ctx context.Context, videoPath string) (string, string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", "", fmt.Errorf("未检测到 ffmpeg，无法从视频截取封面")
	}
	dir := filepath.Dir(videoPath)
	outPath := filepath.Join(dir, "cover_from_frame_1280x720.jpg")
	vf := "scale='if(gt(a,16/9),1280,-1)':'if(gt(a,16/9),-1,720)',pad=1280:720:(ow-iw)/2:(oh-ih)/2:color=black,format=yuv420p"
	args := []string{
		"-y",
		"-ss", "0",
		"-i", videoPath,
		"-vf", vf,
		"-frames:v", "1",
		"-q:v", "2",
		outPath,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("ffmpeg 截帧失败: %w, output=%s", err, string(out))
	}
	return outPath, "image/jpeg", nil
}

// publishVideo 发布视频
func (u *httpUploader) publishVideo(ctx context.Context, filename, title, coverURL, subtitleURL, desc string) (string, error) {
	apiURL := u.buildAPIURL("/intl/videoup/web2/add")

	// 构建发布数据
	publishData := map[string]interface{}{
		"title":            title,
		"cover":            coverURL,
		"desc":             desc,
		"no_reprint":       true,
		"filename":         filename,
		"playlist_id":      "",
		"from_spmid":       "333.1011",
		"copyright":        1,
		"tag":              "",
		"subtitle_id":      nil, // 即使没有字幕，也需要设置为 null
		"subtitle_lang_id": nil, // 即使没有字幕，也需要设置为 null
	}

	// 只有当 subtitleURL 不为空时才添加字幕相关字段
	if subtitleURL != "" {
		publishData["subtitle_url"] = subtitleURL
		publishData["subtitle_lang_id"] = 3 // 有字幕时设置为 3（英语）
	}

	// 记录发布参数（用于调试）
	logger.Info().
		Str("filename", filename).
		Str("title", previewForLog(title, 50)).
		Str("cover", coverURL).
		Str("subtitle_url", subtitleURL).
		Bool("has_subtitle", subtitleURL != "").
		Str("api_url", apiURL).
		Msg("准备发布视频")

	jsonData, err := json.Marshal(publishData)
	if err != nil {
		return "", fmt.Errorf("序列化发布数据失败: %w", err)
	}

	// 记录请求体（用于调试，仅前1000字符）
	requestPreview := string(jsonData)
	if len(requestPreview) > 1000 {
		requestPreview = requestPreview[:1000] + "..."
	}
	logger.Info().Str("request_body", requestPreview).Msg("发布视频请求体（预览）")

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", err
	}

	u.setCookies(req)
	u.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	// 打印发布请求的头与 URL
	logger.Info().
		Str("method", req.Method).
		Str("url", req.URL.String()).
		Str("content_type", req.Header.Get("Content-Type")).
		Str("referer", req.Header.Get("Referer")).
		Str("origin", req.Header.Get("Origin")).
		Str("cookie_header", maskCookieHeader(req.Header.Get("Cookie"))).
		Msg("发布视频请求详情")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("发布视频失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体以便调试
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取发布响应失败: %w", err)
	}

	// 打印完整的 HTTP 响应信息
	logger.Info().
		Int("http_status_code", resp.StatusCode).
		Str("http_status", resp.Status).
		Str("response_body", string(bodyBytes)).
		Msg("发布视频 HTTP 响应")

	if resp.StatusCode != 200 {
		logger.Error().
			Int("status_code", resp.StatusCode).
			Str("status", resp.Status).
			Str("response", string(bodyBytes)).
			Msg("发布视频返回非200状态码")
		return "", fmt.Errorf("发布视频失败: HTTP %d (%s), 响应: %s", resp.StatusCode, resp.Status, string(bodyBytes))
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AID string `json:"aid"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		logger.Error().
			Err(err).
			Str("response", string(bodyBytes)).
			Msg("解析发布响应失败")
		return "", fmt.Errorf("解析发布响应失败: %w, 响应: %s", err, string(bodyBytes))
	}

	if result.Code != 0 {
		// 如果 message 为空或只有 "--"，使用 code 作为错误信息
		errorMsg := result.Message
		if errorMsg == "" || errorMsg == "--" {
			errorMsg = fmt.Sprintf("错误代码: %d", result.Code)
		}

		// 打印完整的 HTTP 响应和请求信息以便排查问题
		logger.Error().
			Int("http_status_code", resp.StatusCode).
			Str("http_status", resp.Status).
			Int("api_code", result.Code).
			Str("api_message", result.Message).
			Str("request_body", string(jsonData)).
			Str("response_body", string(bodyBytes)).
			Str("api_url", apiURL).
			Msg("发布视频返回错误，完整 HTTP 响应和请求体已记录")

		return "", fmt.Errorf(
			"发布视频失败: HTTP %d (%s), API code=%d, message=%s, api_url=%s, response_preview=%s, request_preview=%s, filename=%s, cover=%s, subtitle_url=%s",
			resp.StatusCode, resp.Status, result.Code, errorMsg, apiURL,
			previewForLog(string(bodyBytes), 1000),
			previewForLog(string(jsonData), 1000),
			filename, coverURL, subtitleURL,
		)
	}

	logger.Info().Str("aid", result.Data.AID).Msg("视频发布成功")
	return result.Data.AID, nil
}

// validateRequiredFiles 在开始上传前检查所需文件是否存在
// 返回实际找到的视频文件路径
func (u *httpUploader) validateRequiredFiles(videoPath string, subtitlePaths []string, videoDir string) (string, error) {
	// 1. 检查视频文件（必需）
	// 先尝试直接使用提供的路径
	actualVideoPath := videoPath
	if _, err := os.Stat(videoPath); err != nil {
		// 如果文件不存在，尝试在 videoDir 中查找视频文件（可能是文件名不完全匹配）
		if videoDir != "" {
			// 查找 videoDir 中的所有视频文件
			entries, readErr := os.ReadDir(videoDir)
			if readErr == nil {
				for _, entry := range entries {
					if !entry.IsDir() && (strings.HasSuffix(strings.ToLower(entry.Name()), ".mp4") ||
						strings.HasSuffix(strings.ToLower(entry.Name()), ".mkv") ||
						strings.HasSuffix(strings.ToLower(entry.Name()), ".webm")) {
						// 找到视频文件，使用实际的文件名
						foundPath := filepath.Join(videoDir, entry.Name())
						logger.Info().
							Str("original_path", videoPath).
							Str("found_path", foundPath).
							Msg("在目录中找到视频文件（文件名可能不完全匹配）")
						actualVideoPath = foundPath
						break
					}
				}
			}
		}
		// 再次检查
		if _, err := os.Stat(actualVideoPath); err != nil {
			return "", fmt.Errorf("视频文件不存在: %s (尝试查找后: %s), 错误: %w", videoPath, actualVideoPath, err)
		}
	}
	logger.Info().Str("video_path", actualVideoPath).Msg("✓ 视频文件存在")

	// 2. 检查字幕文件（如果提供了路径）
	for i, subtitlePath := range subtitlePaths {
		subtitlePaths[i] = u.cleanPath(subtitlePath)
		if _, err := os.Stat(subtitlePaths[i]); err != nil {
			return "", fmt.Errorf("字幕文件不存在: %s, 错误: %w", subtitlePaths[i], err)
		}
		logger.Debug().Str("subtitle_path", subtitlePaths[i]).Msg("✓ 字幕文件存在")
	}
	if len(subtitlePaths) > 0 {
		logger.Info().Int("count", len(subtitlePaths)).Msg("✓ 所有字幕文件存在")
	}

	// 3. 检查封面图（必需）
	hasCover := false
	// 3.1 优先与视频同名的 .jpg（yt-dlp --convert-thumbnails 生成）
	base := strings.TrimSuffix(actualVideoPath, filepath.Ext(actualVideoPath))
	sameName := base + ".jpg"
	if _, err := os.Stat(sameName); err == nil {
		hasCover = true
		logger.Info().Str("cover_path", sameName).Msg("✓ 封面图文件存在（与视频同名 .jpg）")
	}
	// 3.2 其次 cover.{jpg|jpeg|png|webp|gif}
	if !hasCover {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
			cp := filepath.Join(videoDir, "cover"+ext)
			if _, err := os.Stat(cp); err == nil {
				hasCover = true
				logger.Info().Str("cover_path", cp).Msg("✓ 封面图文件存在")
				break
			}
		}
	}
	// 3.3 最后 thumbnail.jpg
	if !hasCover {
		thumbnailPath := filepath.Join(videoDir, "thumbnail.jpg")
		if _, err := os.Stat(thumbnailPath); err == nil {
			hasCover = true
			logger.Info().Str("thumbnail_path", thumbnailPath).Msg("✓ 封面图文件存在 (thumbnail.jpg)")
		}
	}
	if !hasCover {
		return "", fmt.Errorf("未找到封面图文件：需要与视频同名 .jpg，或 cover.{jpg|jpeg|png|webp|gif}，或 thumbnail.jpg")
	}

	logger.Info().Msg("文件检查完成，所有必需文件都存在，可以开始上传")
	return actualVideoPath, nil
}

// generateFilename 生成上传文件名
func generateFilename(originalName string) string {
	// B站文件名格式：n{date}ad{random}.mp4
	// 例如：n251209ad16g3917krfoey36fea2g002.mp4
	timestamp := time.Now().Format("060102") // YYMMDD
	random := fmt.Sprintf("%d", time.Now().UnixNano()%1000000000)
	ext := filepath.Ext(originalName)
	if ext == "" {
		ext = ".mp4"
	}
	return fmt.Sprintf("n%sad%s%s", timestamp, random, ext)
}

// cleanPath 清理路径中的转义字符
// 处理 shell 转义，例如：\  -> 空格，\# -> #，\\ -> \
func (u *httpUploader) cleanPath(path string) string {
	// 替换常见的转义字符
	// \  -> 空格
	// \# -> #
	// \" -> "
	// \' -> '
	cleaned := strings.ReplaceAll(path, "\\ ", " ")
	cleaned = strings.ReplaceAll(cleaned, "\\#", "#")
	cleaned = strings.ReplaceAll(cleaned, "\\\"", "\"")
	cleaned = strings.ReplaceAll(cleaned, "\\'", "'")

	return cleaned
}
