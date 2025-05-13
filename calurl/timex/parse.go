package timex

import (
	"fmt"
	"strings"
	"time"
)

// ParseHuman parses:
// 1. "YYYY-MM-DDTHH:MM"
// 2. "tomorrow at 10am"
// 3. "next mon at 9am"
// 4. "next week at 14:00"
func ParseHuman(now time.Time, s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty input")
	}
	loc := now.Location()
	// Try direct datetime parse
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, loc); err == nil {
		return t, nil
	}
	s = strings.ToLower(s)

	baseDate := now
	tokens := strings.Fields(s)

	i := 0
	for i < len(tokens) {
		if t, err := time.ParseInLocation("2006-01-02", tokens[i], loc); err == nil {
			i++
			baseDate = t
			continue
		}
		switch tokens[i] {
		case "today":
			i++
		case "tomorrow":
			baseDate = baseDate.AddDate(0, 0, 1)
			i++
		case "next":
			if i+1 < len(tokens) {
				switch tokens[i+1] {
				case "day":
					baseDate = baseDate.AddDate(0, 0, 1)
					i += 2
				case "week":
					baseDate = baseDate.AddDate(0, 0, 7)
					i += 2
				default:
					// try to parse day of the week
					targetDow, ok := weekdayFromString(tokens[i+1])
					if !ok {
						return time.Time{}, fmt.Errorf("unknown weekday: %s", tokens[i+1])
					}
					baseDate = nextWeekday(baseDate, targetDow)
					i += 2
				}
			} else {
				return time.Time{}, fmt.Errorf("'next' requires additional token")
			}
		case "at":
			if i+1 < len(tokens) {
				return buildDateTime(baseDate, tokens[i+1], loc)
			}
			return time.Time{}, fmt.Errorf("'at' requires time value")
		default:
			return time.Time{}, fmt.Errorf("unknown token: %s", tokens[i])
		}
	}
	return time.Time{}, fmt.Errorf("no time specified with 'at'")
}

func weekdayFromString(s string) (time.Weekday, bool) {
	switch s {
	case "sun", "sunday":
		return time.Sunday, true
	case "mon", "monday":
		return time.Monday, true
	case "tue", "tuesday":
		return time.Tuesday, true
	case "wed", "wednesday":
		return time.Wednesday, true
	case "thu", "thursday":
		return time.Thursday, true
	case "fri", "friday":
		return time.Friday, true
	case "sat", "saturday":
		return time.Saturday, true
	}
	return 0, false
}

func nextWeekday(from time.Time, target time.Weekday) time.Time {
	offset := (int(target) - int(from.Weekday()) + 7) % 7
	if offset == 0 {
		offset = 7
	}
	return from.AddDate(0, 0, offset)
}

// buildDateTime combines date + time string
// supports "10am", "7pm", "6:30pm", "13:00", "15"
func buildDateTime(date time.Time, timestr string, loc *time.Location) (time.Time, error) {
	layouts := []string{
		"15:04",  // 24-hour HH:MM
		"3pm",    // 12-hour Hpm
		"3:04pm", // 12-hour H:MMpm
		"15",     // 24-hour HH
	}
	var parsedTime time.Time
	var err error
	for _, layout := range layouts {
		parsedTime, err = time.ParseInLocation(layout, timestr, loc)
		if err == nil {
			return time.Date(date.Year(), date.Month(), date.Day(),
				parsedTime.Hour(), parsedTime.Minute(), 0, 0, loc), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time format: %s", timestr)
}
