package subtitle

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BilibiliSubtitleEntry B站字幕条目格式
type BilibiliSubtitleEntry struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ConvertSRTToBilibiliJSON 将 SRT 字幕文件转换为 B站 JSON 格式
func ConvertSRTToBilibiliJSON(srtPath string) ([]BilibiliSubtitleEntry, error) {
	file, err := os.Open(srtPath)
	if err != nil {
		return nil, fmt.Errorf("打开 SRT 文件失败: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var entries []BilibiliSubtitleEntry

	// 时间戳正则：00:00:00,000 --> 00:00:00,000
	timePattern := regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2}),(\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2}),(\d{3})`)

	var subtitleIndex int
	var currentText strings.Builder
	var timestamp string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// 空行表示一个字幕条目结束
		if line == "" {
			if currentText.Len() > 0 {
				// 生成 ID：timestamp-index
				entryID := fmt.Sprintf("%s-%d", timestamp, subtitleIndex)
				entries = append(entries, BilibiliSubtitleEntry{
					ID:   entryID,
					Text: strings.TrimSpace(currentText.String()),
				})
				currentText.Reset()
				subtitleIndex++
			}
			continue
		}

		// 检查是否是时间戳行
		if timePattern.MatchString(line) {
			// 提取时间戳（用于生成 ID）
			matches := timePattern.FindStringSubmatch(line)
			if len(matches) >= 5 {
				// 使用开始时间作为 timestamp（转换为毫秒时间戳）
				timestamp = extractTimestamp(matches[1:5])
			}
			continue
		}

		// 检查是否是序号行（跳过）
		if _, err := strconv.Atoi(line); err == nil {
			continue
		}

		// 字幕文本
		if currentText.Len() > 0 {
			currentText.WriteString(" ")
		}
		currentText.WriteString(line)
	}

	// 处理最后一个条目
	if currentText.Len() > 0 {
		entryID := fmt.Sprintf("%s-%d", timestamp, subtitleIndex)
		entries = append(entries, BilibiliSubtitleEntry{
			ID:   entryID,
			Text: strings.TrimSpace(currentText.String()),
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 SRT 文件失败: %w", err)
	}

	return entries, nil
}

// extractTimestamp 从时间字符串提取时间戳（毫秒）
func extractTimestamp(timeParts []string) string {
	if len(timeParts) < 4 {
		return fmt.Sprintf("%d", time.Now().UnixMilli())
	}

	hours, _ := strconv.Atoi(timeParts[0])
	minutes, _ := strconv.Atoi(timeParts[1])
	seconds, _ := strconv.Atoi(timeParts[2])
	milliseconds, _ := strconv.Atoi(timeParts[3])

	totalMs := int64(hours)*3600000 + int64(minutes)*60000 + int64(seconds)*1000 + int64(milliseconds)
	return fmt.Sprintf("%d", totalMs)
}

