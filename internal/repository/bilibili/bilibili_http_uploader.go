package bilibili

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
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
func (u *httpUploader) UploadVideo(ctx context.Context, videoPath, videoTitle string, subtitlePaths []string, account config.Account) (*UploadResult, error) {
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

	// 2. 先上传字幕（如失败仅警告继续）
	var subtitleURL string
	if len(subtitlePaths) > 0 {
		subtitleURL, err = u.uploadSubtitles(ctx, subtitlePaths)
		if err != nil {
			// 字幕合法性检查失败或上传失败，记录详细错误但继续上传
			// 用户可能需要手动处理不合法的字幕内容
			logger.Warn().
				Err(err).
				Strs("subtitle_paths", subtitlePaths).
				Msg("上传字幕失败，继续上传其他内容（视频和封面图）。如需字幕，请手动检查并修复字幕文件中的不合法内容后重新上传")
		} else {
			logger.Info().Str("subtitle_url", subtitleURL).Msg("字幕上传完成")
		}
	}

	// 3. 上传视频（使用实际找到的文件路径）
	filename, err := u.uploadVideo(ctx, actualVideoPath)
	if err != nil {
		return nil, fmt.Errorf("上传视频失败: %w", err)
	}
	logger.Info().Str("filename", filename).Msg("视频上传完成")

	// 4. 上传封面图
	var coverURL string
	// 优先查找 cover.jpg，如果没有则查找 thumbnail.jpg
	coverPath := filepath.Join(filepath.Dir(videoPath), "cover.jpg")
	if _, err := os.Stat(coverPath); err != nil {
		// 如果 cover.jpg 不存在，尝试使用 thumbnail.jpg
		thumbnailPath := filepath.Join(filepath.Dir(videoPath), "thumbnail.jpg")
		if _, err := os.Stat(thumbnailPath); err == nil {
			coverPath = thumbnailPath
			logger.Debug().Str("path", thumbnailPath).Msg("使用 thumbnail.jpg 作为封面图")
		}
	}

	if _, err := os.Stat(coverPath); err == nil {
		coverURL, err = u.uploadCover(ctx, coverPath)
		if err != nil {
			logger.Warn().Err(err).Msg("上传封面图失败，继续发布")
		} else {
			logger.Info().Str("cover_url", coverURL).Msg("封面图上传完成")
		}
	} else {
		logger.Debug().Msg("未找到封面图文件（cover.jpg 或 thumbnail.jpg），将使用默认封面")
	}

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
	aid, err := u.publishVideo(ctx, publishFilename, videoTitle, coverURL, subtitleURL)
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

	// 1. 初始化上传
	uploadURL := fmt.Sprintf("https://upos-cs-upcdntxa.bilivideo.com/iupever/%s?uploads&output=json", filename)
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

	logger.Info().Str("upload_id", uploadID).Msg("已初始化上传")

	// 2. 分块上传
	chunkSize := int64(22 * 1024 * 1024) // 22MB per chunk
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

		chunkURL := fmt.Sprintf("https://upos-cs-upcdntxa.bilivideo.com/iupever/%s?partNumber=%d&uploadId=%s&chunk=%d&chunks=%d&size=%d&start=%d&end=%d&total=%d",
			filename, partNumber, uploadID, chunk, chunks, end-start, start, end, fileSize)

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

		// 添加重试机制（最多重试3次）
		var resp *http.Response
		maxRetries := 3
		for retry := 0; retry < maxRetries; retry++ {
			resp, err = u.httpClient.Do(req)
			if err == nil {
				break
			}

			if retry < maxRetries-1 {
				waitTime := time.Duration(retry+1) * time.Second
				logger.Warn().
					Int("chunk", partNumber).
					Int("retry", retry+1).
					Dur("wait", waitTime).
					Err(err).
					Msg("分块上传失败，重试中")
				time.Sleep(waitTime)

				// 重新创建请求（因为 body 已经被读取）
				req, _ = http.NewRequestWithContext(ctx, "PUT", chunkURL, bytes.NewReader(chunkData))
				u.setHeaders(req)
				req.Header.Set("Content-Type", "application/octet-stream")
				req.Header.Set("Content-Length", fmt.Sprintf("%d", len(chunkData)))
				// 重新设置 X-Upos-Auth
				if uposAuth != "" {
					req.Header.Set("X-Upos-Auth", uposAuth)
				}
			}
		}

		if err != nil {
			return "", fmt.Errorf("上传分块 %d 失败（已重试 %d 次）: %w", partNumber, maxRetries, err)
		}

		// 检查响应状态码
		if resp.StatusCode != 200 && resp.StatusCode != 204 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			logger.Error().
				Int("chunk", partNumber).
				Int("status_code", resp.StatusCode).
				Str("response", string(bodyBytes)).
				Msg("分块上传返回非200状态码")
			return "", fmt.Errorf("上传分块 %d 失败: HTTP %d, 响应: %s", partNumber, resp.StatusCode, string(bodyBytes))
		}

		resp.Body.Close()

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

	// 3. 完成上传
	completeURL := u.buildAPIURL("/intl/videoup/web2/uploading")
	formData := url.Values{}
	formData.Set("filename", filename)

	req, err = http.NewRequestWithContext(ctx, "POST", completeURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", err
	}

	u.setCookies(req)
	u.setHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err = u.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("完成上传失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("完成上传失败: status %d, body: %s", resp.StatusCode, string(body))
	}

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

	// 从 upos_uri 中提取实际文件名
	actualFilename := filename
	if uposURI, ok := preuploadResp["upos_uri"].(string); ok && uposURI != "" {
		// upos_uri 格式：upos://iupever/n251209ad16g3917krfoey36fea2g002.mp4
		// 提取文件名部分
		if strings.HasPrefix(uposURI, "upos://iupever/") {
			actualFilename = strings.TrimPrefix(uposURI, "upos://iupever/")
			logger.Debug().Str("upos_uri", uposURI).Str("actual_filename", actualFilename).Msg("从 upos_uri 提取文件名")
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

	logger.Info().Str("srt_path", srtPath).Msg("开始上传字幕")

	// 1. 将 SRT 转换为 B站 JSON 格式
	entries, err := subtitle.ConvertSRTToBilibiliJSON(srtPath)
	if err != nil {
		return "", fmt.Errorf("转换字幕格式失败: %w", err)
	}

	// 2. 分批进行合法性检查（避免单次请求过大导致 -400）
	checkURL := u.buildAPIURL("/intl/videoup/web2/subtitle/multi-check")

	// 检查字幕条目是否为空
	if len(entries) == 0 {
		logger.Warn().Msg("字幕条目为空，跳过合法性检查")
		return "", nil
	}

	logger.Info().
		Int("entry_count", len(entries)).
		Msg("开始字幕合法性检查（分批）")
	batchSize := 200
	var allHitIDs []string
	checkFailed := false
	for start := 0; start < len(entries); start += batchSize {
		end := start + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]
		hitIDs, err := u.checkSubtitleBatch(ctx, checkURL, batch)
		if err != nil {
			logger.Warn().
				Err(err).
				Int("batch_start", start).
				Int("batch_end", end).
				Msg("字幕合法性检查分批失败，将跳过校验并继续上传字幕")
			checkFailed = true
			break
		}
		if len(hitIDs) > 0 {
			allHitIDs = append(allHitIDs, hitIDs...)
		}
	}
	if !checkFailed && len(allHitIDs) > 0 {
		hitSet := make(map[string]struct{}, len(allHitIDs))
		for _, id := range allHitIDs {
			hitSet[id] = struct{}{}
		}
		var filtered []subtitle.BilibiliSubtitleEntry
		for _, e := range entries {
			if _, bad := hitSet[e.ID]; !bad {
				filtered = append(filtered, e)
			}
		}
		logger.Warn().
			Int("hit_count", len(allHitIDs)).
			Int("before", len(entries)).
			Int("after", len(filtered)).
			Msg("已过滤不合法字幕条目")
		entries = filtered
	}

	// 3. 获取字幕直传 OSS 的临时凭证
	tokenURL := u.buildAPIURL("/intl/videoup/web2/upload/token?type=subtitle")
	tokenReq, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建字幕上传凭证请求失败: %w", err)
	}
	u.setCookies(tokenReq)
	u.setHeaders(tokenReq)
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

	// 4. 构建字幕 JSON（与 multi-check 相同结构）
	subJSON, err := json.Marshal(map[string]any{"subtitles": entries})
	if err != nil {
		return "", fmt.Errorf("序列化字幕JSON失败: %w", err)
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
func (u *httpUploader) uploadCover(ctx context.Context, coverPath string) (string, error) {
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
		return "", fmt.Errorf("上传封面图失败: code=%d, message=%s", result.Code, result.Message)
	}

	return result.Data.URL, nil
}

