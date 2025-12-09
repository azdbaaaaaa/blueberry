# B站 Cookies 配置指南

## 问题说明

B站上传功能需要在服务器上运行，但服务器通常没有图形界面，无法使用浏览器自动化进行登录。为了解决这个问题，我们可以使用 cookies 文件来实现自动登录。

## 配置方式

### 方式一：使用 Cookies 文件（推荐，适用于服务器环境）

1. **导出 Cookies 文件**

   **方法 A：使用浏览器扩展（最简单）**
   
   - Chrome/Edge: 安装扩展 "Get cookies.txt LOCALLY"
     - 访问：https://chrome.google.com/webstore/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc
     - 打开 B站（bilibili.tv）并登录
     - 点击扩展图标，选择 "Export" -> "Netscape format"
     - 保存为 `bilibili_cookies.txt`
   
   - Firefox: 安装扩展 "cookies.txt"
     - 访问：https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/
     - 打开 B站并登录
     - 点击扩展图标，选择 "Export"
     - 保存为 `bilibili_cookies.txt`

   **方法 B：使用 EditThisCookie 扩展（Chrome/Edge）**
   
   - 安装 EditThisCookie 扩展
     - Chrome: https://chrome.google.com/webstore/detail/editthiscookie/fngmhnnpilhplaeedifhccceomclgfbg
     - Edge: https://microsoftedge.microsoft.com/addons/detail/editthiscookie/fngmhnnpilhplaeedifhccceomclgfbg
   - 打开 B站并登录
   - 点击扩展图标，选择 "Export" -> "Netscape format"
   - 保存为 `bilibili_cookies.txt`

2. **配置 config.yaml**

   ```yaml
   bilibili:
     base_url: "https://www.bilibili.tv/en/"
     # 使用 cookies 文件（推荐用于服务器环境）
     cookies_file: "./bilibili_cookies.txt"
     
     # 注释掉 cookies_from_browser（服务器上可能没有浏览器）
     # cookies_from_browser: "chrome"
   ```

3. **将 cookies.txt 上传到服务器**

   ```bash
   # 使用 scp 上传
   scp bilibili_cookies.txt user@server:/path/to/blueberry/
   ```

### 方式二：从浏览器导入（仅适用于本地开发环境）

```yaml
bilibili:
  base_url: "https://www.bilibili.tv/en/"
  # 支持的浏览器: chrome, firefox, safari, edge, opera, brave, vivaldi
  cookies_from_browser: "chrome"
  
  # 注释掉 cookies_file
  # cookies_file: ""
```

## Cookies 文件格式

工具支持两种格式：

1. **Netscape 格式**（.txt 文件）
   ```
   # Netscape HTTP Cookie File
   .bilibili.tv	TRUE	/	FALSE	1735689600	SESSDATA	abc123...
   .bilibili.tv	TRUE	/	TRUE	1735689600	DedeUserID	123456
   ```

2. **JSON 格式**（.json 文件）
   ```json
   [
     {
       "name": "SESSDATA",
       "value": "abc123...",
       "domain": ".bilibili.tv",
       "path": "/",
       "expirationDate": 1735689600,
       "httpOnly": false,
       "secure": true
     }
   ]
   ```

## 注意事项

1. **Cookies 有效期**：
   - B站 cookies 的有效期通常为 **1-2 年**（从导出时算起）
   - 但实际有效期可能更短，因为：
     - B站可能会定期更新安全策略
     - 某些 cookies 可能只有几周的有效期
     - 如果长时间不使用，B站可能会要求重新登录
   - **建议**：如果遇到登录失败，首先尝试重新导出 cookies
   - **检查方法**：查看 cookies 文件中的时间戳（Unix 时间戳），如果接近当前时间，可能需要更新

2. **隐私安全**：Cookies 文件包含登录信息，请妥善保管，不要提交到版本控制系统

3. **服务器环境**：Linux 服务器通常没有图形界面和浏览器，必须使用 cookies 文件方式

4. **文件格式**：支持 Netscape 格式（.txt）和 JSON 格式

5. **域名匹配**：确保 cookies 文件中的域名与 `base_url` 匹配（例如 `.bilibili.tv`）

## 故障排除

### 错误：登录失败

- **原因**：Cookies 已过期或无效
- **解决**：重新导出 cookies 文件

### 错误：找不到 cookies 文件

- 检查 `cookies_file` 路径是否正确
- 确保文件已上传到服务器
- 检查文件权限（确保可读）

### 错误：cookies 加载失败

- 检查文件格式是否正确
- 确保文件不为空
- 查看日志了解具体错误信息

## 工作流程

1. **本地导出 cookies**：在本地浏览器中登录 B站，使用扩展导出 cookies
2. **上传到服务器**：将 cookies 文件上传到服务器
3. **配置 config.yaml**：设置 `cookies_file` 路径
4. **运行工具**：工具会自动加载 cookies 并尝试登录
5. **验证登录**：工具会自动检测登录状态，如果已登录则跳过登录步骤

## 相关文档

- [YouTube Cookies 配置指南](./COOKIES_SETUP.md)
- [FFmpeg 安装指南](./FFMPEG_SETUP.md)

