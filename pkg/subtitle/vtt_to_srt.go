package subtitle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConvertVTTToSRT 将 VTT 文件转换为 SRT 格式
// 如果转换成功，返回 SRT 文件路径；如果失败，返回错误
func ConvertVTTToSRT(vttPath string) (string, error) {
	// 读取 VTT 文件
	vttFile, err := os.Open(vttPath)
	if err != nil {
		return "", fmt.Errorf("打开 VTT 文件失败: %w", err)
	}
	defer vttFile.Close()

	// 生成 SRT 文件路径（替换扩展名）
	srtPath := strings.TrimSuffix(vttPath, filepath.Ext(vttPath)) + ".srt"

	// 创建 SRT 文件
	srtFile, err := os.Create(srtPath)
	if err != nil {
		return "", fmt.Errorf("创建 SRT 文件失败: %w", err)
	}
	defer srtFile.Close()

	scanner := bufio.NewScanner(vttFile)
	writer := bufio.NewWriter(srtFile)
	defer writer.Flush()

	subtitleIndex := 1
	inSubtitle := false
	var currentSubtitle strings.Builder
	var timeLine string

	// 正则表达式匹配时间戳（VTT 格式：00:00:00.000 --> 00:00:00.000）
	timePattern := regexp.MustCompile(`(\d{2}:\d{2}:\d{2})\.(\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2})\.(\d{3})`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// 跳过 WEBVTT 头部和空行
		if line == "WEBVTT" || line == "" {
			continue
		}

		// 检查是否是时间戳行
		if timePattern.MatchString(line) {
			// 如果之前有字幕内容，先写入
			if inSubtitle && currentSubtitle.Len() > 0 {
				// 转换时间格式并写入
				convertedTime := convertVTTTimeToSRT(timeLine)
				writer.WriteString(fmt.Sprintf("%d\n", subtitleIndex))
				writer.WriteString(convertedTime + "\n")
				writer.WriteString(currentSubtitle.String() + "\n\n")
				subtitleIndex++
				currentSubtitle.Reset()
			}
			timeLine = line
			inSubtitle = true
			continue
		}

		// 如果是字幕内容行
		if inSubtitle {
			// 移除 VTT 的样式标签（如 <c>, </c>, <v>, </v> 等）
			cleanLine := removeVTTTags(line)
			if cleanLine != "" {
				if currentSubtitle.Len() > 0 {
					currentSubtitle.WriteString("\n")
				}
				currentSubtitle.WriteString(cleanLine)
			}
		}
	}

	// 写入最后一个字幕
	if inSubtitle && currentSubtitle.Len() > 0 {
		convertedTime := convertVTTTimeToSRT(timeLine)
		writer.WriteString(fmt.Sprintf("%d\n", subtitleIndex))
		writer.WriteString(convertedTime + "\n")
		writer.WriteString(currentSubtitle.String() + "\n\n")
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取 VTT 文件失败: %w", err)
	}

	return srtPath, nil
}

// convertVTTTimeToSRT 将 VTT 时间格式转换为 SRT 格式
// VTT: 00:00:00.000 --> 00:00:00.000
// SRT: 00:00:00,000 --> 00:00:00,000
func convertVTTTimeToSRT(vttTime string) string {
	// 将点号替换为逗号
	srtTime := strings.ReplaceAll(vttTime, ".", ",")
	return srtTime
}

// removeVTTTags 移除 VTT 格式的标签
func removeVTTTags(line string) string {
	// 移除常见的 VTT 标签：<c>, </c>, <v>, </v>, <i>, </i>, <b>, </b>, <u>, </u>
	tagPattern := regexp.MustCompile(`</?[cviub]>|</?[cviub]\s+[^>]*>`)
	cleaned := tagPattern.ReplaceAllString(line, "")

	// 移除颜色标签：<c.color> 或 <c.colorName>
	colorPattern := regexp.MustCompile(`<c[^>]*>|</c>`)
	cleaned = colorPattern.ReplaceAllString(cleaned, "")

	// 移除位置标签：<v name>
	voicePattern := regexp.MustCompile(`<v[^>]*>`)
	cleaned = voicePattern.ReplaceAllString(cleaned, "")

	// 清理多余的空格
	cleaned = strings.TrimSpace(cleaned)
	cleaned = regexp.MustCompile(`\s+`).ReplaceAllString(cleaned, " ")

	return cleaned
}

// ConvertVTTFilesInDir 转换目录中的所有 VTT 文件为 SRT
func ConvertVTTFilesInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}

	var convertedFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if strings.HasSuffix(strings.ToLower(entry.Name()), ".vtt") {
			vttPath := filepath.Join(dir, entry.Name())
			srtPath, err := ConvertVTTToSRT(vttPath)
			if err != nil {
				// 记录错误但继续处理其他文件
				fmt.Printf("转换 %s 失败: %v\n", vttPath, err)
				continue
			}
			convertedFiles = append(convertedFiles, srtPath)
		}
	}

	return convertedFiles, nil
}