// publishVideo 发布视频
func (u *httpUploader) publishVideo(ctx context.Context, filename, title, coverURL, subtitleURL string) (string, error) {
	apiURL := u.buildAPIURL("/intl/videoup/web2/add")

	// 构建发布数据
	publishData := map[string]interface{}{
		"title":            title,
		"cover":            coverURL,
		"desc":             "",
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
	logger.Debug().Str("request_body", requestPreview).Msg("发布视频请求体（预览）")

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", err
	}

	u.setCookies(req)
	u.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

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

		return "", fmt.Errorf("发布视频失败: HTTP %d (%s), API code=%d, message=%s", resp.StatusCode, resp.Status, result.Code, errorMsg)
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

	// 3. 检查封面图（可选，但建议存在）
	coverPath := filepath.Join(videoDir, "cover.jpg")
	thumbnailPath := filepath.Join(videoDir, "thumbnail.jpg")
	hasCover := false

	if _, err := os.Stat(coverPath); err == nil {
		hasCover = true
		logger.Info().Str("cover_path", coverPath).Msg("✓ 封面图文件存在 (cover.jpg)")
	} else if _, err := os.Stat(thumbnailPath); err == nil {
		hasCover = true
		logger.Info().Str("thumbnail_path", thumbnailPath).Msg("✓ 封面图文件存在 (thumbnail.jpg)")
	}

	if !hasCover {
		logger.Warn().
			Str("video_dir", videoDir).
			Msg("⚠ 未找到封面图文件 (cover.jpg 或 thumbnail.jpg)，将使用默认封面")
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
