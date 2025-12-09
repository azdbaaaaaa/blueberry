# B站海外站上传 API 分析

基于 HAR 文件分析的上传流程，可以使用纯 HTTP 请求替代浏览器自动化。

## 关键 API 端点

### 1. 视频上传流程

#### 1.1 初始化上传
```
POST https://upos-cs-upcdntxa.bilivideo.com/iupever/{filename}.mp4?uploads&output=json
```
- 返回 `uploadId` 用于后续分块上传

#### 1.2 分块上传
```
PUT https://upos-cs-upcdntxa.bilivideo.com/iupever/{filename}.mp4?partNumber={partNumber}&uploadId={uploadId}&chunk={chunk}&chunks={chunks}&size={size}&start={start}&end={end}&total={total}
```
- 每个分块约 22MB
- 需要按顺序上传所有分块

#### 1.3 完成上传
```
POST https://api.bilibili.tv/intl/videoup/web2/uploading?lang_id=3&platform=web&lang=en_US&s_locale=en_US&timezone=GMT%2B08:00&csrf={csrf}
```
- Body: `filename={filename}`
- 确认视频上传完成

### 2. 字幕上传流程

#### 2.1 字幕合法性检查
```
POST https://api.bilibili.tv/intl/videoup/web2/subtitle/multi-check?lang_id=3&platform=web&lang=en_US&s_locale=en_US&timezone=GMT%2B08:00&csrf={csrf}
```
- Body (JSON):
```json
{
  "subtitles": [
    {"id": "timestamp-0", "text": "字幕文本1"},
    {"id": "timestamp-1", "text": "字幕文本2"},
    ...
  ]
}
```
- 响应：`{"code":0,"message":"0","ttl":1,"data":{"hit_ids":[]}}`

#### 2.2 字幕文件上传
- 上传到 OSS：`ali-sgp-intl-common-p.oss-accelerate.aliyuncs.com`
- 字幕 URL 格式：`ugc/subtitle/{timestamp}_{hash}_subtitle-{timestamp}.json`

### 3. 封面图上传

```
POST https://api.bilibili.tv/intl/videoup/web2/cover?lang_id=3&platform=web&lang=en_US&s_locale=en_US&timezone=GMT%2B08:00&csrf={csrf}
```
- Content-Type: `multipart/form-data`
- Body: `cover=data:image/jpeg;base64,{base64_encoded_image}`
- 响应：`{"code":0,"message":"0","ttl":1,"data":{"url":"https://p.bstarstatic.com/ugc/..."}}`

### 4. 发布视频

```
POST https://api.bilibili.tv/intl/videoup/web2/add?lang_id=3&platform=web&lang=en_US&s_locale=en_US&timezone=GMT%2B08:00&csrf={csrf}
```
- Body (JSON):
```json
{
  "title": "视频标题",
  "cover": "https://p.bstarstatic.com/ugc/...",
  "desc": "",
  "no_reprint": true,
  "filename": "{filename}",
  "playlist_id": "",
  "subtitle_id": null,
  "subtitle_url": "ugc/subtitle/...",
  "subtitle_lang_id": 3,
  "from_spmid": "333.1011",
  "copyright": 1,
  "tag": ""
}
```
- 响应：`{"code":0,"message":"0","ttl":1,"data":{"aid":"4797773015554048"}}`

## 必需参数

### Query 参数（所有 API）
- `lang_id=3` - 语言 ID（3 可能是英语）
- `platform=web` - 平台
- `lang=en_US` - 语言代码
- `s_locale=en_US` - 区域设置
- `timezone=GMT%2B08:00` - 时区
- `csrf={csrf_token}` - CSRF token（从 cookies 中获取）

### Headers
- `Cookie` - 包含所有认证 cookies
- `Content-Type` - 根据请求类型设置
- `Referer` - `https://studio.bilibili.tv/archive/new`
- `Origin` - `https://studio.bilibili.tv`

## 实现优势

1. **无需浏览器**：纯 HTTP 请求，适合服务器环境
2. **更高效**：不需要启动浏览器进程
3. **更稳定**：不依赖浏览器自动化，减少失败率
4. **更易调试**：可以直接查看 HTTP 请求和响应

## 注意事项

1. **CSRF Token**：需要从 cookies 中提取 `csrf` 字段
2. **Cookies 有效性**：确保 cookies 未过期
3. **分块上传**：大文件需要分块上传，每块约 22MB
4. **字幕格式**：需要将 SRT 转换为 B站要求的 JSON 格式
5. **封面图格式**：需要转换为 base64 编码的 JPEG

