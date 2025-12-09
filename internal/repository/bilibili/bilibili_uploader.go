package bilibili

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"blueberry/internal/config"
	"blueberry/pkg/logger"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type Uploader interface {
	UploadVideo(ctx context.Context, videoPath, videoTitle string, subtitlePaths []string, account config.Account) (*UploadResult, error)
	CheckLoginStatus(ctx context.Context) (bool, error)
}

type UploadResult struct {
	Success bool
	VideoID string
	AID     string
	Error   error
}

type uploader struct {
	baseURL            string
	cookiesFromBrowser string
	cookiesFile        string
}

func NewUploader(baseURL, cookiesFromBrowser, cookiesFile string) Uploader {
	return &uploader{
		baseURL:            baseURL,
		cookiesFromBrowser: cookiesFromBrowser,
		cookiesFile:        cookiesFile,
	}
}

func (u *uploader) UploadVideo(ctx context.Context, videoPath, videoTitle string, subtitlePaths []string, account config.Account) (*UploadResult, error) {
	result := &UploadResult{}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	ctx, cancel = chromedp.NewContext(allocCtx, chromedp.WithLogf(logger.Printf))
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// 加载 cookies（优先使用账号级别的配置，否则使用全局配置）
	cookiesFile := account.CookiesFile
	cookiesFromBrowser := account.CookiesFromBrowser
	if cookiesFile == "" {
		cookiesFile = u.cookiesFile
	}
	if cookiesFromBrowser == "" {
		cookiesFromBrowser = u.cookiesFromBrowser
	}

	// 如果账号有独立的 cookies 配置，使用账号的配置
	if cookiesFile != "" || cookiesFromBrowser != "" {
		if err := u.loadCookiesWithConfig(ctx, cookiesFile, cookiesFromBrowser); err != nil {
			logger.Warn().Err(err).Msg("加载 cookies 失败，将尝试正常登录")
		}
	} else if u.cookiesFile != "" || u.cookiesFromBrowser != "" {
		// 否则使用全局配置
		if err := u.loadCookies(ctx); err != nil {
			logger.Warn().Err(err).Msg("加载 cookies 失败，将尝试正常登录")
		}
	}

	if err := u.login(ctx, account); err != nil {
		result.Error = fmt.Errorf("登录失败: %w", err)
		return result, result.Error
	}

	videoID, err := u.uploadVideoFile(ctx, videoPath, videoTitle, subtitlePaths)
	if err != nil {
		result.Error = fmt.Errorf("上传失败: %w", err)
		return result, result.Error
	}

	result.Success = true
	result.VideoID = videoID
	return result, nil
}

