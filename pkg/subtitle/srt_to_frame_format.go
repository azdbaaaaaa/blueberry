package subtitle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ConvertSRTToFrameFormat 将毫秒格式的 SRT 文件转换为帧格式，保存到新文件
// 输入：00:00:00,000 --> 00:00:00,000 (毫秒格式)
// 输出：00:00:00:00 --> 00:00:00:00 (帧格式)
// frameRate: 视频帧率（如 30.0 或 29.97），默认 30fps
// 返回新文件的路径
func ConvertSRTToFrameFormat(srtPath string, frameRate float64) (string, error) {
	if frameRate == 0 {
		frameRate = 30.0
	}

	// 读取原始 SRT 文件
	srtFile, err := os.Open(srtPath)
	if err != nil {
		return "", fmt.Errorf("打开 SRT 文件失败: %w", err)
	}
	defer srtFile.Close()

	// 生成新的 SRT 文件路径（添加 .frame 后缀）
	ext := filepath.Ext(srtPath)
	basePath := strings.TrimSuffix(srtPath, ext)
	newSrtPath := basePath + ".frame" + ext

	// 创建新的 SRT 文件
	newSrtFile, err := os.Create(newSrtPath)
	if err != nil {
		return "", fmt.Errorf("创建新 SRT 文件失败: %w", err)
	}
	defer newSrtFile.Close()

	scanner := bufio.NewScanner(srtFile)
	writer := bufio.NewWriter(newSrtFile)
	defer writer.Flush()

	// 时间戳正则：00:00:00,000 --> 00:00:00,000 (毫秒格式)
	timePattern := regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2}),(\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2}),(\d{3})`)

	for scanner.Scan() {
		line := scanner.Text()

		// 检查是否是时间戳行
		if timePattern.MatchString(line) {
			// 转换为帧格式
			convertedTime := convertMillisecondToFrame(line, frameRate)
			if _, err := writer.WriteString(convertedTime + "\n"); err != nil {
				return "", fmt.Errorf("写入转换后的时间戳失败: %w", err)
			}
		} else {
			// 其他行直接复制
			if _, err := writer.WriteString(line + "\n"); err != nil {
				return "", fmt.Errorf("写入行失败: %w", err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取 SRT 文件失败: %w", err)
	}

	return newSrtPath, nil
}

// convertMillisecondToFrame 将毫秒格式的时间戳转换为帧格式
// 输入：00:00:00,000 --> 00:00:00,000
// 输出：00:00:00:00 --> 00:00:00:00
func convertMillisecondToFrame(timeStr string, frameRate float64) string {
	// 解析时间戳
	timePattern := regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2}),(\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2}),(\d{3})`)
	matches := timePattern.FindStringSubmatch(timeStr)

	if len(matches) < 9 {
		// 如果解析失败，返回原字符串
		return timeStr
	}

	// 转换为帧格式
	startFrame := timeToFrame(matches[1], matches[2], matches[3], matches[4], frameRate)
	endFrame := timeToFrame(matches[5], matches[6], matches[7], matches[8], frameRate)

	// 计算帧数对应的时分秒
	startTime := frameToTime(startFrame, frameRate)
	endTime := frameToTime(endFrame, frameRate)

	return fmt.Sprintf("%s:%s:%s:%s --> %s:%s:%s:%s",
		startTime[0], startTime[1], startTime[2], startTime[3],
		endTime[0], endTime[1], endTime[2], endTime[3])
}

// timeToFrame 将时间转换为帧数
func timeToFrame(h, m, s, ms string, frameRate float64) int {
	hours, _ := strconv.Atoi(h)
	minutes, _ := strconv.Atoi(m)
	seconds, _ := strconv.Atoi(s)
	milliseconds, _ := strconv.Atoi(ms)

	totalSeconds := float64(hours)*3600 + float64(minutes)*60 + float64(seconds) + float64(milliseconds)/1000.0
	frame := int(totalSeconds * frameRate)
	return frame
}

// frameToTime 将帧数转换为时间（HH, MM, SS, FF）
func frameToTime(frame int, frameRate float64) [4]string {
	totalSeconds := float64(frame) / frameRate

	hours := int(totalSeconds) / 3600
	minutes := (int(totalSeconds) % 3600) / 60
	seconds := int(totalSeconds) % 60
	frames := int((totalSeconds - float64(int(totalSeconds))) * frameRate)

	// 确保帧数在有效范围内（0 到 frameRate-1）
	maxFrames := int(frameRate)
	if frames >= maxFrames {
		frames = maxFrames - 1
	}

	return [4]string{
		fmt.Sprintf("%02d", hours),
		fmt.Sprintf("%02d", minutes),
		fmt.Sprintf("%02d", seconds),
		fmt.Sprintf("%02d", frames),
	}
}

// IsMillisecondFormat 检查 SRT 文件是否使用毫秒格式
func IsMillisecondFormat(srtPath string) bool {
	file, err := os.Open(srtPath)
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// 只检查前 20 行，应该足够找到时间戳
	lineCount := 0
	millisecondPattern := regexp.MustCompile(`\d{2}:\d{2}:\d{2},\d{3}\s*-->\s*\d{2}:\d{2}:\d{2},\d{3}`)
	framePattern := regexp.MustCompile(`\d{2}:\d{2}:\d{2}:\d{2}\s*-->\s*\d{2}:\d{2}:\d{2}:\d{2}`)

	for scanner.Scan() && lineCount < 20 {
		line := scanner.Text()
		if millisecondPattern.MatchString(line) {
			return true
		}
		if framePattern.MatchString(line) {
			return false
		}
		lineCount++
	}

	// 默认假设是毫秒格式（更常见）
	return true
}

