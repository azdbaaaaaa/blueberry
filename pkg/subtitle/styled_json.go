package subtitle

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// StyledSubtitleJSON 描述带样式的字幕 JSON 顶层结构
// 参考用户提供示例：
//
//	{
//	  "font_size": 0.4,
//	  "font_color": "#FFFFFF",
//	  "background_alpha": 0.5,
//	  "background_color": "#9C27B0",
//	  "Stroke": "none",
//	  "body": [
//	    { "from": 0.033, "to": 2, "location": 2, "content": "..." },
//	    ...
//	  ]
//	}
type StyledSubtitleJSON struct {
	FontSize        float64           `json:"font_size"`
	FontColor       string            `json:"font_color"`
	BackgroundAlpha float64           `json:"background_alpha"`
	BackgroundColor string            `json:"background_color"`
	Stroke          string            `json:"Stroke"`
	Body            []StyledBodyEntry `json:"body"`
}

type StyledBodyEntry struct {
	From     float64 `json:"from"`
	To       float64 `json:"to"`
	Location int     `json:"location"`
	Content  string  `json:"content"`
}

// BilibiliSubtitleRichEntry 上传给 B站时的“富”条目，包含样式与时间信息
// 注意：B站接口是否严格检查字段未知；若严格，可能需要回退到最小集（id、text）
type BilibiliSubtitleRichEntry struct {
	ID              string  `json:"id"`
	Text            string  `json:"text"`
	From            float64 `json:"from,omitempty"`
	To              float64 `json:"to,omitempty"`
	Location        int     `json:"location,omitempty"`
	FontSize        float64 `json:"font_size,omitempty"`
	FontColor       string  `json:"font_color,omitempty"`
	BackgroundAlpha float64 `json:"background_alpha,omitempty"`
	BackgroundColor string  `json:"background_color,omitempty"`
	Stroke          string  `json:"stroke,omitempty"`
}

// ConvertStyledJSONToRichEntries 读取带样式的字幕 JSON，并转成包含样式的条目数组
func ConvertStyledJSONToRichEntries(jsonPath string) ([]BilibiliSubtitleRichEntry, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("读取样式字幕JSON失败: %w", err)
	}
	var styled StyledSubtitleJSON
	if err := json.Unmarshal(data, &styled); err != nil {
		return nil, fmt.Errorf("解析样式字幕JSON失败: %w", err)
	}
	entries := make([]BilibiliSubtitleRichEntry, 0, len(styled.Body))
	for i, b := range styled.Body {
		// 使用开始时间毫秒 + 序号作为 id
		// 保持与 SRT 转换逻辑接近（毫秒字符串 + 索引）
		id := fmt.Sprintf("%d-%d", int64(b.From*1000.0), i)
		entries = append(entries, BilibiliSubtitleRichEntry{
			ID:              id,
			Text:            b.Content,
			From:            b.From,
			To:              b.To,
			Location:        b.Location,
			FontSize:        styled.FontSize,
			FontColor:       styled.FontColor,
			BackgroundAlpha: styled.BackgroundAlpha,
			BackgroundColor: styled.BackgroundColor,
			Stroke:          styled.Stroke,
		})
	}
	return entries, nil
}

// StyledDefaults 定义从 SRT 生成样式字幕时的全局样式默认值
type StyledDefaults struct {
	FontSize        float64
	FontColor       string
	BackgroundAlpha float64
	BackgroundColor string
	Stroke          string
	Location        int
}

// ConvertSRTToStyledJSON 将 SRT 转换为带样式的 JSON（包含 from/to/location 与全局样式）
func ConvertSRTToStyledJSON(srtPath string, defaults StyledDefaults) (*StyledSubtitleJSON, error) {
	srtEntries, err := ParseSRT(srtPath)
	if err != nil {
		return nil, err
	}
	styled := &StyledSubtitleJSON{
		FontSize:        defaults.FontSize,
		FontColor:       defaults.FontColor,
		BackgroundAlpha: defaults.BackgroundAlpha,
		BackgroundColor: defaults.BackgroundColor,
		Stroke:          defaults.Stroke,
		Body:            make([]StyledBodyEntry, 0, len(srtEntries)),
	}
	for _, e := range srtEntries {
		startMsStr := extractTimestampFromTime(e.StartTime)
		endMsStr := extractTimestampFromTime(e.EndTime)
		// 重用 ms 计算逻辑（字符串 -> 毫秒）
		startMs, _ := timeStringToMs(strings.Replace(e.StartTime, ".", ",", 1))
		endMs, _ := timeStringToMs(strings.Replace(e.EndTime, ".", ",", 1))
		_ = startMsStr
		_ = endMsStr
		styled.Body = append(styled.Body, StyledBodyEntry{
			From:     float64(startMs) / 1000.0,
			To:       float64(endMs) / 1000.0,
			Location: defaults.Location,
			Content:  e.Text,
		})
	}
	return styled, nil
}
