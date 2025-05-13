package timex

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ParseHuman parses:
// 1. "YYYY-MM-DDTHH:MM"
// 2. "tomorrow at 10am"
// 3. "next mon at 9am"
// 4. "next week at 14:00"
func ParseHuman(now time.Time, s string) (time.Time, error) {
	loc := now.Location()

	// Try direct layout first
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, loc); err == nil {
		return t, nil
	}

	tokens := strings.Fields(strings.ToLower(s))
	if len(tokens) == 0 {
		return time.Time{}, fmt.Errorf("empty input")
	}

	// Extract optional time after "at"
	baseTokens, timeToken, err := splitTimeToken(tokens)
	if err != nil {
		return time.Time{}, err
	}

	// Try parse 2006-01-02 at ...
	if len(baseTokens) > 0 {
		if t, err := time.ParseInLocation("2006-01-02", baseTokens[0], loc); err == nil {
			return applyTimeIfNeeded(t, timeToken, loc)
		}
	}

	// Try month-day format: "jul 17th", "december 31"
	if t, ok := parseMonthDay(baseTokens, now, loc); ok {
		return applyTimeIfNeeded(t, timeToken, loc)
	}

	// Try relative expressions: "tomorrow", "next week", "in 3 days"
	t, err := parseRelativeDate(baseTokens, now)
	if err != nil {
		return time.Time{}, err
	}
	return applyTimeIfNeeded(t, timeToken, loc)
}

func splitTimeToken(tokens []string) ([]string, string, error) {
	for i := 0; i < len(tokens)-1; i++ {
		if tokens[i] == "at" {
			return tokens[:i], tokens[i+1], nil
		}
	}
	return tokens, "", nil
}

func parseMonthDay(tokens []string, now time.Time, loc *time.Location) (time.Time, bool) {
	if len(tokens) < 2 {
		return time.Time{}, false
	}
	month, ok := monthFromString(tokens[0])
	if !ok {
		return time.Time{}, false
	}
	day, err := stripDaySuffix(tokens[1])
	if err != nil {
		return time.Time{}, false
	}
	year := now.Year()
	t := time.Date(year, month, day, 0, 0, 0, 0, loc)
	if t.Before(now) {
		t = t.AddDate(1, 0, 0)
	}
	return t, true
}

func parseRelativeDate(tokens []string, now time.Time) (time.Time, error) {
	base := now
	i := 0
	for i < len(tokens) {
		switch tokens[i] {
		case "today":
			i++
		case "tomorrow":
			base = base.AddDate(0, 0, 1)
			i++
		case "next":
			if i+1 >= len(tokens) {
				return time.Time{}, fmt.Errorf("'next' requires additional token")
			}
			switch tokens[i+1] {
			case "day":
				base = base.AddDate(0, 0, 1)
			case "week":
				base = base.AddDate(0, 0, 7)
			case "mon", "monday", "tue", "tuesday", "wed", "wednesday",
				"thu", "thursday", "fri", "friday", "sat", "saturday", "sun", "sunday":
				dow, ok := weekdayFromString(tokens[i+1])
				if !ok {
					return time.Time{}, fmt.Errorf("unknown token after 'next': %s", tokens[i+1])
				}
				base = nextWeekday(base, dow)
			default:
				return time.Time{}, fmt.Errorf("unknown token after 'next': %s", tokens[i+1])
			}
			i += 2
		case "in":
			if i+2 >= len(tokens) {
				return time.Time{}, fmt.Errorf("invalid 'in' syntax")
			}
			n, err := strconv.Atoi(tokens[i+1])
			if err != nil {
				return time.Time{}, fmt.Errorf("invalid number in 'in' clause: %s", tokens[i+1])
			}
			switch tokens[i+2] {
			case "day", "days":
				base = base.AddDate(0, 0, n)
			case "week", "weeks":
				base = base.AddDate(0, 0, 7*n)
			default:
				return time.Time{}, fmt.Errorf("unknown unit in 'in' clause: %s", tokens[i+2])
			}
			i += 3
		default:
			return time.Time{}, fmt.Errorf("unknown token: %s", tokens[i])
		}
	}
	return base, nil
}

func applyTimeIfNeeded(date time.Time, timeToken string, loc *time.Location) (time.Time, error) {
	if timeToken == "" {
		return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc), nil
	}
	return buildDateTime(date, timeToken, loc)
}

func buildDateTime(date time.Time, timestr string, loc *time.Location) (time.Time, error) {
	layouts := []string{
		"15:04",  // 24-hour HH:MM
		"3pm",    // 12-hour Hpm
		"3:04pm", // 12-hour H:MMpm
		"15",     // 24-hour HH
	}

	for _, layout := range layouts {
		if parsedTime, err := time.ParseInLocation(layout, timestr, loc); err == nil {
			return time.Date(date.Year(), date.Month(), date.Day(),
				parsedTime.Hour(), parsedTime.Minute(), 0, 0, loc), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time format: %s", timestr)
}

func stripDaySuffix(s string) (int, error) {
	for i := 0; i < len(s); i++ {
		if !unicode.IsDigit(rune(s[i])) {
			return strconv.Atoi(s[:i])
		}
	}
	return strconv.Atoi(s)
}

func monthFromString(s string) (time.Month, bool) {
	switch s {
	case "jan", "january":
		return time.January, true
	case "feb", "february":
		return time.February, true
	case "mar", "march":
		return time.March, true
	case "apr", "april":
		return time.April, true
	case "may":
		return time.May, true
	case "jun", "june":
		return time.June, true
	case "jul", "july":
		return time.July, true
	case "aug", "august":
		return time.August, true
	case "sep", "sept", "september":
		return time.September, true
	case "oct", "october":
		return time.October, true
	case "nov", "november":
		return time.November, true
	case "dec", "december":
		return time.December, true
	default:
		return 0, false
	}
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
	default:
		return 0, false
	}
}

func nextWeekday(from time.Time, target time.Weekday) time.Time {
	offset := (int(target) - int(from.Weekday()) + 7) % 7
	if offset == 0 {
		offset = 7
	}
	return from.AddDate(0, 0, offset)
}
