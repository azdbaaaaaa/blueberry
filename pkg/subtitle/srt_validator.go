package subtitle

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// SRTEntry 表示一个 SRT 字幕条目
type SRTEntry struct {
	Index     int    // 序号
	StartTime string // 开始时间 (HH:MM:SS,mmm)
	EndTime   string // 结束时间 (HH:MM:SS,mmm)
	Text      string // 字幕文本
	ID        string // 生成的 ID（用于匹配 B站返回的 hit_ids）
}

// ParseSRT 解析 SRT 文件，返回所有条目
func ParseSRT(srtPath string) ([]SRTEntry, error) {
	file, err := os.Open(srtPath)
	if err != nil {
		return nil, fmt.Errorf("打开 SRT 文件失败: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var entries []SRTEntry

	// 时间戳正则：00:00:00,000 --> 00:00:00,000
	timePattern := regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2}),(\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2}),(\d{3})`)

	var currentEntry SRTEntry
	var currentText strings.Builder
	var inEntry bool

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// 空行表示一个字幕条目结束
		if line == "" {
			if inEntry && currentText.Len() > 0 {
				currentEntry.Text = strings.TrimSpace(currentText.String())
				// 生成 ID（与 ConvertSRTToBilibiliJSON 中的格式一致）
				currentEntry.ID = fmt.Sprintf("%s-%d", extractTimestampFromTime(currentEntry.StartTime), len(entries))
				entries = append(entries, currentEntry)
				currentText.Reset()
				inEntry = false
			}
			continue
		}

		// 检查是否是序号行
		if index, err := strconv.Atoi(line); err == nil {
			currentEntry = SRTEntry{Index: index}
			inEntry = true
			continue
		}

		// 检查是否是时间戳行
		if timePattern.MatchString(line) {
			matches := timePattern.FindStringSubmatch(line)
			if len(matches) >= 9 {
				currentEntry.StartTime = fmt.Sprintf("%s:%s:%s,%s", matches[1], matches[2], matches[3], matches[4])
				currentEntry.EndTime = fmt.Sprintf("%s:%s:%s,%s", matches[5], matches[6], matches[7], matches[8])
			}
			continue
		}

		// 字幕文本
		if inEntry {
			if currentText.Len() > 0 {
				currentText.WriteString(" ")
			}
			currentText.WriteString(line)
		}
	}

	// 处理最后一个条目
	if inEntry && currentText.Len() > 0 {
		currentEntry.Text = strings.TrimSpace(currentText.String())
		currentEntry.ID = fmt.Sprintf("%s-%d", extractTimestampFromTime(currentEntry.StartTime), len(entries))
		entries = append(entries, currentEntry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 SRT 文件失败: %w", err)
	}

	return entries, nil
}

// WriteSRT 将 SRT 条目写入文件
func WriteSRT(entries []SRTEntry, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("创建 SRT 文件失败: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	for i, entry := range entries {
		// 写入序号（从1开始）
		if _, err := writer.WriteString(fmt.Sprintf("%d\n", i+1)); err != nil {
			return fmt.Errorf("写入序号失败: %w", err)
		}

		// 写入时间戳
		if _, err := writer.WriteString(fmt.Sprintf("%s --> %s\n", entry.StartTime, entry.EndTime)); err != nil {
			return fmt.Errorf("写入时间戳失败: %w", err)
		}

		// 写入文本（可能有多行）
		textLines := strings.Split(entry.Text, "\n")
		for _, textLine := range textLines {
			if _, err := writer.WriteString(textLine + "\n"); err != nil {
				return fmt.Errorf("写入文本失败: %w", err)
			}
		}

		// 写入空行
		if _, err := writer.WriteString("\n"); err != nil {
			return fmt.Errorf("写入空行失败: %w", err)
		}
	}

	return nil
}

// FilterInvalidEntries 根据 hit_ids 过滤掉不合法的条目
func FilterInvalidEntries(entries []SRTEntry, hitIDs []string) []SRTEntry {
	if len(hitIDs) == 0 {
		return entries
	}

	// 创建 hit_ids 的 map 以便快速查找
	hitIDMap := make(map[string]bool)
	for _, hitID := range hitIDs {
		hitIDMap[hitID] = true
	}

	var filtered []SRTEntry
	var removedCount int

	for _, entry := range entries {
		if hitIDMap[entry.ID] {
			removedCount++
			continue // 跳过不合法的条目
		}
		filtered = append(filtered, entry)
	}

	if removedCount > 0 {
		fmt.Printf("已过滤 %d 个不合法的字幕条目\n", removedCount)
	}

	return filtered
}

// extractTimestampFromTime 从时间字符串提取时间戳（毫秒）
func extractTimestampFromTime(timeStr string) string {
	// 时间格式：HH:MM:SS,mmm
	parts := strings.Split(timeStr, ",")
	if len(parts) != 2 {
		return "0"
	}

	timeParts := strings.Split(parts[0], ":")
	if len(timeParts) != 3 {
		return "0"
	}

	hours, _ := strconv.Atoi(timeParts[0])
	minutes, _ := strconv.Atoi(timeParts[1])
	seconds, _ := strconv.Atoi(timeParts[2])
	milliseconds, _ := strconv.Atoi(parts[1])

	totalMs := int64(hours)*3600000 + int64(minutes)*60000 + int64(seconds)*1000 + int64(milliseconds)
	return fmt.Sprintf("%d", totalMs)
}