func (u *uploader) login(ctx context.Context, account config.Account) error {
	logger.Info().Str("username", account.Username).Msg("开始登录B站账号")

	// 确定使用的 cookies 配置（优先账号级别，否则全局）
	cookiesFile := account.CookiesFile
	if cookiesFile == "" {
		cookiesFile = u.cookiesFile
	}

	// 先检查是否已经登录（如果使用了 cookies，可能已经登录）
	if loggedIn, _ := u.CheckLoginStatusWithCookies(ctx, cookiesFile); loggedIn {
		logger.Info().Msg("已经登录（可能通过 cookies），跳过登录步骤")
		return nil
	}

	// 如果配置了 cookies 文件，且已经加载，可能不需要登录
	if cookiesFile != "" {
		logger.Info().Str("cookies_file", cookiesFile).Msg("已使用 cookies 文件，如果登录失败可能需要手动登录")
	}

	loginURL := fmt.Sprintf("%s/login", u.baseURL)

	err := chromedp.Run(ctx,
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		// 尝试自动填写用户名和密码
		chromedp.ActionFunc(func(ctx context.Context) error {
			// 查找用户名输入框（可能的选择器）
			usernameSelectors := []string{
				`input[name="username"]`,
				`input[type="text"]`,
				`input[placeholder*="username" i]`,
				`input[placeholder*="email" i]`,
				`input[placeholder*="手机号" i]`,
				`#username`,
				`.username-input`,
			}

			passwordSelectors := []string{
				`input[name="password"]`,
				`input[type="password"]`,
				`#password`,
				`.password-input`,
			}

			loginButtonSelectors := []string{
				`button[type="submit"]`,
				`button:contains("登录")`,
				`button:contains("Login")`,
				`.login-button`,
				`#login-button`,
			}

			var usernameFound, passwordFound bool

			// 尝试填写用户名
			for _, selector := range usernameSelectors {
				if err := chromedp.SendKeys(selector, account.Username, chromedp.ByQuery).Do(ctx); err == nil {
					logger.Info().Str("selector", selector).Msg("已填写用户名")
					usernameFound = true
					chromedp.Sleep(500 * time.Millisecond)
					break
				}
			}

			if !usernameFound {
				logger.Warn().Msg("未找到用户名输入框，可能需要手动填写")
			}

			// 尝试填写密码
			for _, selector := range passwordSelectors {
				if err := chromedp.SendKeys(selector, account.Password, chromedp.ByQuery).Do(ctx); err == nil {
					logger.Info().Str("selector", selector).Msg("已填写密码")
					passwordFound = true
					chromedp.Sleep(500 * time.Millisecond)
					break
				}
			}

			if !passwordFound {
				logger.Warn().Msg("未找到密码输入框，可能需要手动填写")
			}

			// 如果自动填写失败，提示用户手动填写
			if !usernameFound || !passwordFound {
				logger.Info().Msg("自动填写失败，请在浏览器中手动填写用户名和密码")
				logger.Info().Msg("填写完成后，程序将自动检测登录状态")
				// 等待用户手动登录（最多等待5分钟）
				chromedp.Sleep(5 * time.Minute)
			} else {
				// 尝试点击登录按钮
				for _, selector := range loginButtonSelectors {
					if err := chromedp.Click(selector, chromedp.ByQuery).Do(ctx); err == nil {
						logger.Info().Str("selector", selector).Msg("已点击登录按钮")
						chromedp.Sleep(3 * time.Second)
						break
					}
				}
			}

			return nil
		}),
		// 等待登录完成
		chromedp.Sleep(3*time.Second),
	)

	if err != nil {
		return fmt.Errorf("登录过程出错: %w", err)
	}

	// 检测登录状态
	logger.Info().Msg("检测登录状态...")
	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		if loggedIn, _ := u.CheckLoginStatusWithCookies(ctx, cookiesFile); loggedIn {
			logger.Info().Msg("登录成功")
			return nil
		}
		logger.Info().Int("retry", i+1).Int("max", maxRetries).Msg("等待登录完成...")
		chromedp.Sleep(2 * time.Second).Do(ctx)
	}

	logger.Warn().Msg("未检测到登录状态，但继续尝试上传（可能需要手动登录）")
	return nil
}

