package parse

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration parses human readable durations
func Duration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	if val, err := strconv.Atoi(s); err == nil {
		// val is a correct number without units - use minutes by default
		return time.Duration(val) * time.Minute, nil
	}
	if strings.HasSuffix(s, "d") {
		val, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid number in duration: %w", err)
		}
		return time.Duration(val) * 24 * time.Hour, nil
	}
	// handle units like: 1h, 20m or 3s
	return time.ParseDuration(s)
}

// Timezone parses timezones
func Timezone(tz string) (*time.Location, error) {
	if tz == "" {
		return time.Now().Location(), nil
	}
	return time.LoadLocation(tz)
}
