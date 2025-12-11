package subtitle

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// timeStringToMs converts "HH:MM:SS,mmm" to milliseconds.
func timeStringToMs(ts string) (int64, error) {
	parts := strings.Split(ts, ",")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid time format: %s", ts)
	}
	hms := strings.Split(parts[0], ":")
	if len(hms) != 3 {
		return 0, fmt.Errorf("invalid time format: %s", ts)
	}
	h, err1 := strconv.Atoi(hms[0])
	m, err2 := strconv.Atoi(hms[1])
	s, err3 := strconv.Atoi(hms[2])
	ms, err4 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return 0, fmt.Errorf("invalid time value: %s", ts)
	}
	return int64(h)*3600000 + int64(m)*60000 + int64(s)*1000 + int64(ms), nil
}

// msToTimeString converts milliseconds to "HH:MM:SS,mmm".
func msToTimeString(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// FixSRTOverlaps reads an SRT file, fixes overlapping or inverted time ranges,
// and writes the fixed entries back to the same file path.
// Returns number of entries adjusted.
func FixSRTOverlaps(srtPath string) (int, error) {
	entries, err := ParseSRT(srtPath)
	if err != nil {
		return 0, err
	}

	if len(entries) == 0 {
		return 0, nil
	}

	type timed struct {
		idx      int
		startMs  int64
		endMs    int64
		text     string
	}

	var list []timed
	for i, e := range entries {
		startMs, err1 := timeStringToMs(e.StartTime)
		endMs, err2 := timeStringToMs(e.EndTime)
		if err1 != nil || err2 != nil {
			// Skip malformed time entry; keep as-is
			startMs = 0
			endMs = 0
		}
		list = append(list, timed{
			idx:     i,
			startMs: startMs,
			endMs:   endMs,
			text:    e.Text,
		})
	}

	// Sort by start time, stable to keep original order when equal
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].startMs == list[j].startMs {
			return list[i].idx < list[j].idx
		}
		return list[i].startMs < list[j].startMs
	})

	const minDurationMs int64 = 400
	adjusted := 0
	var lastEnd int64 = -1

	for i := range list {
		// Ensure non-negative and start <= end
		if list[i].endMs <= list[i].startMs {
			list[i].endMs = list[i].startMs + minDurationMs
			adjusted++
		}
		// Fix overlap with previous
		if lastEnd >= 0 && list[i].startMs < lastEnd {
			list[i].startMs = lastEnd
			if list[i].endMs <= list[i].startMs {
				list[i].endMs = list[i].startMs + minDurationMs
			}
			adjusted++
		}
		lastEnd = list[i].endMs
	}

	// Optional: fix overly long single subtitles (not required; keep as-is)

	// Write back to entries in original order (by idx)
	out := make([]SRTEntry, len(entries))
	for _, t := range list {
		out[t.idx] = SRTEntry{
			Index:     entries[t.idx].Index,
			StartTime: msToTimeString(t.startMs),
			EndTime:   msToTimeString(t.endMs),
			Text:      entries[t.idx].Text,
			ID:        entries[t.idx].ID,
		}
	}

	if err := WriteSRT(out, srtPath); err != nil {
		return 0, err
	}
	return adjusted, nil
}


