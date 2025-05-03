package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	defaultDuration = "30m"
	defaultProvider = "google"

	flagTitle    = flag.String("t", "", "Event title (required)")
	flagStart    = flag.String("s", "", "Start datetime (YYYY-MM-DDTHH:MM, required)")
	flagDuration = flag.String("u", defaultDuration, "Event duration (required)")
	flagLocation = flag.String("l", "", "Location")
	flagDesc     = flag.String("d", "", "Description")
	flagTimezone = flag.String("z", "", "Timezone (default: system local)")
	flagProvider = flag.String("p", defaultProvider, "Provider: google|outlook|apple (default: google)")
)

type event struct {
	title    string
	desc     string
	location string

	start time.Time
	end   time.Time
}

func init() {
	flag.Parse()
}

func main() {
	eventURL, err := mkUrl()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s", err)
		os.Exit(1)
	}
	fmt.Println(eventURL.String())
}

func parseAndValidate() (*event, error) {
	if *flagTitle == "" || *flagStart == "" {
		return nil, fmt.Errorf("-t, and -s are required")
	}
	loc, err := parseTimezone(*flagTimezone)
	if err != nil {
		return nil, fmt.Errorf("bad timezone: %w", err)
	}
	startTime, err := parseTime(*flagStart, loc)
	if err != nil {
		return nil, fmt.Errorf("bad start time: %w", err)
	}
	d, err := parseDuration(*flagDuration)
	if err != nil {
		return nil, fmt.Errorf("bad duration: %w", err)
	}
	return &event{
		title:    *flagTitle,
		desc:     *flagDesc,
		location: *flagLocation,

		start: startTime,
		end:   startTime.Add(d),
	}, nil
}

func mkUrl() (*url.URL, error) {
	evt, err := parseAndValidate()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(*flagProvider) {
	case "google":
		return mkGoogleURL(evt)
	case "outlook":
		return mkOutlookURL(evt)
	case "apple":
		return mkAppleURL(evt)
	}
	return nil, fmt.Errorf("unknown provider: %s", *flagProvider)
}

func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	if val, err := strconv.Atoi(s); err == nil {
		// val is a correct number without units
		// use minutes by default
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

func parseTimezone(tz string) (*time.Location, error) {
	if tz == "" {
		return time.Now().Location(), nil
	}
	return time.LoadLocation(tz)
}

func parseTime(datetime string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation("2006-01-02T15:04", datetime, loc)
}

func formatICS(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// ------------------------- PROVIDER BUILDERS -------------------------

func mkGoogleURL(evt *event) (*url.URL, error) {
	u, err := url.Parse("https://calendar.google.com/calendar/render")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("action", "TEMPLATE")
	params.Set("text", evt.title)
	params.Set("dates", fmt.Sprintf("%s/%s", formatICS(evt.start), formatICS(evt.end)))
	if evt.location != "" {
		params.Set("location", evt.location)
	}
	if evt.desc != "" {
		params.Set("details", evt.desc)
	}
	u.RawQuery = params.Encode()
	return u, nil
}

func mkOutlookURL(evt *event) (*url.URL, error) {
	u, err := url.Parse("https://outlook.live.com/owa/")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("path", "/calendar/action/compose")
	params.Set("rru", "addevent")
	params.Set("subject", evt.title)
	params.Set("startdt", evt.start.Format(time.RFC3339))
	params.Set("enddt", evt.end.Format(time.RFC3339))
	if evt.location != "" {
		params.Set("location", evt.location)
	}
	if evt.desc != "" {
		params.Set("body", evt.desc)
	}
	u.RawQuery = params.Encode()
	return u, nil
}

func mkAppleURL(evt *event) (*url.URL, error) {
	u, err := url.Parse("webcal://example.com/event")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("title", evt.title)
	params.Set("start", formatICS(evt.start))
	params.Set("end", formatICS(evt.end))
	if evt.location != "" {
		params.Set("location", evt.location)
	}
	if evt.desc != "" {
		params.Set("desc", evt.desc)
	}
	u.RawQuery = params.Encode()
	return u, nil
}