func (u *uploader) uploadVideoFile(ctx context.Context, videoPath, videoTitle string, subtitlePaths []string) (string, error) {
	logger.Info().Str("video_path", videoPath).Str("title", videoTitle).Msg("开始上传视频")

	uploadURL := fmt.Sprintf("%s/upload", u.baseURL)
	absVideoPath, err := filepath.Abs(videoPath)
	if err != nil {
		return "", fmt.Errorf("获取视频绝对路径失败: %w", err)
	}

	// 准备字幕文件的绝对路径
	absSubtitlePaths := make([]string, 0, len(subtitlePaths))
	for _, subPath := range subtitlePaths {
		absPath, err := filepath.Abs(subPath)
		if err == nil {
			absSubtitlePaths = append(absSubtitlePaths, absPath)
		} else {
			logger.Warn().Str("path", subPath).Err(err).Msg("获取字幕文件绝对路径失败")
		}
	}

	var videoID string
	err = chromedp.Run(ctx,
		chromedp.Navigate(uploadURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
		// 步骤1: 上传视频文件
		chromedp.ActionFunc(func(ctx context.Context) error {
			logger.Info().Msg("查找视频上传输入框...")

			// 尝试多种可能的选择器
			uploadSelectors := []string{
				`input[type="file"]`,
				`input[accept*="video"]`,
				`input[accept*="mp4"]`,
				`input.file-input`,
				`#file-input`,
				`.upload-input`,
			}

			var uploaded bool
			for _, selector := range uploadSelectors {
				if err := chromedp.SetUploadFiles(selector, []string{absVideoPath}, chromedp.ByQuery).Do(ctx); err == nil {
					logger.Info().Str("selector", selector).Msg("视频文件已选择")
					uploaded = true
					chromedp.Sleep(2 * time.Second)
					break
				}
			}

			if !uploaded {
				logger.Warn().Msg("自动选择视频文件失败，请在浏览器中手动选择视频文件")
				logger.Info().Str("path", absVideoPath).Msg("视频文件路径")
			}

			return nil
		}),
		// 等待文件上传和处理
		chromedp.Sleep(5*time.Second),
		// 步骤2: 填写视频标题
		chromedp.ActionFunc(func(ctx context.Context) error {
			logger.Info().Str("title", videoTitle).Msg("尝试填写视频标题...")

			titleSelectors := []string{
				`input[name="title"]`,
				`input[placeholder*="title" i]`,
				`input[placeholder*="标题" i]`,
				`#title`,
				`.title-input`,
				`textarea[name="title"]`,
			}

			var titleFilled bool
			for _, selector := range titleSelectors {
				// 先清空输入框
				chromedp.Clear(selector, chromedp.ByQuery).Do(ctx)
				if err := chromedp.SendKeys(selector, videoTitle, chromedp.ByQuery).Do(ctx); err == nil {
					logger.Info().Str("selector", selector).Msg("已填写视频标题")
					titleFilled = true
					chromedp.Sleep(1 * time.Second)
					break
				}
			}

			if !titleFilled {
				logger.Warn().Msg("未找到标题输入框，可能需要手动填写")
				logger.Info().Str("title", videoTitle).Msg("请使用此标题")
			}

			return nil
		}),
		// 步骤3: 上传字幕文件（如果有）
		chromedp.ActionFunc(func(ctx context.Context) error {
			if len(absSubtitlePaths) == 0 {
				return nil
			}

			logger.Info().Int("count", len(absSubtitlePaths)).Msg("尝试上传字幕文件...")

			// 查找字幕上传输入框
			subtitleSelectors := []string{
				`input[type="file"][accept*="srt"]`,
				`input[type="file"][accept*="subtitle"]`,
				`input.subtitle-input`,
				`#subtitle-input`,
				`input[accept*=".srt"]`,
			}

			// 尝试上传每个字幕文件
			for i, subPath := range absSubtitlePaths {
				var uploaded bool
				for _, selector := range subtitleSelectors {
					if err := chromedp.SetUploadFiles(selector, []string{subPath}, chromedp.ByQuery).Do(ctx); err == nil {
						logger.Info().Str("selector", selector).Str("file", subPath).Msg("字幕文件已上传")
						uploaded = true
						chromedp.Sleep(1 * time.Second)
						break
					}
				}

				if !uploaded {
					logger.Warn().Str("file", subPath).Int("index", i+1).Msg("字幕文件上传失败，可能需要手动上传")
				}
			}

			return nil
		}),
		// 等待处理完成
		chromedp.Sleep(10*time.Second),
		// 步骤4: 尝试获取视频ID
		chromedp.ActionFunc(func(ctx context.Context) error {
			logger.Info().Msg("尝试获取视频ID...")

			// 尝试从URL中获取
			var currentURL string
			if err := chromedp.Evaluate(`window.location.href`, &currentURL).Do(ctx); err == nil {
				logger.Debug().Str("url", currentURL).Msg("当前页面URL")
				// 从URL中提取视频ID（如果在上传成功页面）
				// 例如: https://www.bilibili.tv/en/video/av1234567890
				if id := extractVideoIDFromURL(currentURL); id != "" {
					videoID = id
					logger.Info().Str("video_id", videoID).Msg("从URL获取到视频ID")
					return nil
				}
			}

			// 尝试从页面元素中获取
			idSelectors := []string{
				`[data-video-id]`,
				`[data-aid]`,
				`.video-id`,
				`#video-id`,
			}

			for _, selector := range idSelectors {
				var id string
				if err := chromedp.TextContent(selector, &id, chromedp.ByQuery).Do(ctx); err == nil && id != "" {
					videoID = id
					logger.Info().Str("video_id", videoID).Str("selector", selector).Msg("从页面元素获取到视频ID")
					return nil
				}
			}

			// 尝试从JavaScript变量中获取
			var jsID string
			jsCode := `
				(function() {
					if (window.__INITIAL_STATE__ && window.__INITIAL_STATE__.videoData && window.__INITIAL_STATE__.videoData.aid) {
						return window.__INITIAL_STATE__.videoData.aid.toString();
					}
					if (window.aid) {
						return window.aid.toString();
					}
					return '';
				})()
			`
			if err := chromedp.Evaluate(jsCode, &jsID).Do(ctx); err == nil && jsID != "" {
				videoID = jsID
				logger.Info().Str("video_id", videoID).Msg("从JavaScript变量获取到视频ID")
				return nil
			}

			logger.Warn().Msg("未能自动获取视频ID，可能需要手动获取")
			logger.Info().Msg("上传完成后，请在上传成功页面或视频页面查看视频ID（aid）")
			logger.Info().Msg("然后可以使用 rename 命令重命名字幕文件")

			return nil
		}),
		// 等待用户完成剩余操作（如果有）
		chromedp.Sleep(5*time.Second),
	)

	if err != nil {
		return "", fmt.Errorf("上传过程出错: %w", err)
	}

	if videoID == "" {
		logger.Warn().Msg("未能获取视频ID，上传可能未完成或需要手动操作")
		logger.Info().Msg("如果上传已完成，请手动获取视频ID（aid），然后使用 rename 命令重命名字幕文件")
	} else {
		logger.Info().Str("video_id", videoID).Msg("上传流程完成，已获取视频ID")
	}

	return videoID, nil
}

// extractVideoIDFromURL 从URL中提取视频ID
func extractVideoIDFromURL(url string) string {
	// 可能的URL格式：
	// https://www.bilibili.tv/en/video/av1234567890
	// https://www.bilibili.tv/en/video/BVxxxxxxxxxx
	// https://www.bilibili.tv/en/video/1234567890

	// 这里需要根据实际的B站URL格式来提取
	// 暂时返回空，让调用方处理
	return ""
}

func (u *uploader) CheckLoginStatus(ctx context.Context) (bool, error) {
	return u.CheckLoginStatusWithCookies(ctx, u.cookiesFile)
}

// CheckLoginStatusWithCookies 检查登录状态（使用指定的 cookies 文件）
func (u *uploader) CheckLoginStatusWithCookies(ctx context.Context, cookiesFile string) (bool, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	checkCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	checkCtx, cancel = context.WithTimeout(checkCtx, 30*time.Second)
	defer cancel()

	// 如果配置了 cookies，先加载
	if cookiesFile != "" {
		if err := u.loadCookiesFromFile(checkCtx, cookiesFile); err != nil {
			logger.Warn().Err(err).Msg("加载 cookies 失败")
		}
	}

	var loggedIn bool
	err := chromedp.Run(checkCtx,
		chromedp.Navigate(u.baseURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(`document.querySelector('[data-testid="user-menu"]') !== null`, &loggedIn),
	)

	return loggedIn, err
}

// loadCookies 从文件加载 cookies 到浏览器（使用全局配置）
func (u *uploader) loadCookies(ctx context.Context) error {
	return u.loadCookiesWithConfig(ctx, u.cookiesFile, u.cookiesFromBrowser)
}

// loadCookiesWithConfig 从文件加载 cookies 到浏览器（使用指定配置）
func (u *uploader) loadCookiesWithConfig(ctx context.Context, cookiesFile, cookiesFromBrowser string) error {
	// 优先使用 cookies 文件（服务器环境）
	if cookiesFile != "" {
		return u.loadCookiesFromFile(ctx, cookiesFile)
	}

	// 如果配置了从浏览器导入，chromedp 会自动处理
	// 这里我们只处理文件方式
	return nil
}

// loadCookiesFromFile 从文件加载 cookies（支持 Netscape 格式和 JSON 格式）
func (u *uploader) loadCookiesFromFile(ctx context.Context, cookiesPath string) error {
	// 解析路径
	if !filepath.IsAbs(cookiesPath) {
		if absPath, err := filepath.Abs(cookiesPath); err == nil {
			cookiesPath = absPath
			logger.Info().Str("original", u.cookiesFile).Str("resolved", cookiesPath).Msg("解析 cookies 文件路径")
		}
	}

	// 检查文件是否存在
	fileInfo, err := os.Stat(cookiesPath)
	if err != nil {
		return fmt.Errorf("cookies 文件不存在: %w", err)
	}
	logger.Info().Str("path", cookiesPath).Int64("size", fileInfo.Size()).Msg("加载 cookies 文件")

	// 判断文件格式（通过文件扩展名或内容）
	cookies, err := u.parseCookiesFile(cookiesPath)
	if err != nil {
		return fmt.Errorf("解析 cookies 文件失败: %w", err)
	}

	if len(cookies) == 0 {
		logger.Warn().Msg("cookies 文件为空")
		return nil
	}

	// 启用网络域
	if err := chromedp.Run(ctx, network.Enable()); err != nil {
		return fmt.Errorf("启用网络域失败: %w", err)
	}

	// 设置 cookies
	baseURL, err := u.parseBaseURL(u.baseURL)
	if err != nil {
		return fmt.Errorf("解析 base URL 失败: %w", err)
	}

	for _, cookie := range cookies {
		// 构建 cookie 参数
		cookieParams := network.SetCookie(cookie.Name, cookie.Value).
			WithURL(baseURL).
			WithDomain(cookie.Domain).
			WithPath(cookie.Path).
			WithHTTPOnly(cookie.HttpOnly).
			WithSecure(cookie.Secure)

		// 设置过期时间（如果提供了）
		// chromedp 的 WithExpires 接受 *cdp.TimeSinceEpoch，但我们可以不设置过期时间
		// 浏览器会自动处理 session cookies
		// 如果需要设置过期时间，可以使用 network.TimeSinceEpoch(float64(cookie.Expires))
		// 但为了简化，我们先不设置过期时间，让浏览器自动处理

		err := chromedp.Run(ctx, cookieParams)
		if err != nil {
			logger.Warn().Str("name", cookie.Name).Err(err).Msg("设置 cookie 失败")
			continue
		}
	}

	logger.Info().Int("count", len(cookies)).Msg("已加载 cookies")
	return nil
}

// Cookie 结构体
type Cookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Expires  int64
	HttpOnly bool
	Secure   bool
}

// parseCookiesFile 解析 cookies 文件（支持 Netscape 格式和 JSON 格式）
func (u *uploader) parseCookiesFile(path string) ([]Cookie, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// 尝试解析为 JSON 格式
	var jsonCookies []map[string]interface{}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&jsonCookies); err == nil {
		// JSON 格式
		cookies := make([]Cookie, 0, len(jsonCookies))
		for _, c := range jsonCookies {
			cookie := Cookie{}
			if name, ok := c["name"].(string); ok {
				cookie.Name = name
			}
			if value, ok := c["value"].(string); ok {
				cookie.Value = value
			}
			if domain, ok := c["domain"].(string); ok {
				cookie.Domain = domain
			}
			if path, ok := c["path"].(string); ok {
				cookie.Path = path
			}
			if expires, ok := c["expirationDate"].(float64); ok {
				cookie.Expires = int64(expires)
			}
			if httpOnly, ok := c["httpOnly"].(bool); ok {
				cookie.HttpOnly = httpOnly
			}
			if secure, ok := c["secure"].(bool); ok {
				cookie.Secure = secure
			}
			cookies = append(cookies, cookie)
		}
		return cookies, nil
	}

	// 如果不是 JSON，尝试 Netscape 格式
	file.Seek(0, 0)
	scanner := bufio.NewScanner(file)
	cookies := make([]Cookie, 0)

	// 跳过 Netscape 格式的头部
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "# Netscape HTTP Cookie File") {
			continue
		}

		// Netscape 格式: domain, flag, path, secure, expiration, name, value
		// flag 通常是 TRUE/FALSE，表示是否包含子域名
		// secure 是 TRUE/FALSE，表示是否只在 HTTPS 下发送
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 {
			cookie := Cookie{
				Domain: parts[0],
				Path:   parts[2],
			}

			// secure 标志
			if parts[3] == "TRUE" {
				cookie.Secure = true
			}

			// expiration (Unix timestamp)
			if expires, err := strconv.ParseInt(parts[4], 10, 64); err == nil {
				cookie.Expires = expires
			}

			cookie.Name = parts[5]
			cookie.Value = parts[6]

			// HttpOnly 在 Netscape 格式中通常不包含，默认为 false
			cookie.HttpOnly = false

			cookies = append(cookies, cookie)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return cookies, nil
}

// parseBaseURL 解析 base URL，提取域名
func (u *uploader) parseBaseURL(url string) (string, error) {
	// 简单处理：如果 URL 包含协议，提取域名部分
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		// 提取域名（去除协议和路径）
		parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://"), "/")
		return "https://" + parts[0], nil
	}
	return url, nil
}
