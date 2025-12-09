# YouTube Cookies 配置指南

## 问题说明

YouTube 可能会要求登录验证才能下载某些视频。为了解决这个问题，我们需要使用 cookies。

## 配置方式

### 方式一：使用 Cookies 文件（推荐，适用于服务器环境）

1. **导出 Cookies 文件**

   **方法 A：使用浏览器扩展（最简单）**
   
   - Chrome/Edge: 安装扩展 "Get cookies.txt LOCALLY"
     - 访问：https://chrome.google.com/webstore/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc
     - 打开 YouTube 并登录
     - 点击扩展图标，选择 "Export" -> "Netscape format"
     - 保存为 `cookies.txt`
   
   - Firefox: 安装扩展 "cookies.txt"
     - 访问：https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/
     - 打开 YouTube 并登录
     - 点击扩展图标，选择 "Export"
     - 保存为 `cookies.txt`

   **方法 B：使用 EditThisCookie 扩展（Chrome/Edge）**
   
   - 安装 EditThisCookie 扩展
     - Chrome: https://chrome.google.com/webstore/detail/editthiscookie/fngmhnnpilhplaeedifhccceomclgfbg
     - Edge: https://microsoftedge.microsoft.com/addons/detail/editthiscookie/fngmhnnpilhplaeedifhccceomclgfbg
   - 打开 YouTube 并登录
   - 点击扩展图标，选择 "Export" -> "Netscape format"
   - 保存为 `cookies.txt`

   **方法 C：使用 Firefox 的 cookies.txt 扩展**
   
   - 安装扩展：https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/
   - 打开 YouTube 并登录
   - 点击扩展图标，选择 "Export"
   - 保存为 `cookies.txt`

   **注意**：`yt-dlp` 的 `--cookies` 参数是用于**读取** cookies 文件，不能用于导出。
   要导出 cookies，必须使用浏览器扩展（方法 A、B 或 C）。

2. **配置 config.yaml**

   ```yaml
   youtube:
     # 注释掉 cookies_from_browser（服务器上可能没有浏览器）
     # cookies_from_browser: "chrome"
     
     # 使用 cookies 文件路径（相对路径或绝对路径）
     cookies_file: "./cookies.txt"
   ```

3. **将 cookies.txt 上传到服务器**

   ```bash
   # 使用 scp 上传
   scp cookies.txt user@server:/path/to/blueberry/
   ```

### 方式二：从浏览器导入（仅适用于本地开发环境）

```yaml
youtube:
  # 支持的浏览器: chrome, firefox, safari, edge, opera, brave, vivaldi
  cookies_from_browser: "chrome"
  
  # 注释掉 cookies_file
  # cookies_file: ""
```

## 注意事项

1. **Cookies 有效期**：
   - YouTube cookies 的有效期通常为 **1-2 年**（从导出时算起）
   - 但实际有效期可能更短，因为：
     - YouTube 可能会定期更新安全策略
     - 某些 cookies（如 `__Secure-1PSIDTS`、`__Secure-3PSIDTS`）可能只有几周的有效期
     - 如果长时间不使用，YouTube 可能会要求重新登录
   - **建议**：如果遇到 "Sign in to confirm you're not a bot" 错误，首先尝试重新导出 cookies
   - **检查方法**：查看 cookies.txt 文件中的时间戳（Unix 时间戳），如果接近当前时间，可能需要更新

2. **隐私安全**：Cookies 文件包含登录信息，请妥善保管，不要提交到版本控制系统

3. **服务器环境**：Linux 服务器通常没有图形界面和浏览器，必须使用 cookies 文件方式

4. **文件格式**：支持 Netscape 格式（.txt）和 JSON 格式

## 故障排除

### 错误："Sign in to confirm you're not a bot"

- 原因：Cookies 已过期或无效
- 解决：重新导出 cookies 文件

### 错误："The following content is not available on this app"

- 原因：视频在某些地区不可用，或需要特定的 cookies
- 解决：
  1. 确保使用已登录的 cookies
  2. 尝试使用 VPN 或代理
  3. 检查视频是否真的可用

### 错误：找不到 cookies 文件

- 检查 `cookies_file` 路径是否正确
- 检查文件是否存在且有读取权限
- 使用绝对路径而不是相对路径

## 测试 Cookies

使用以下命令测试 cookies 是否有效：

```bash
yt-dlp --cookies cookies.txt --dump-json https://www.youtube.com/watch?v=VIDEO_ID
```

如果成功返回 JSON 数据，说明 cookies 有效。

