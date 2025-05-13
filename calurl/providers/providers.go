package providers

import (
	"fmt"
	"net/url"
	"time"
)

type Event struct {
	Desc     string
	Guests   string
	Location string
	Title    string

	Start time.Time
	End   time.Time
}

func formatICS(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

func GoogleURL(evt *Event) (*url.URL, error) {
	u, err := url.Parse("https://calendar.google.com/calendar/render")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("action", "TEMPLATE")
	params.Set("text", evt.Title)
	params.Set("dates", fmt.Sprintf("%s/%s", formatICS(evt.Start), formatICS(evt.End)))
	if evt.Location != "" {
		params.Set("location", evt.Location)
	}
	if evt.Desc != "" {
		params.Set("details", evt.Desc)
	}
	if evt.Guests != "" {
		params.Set("add", evt.Guests)
	}
	u.RawQuery = params.Encode()
	return u, nil
}

func OutlookURL(evt *Event) (*url.URL, error) {
	u, err := url.Parse("https://outlook.live.com/owa/")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("path", "/calendar/action/compose")
	params.Set("rru", "addevent")
	params.Set("subject", evt.Title)
	params.Set("startdt", evt.Start.Format(time.RFC3339))
	params.Set("enddt", evt.End.Format(time.RFC3339))
	if evt.Location != "" {
		params.Set("location", evt.Location)
	}
	if evt.Desc != "" {
		params.Set("body", evt.Desc)
	}
	u.RawQuery = params.Encode()
	return u, nil
}

func AppleURL(evt *Event) (*url.URL, error) {
	u, err := url.Parse("webcal://example.com/event")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("title", evt.Title)
	params.Set("start", formatICS(evt.Start))
	params.Set("end", formatICS(evt.End))
	if evt.Location != "" {
		params.Set("location", evt.Location)
	}
	if evt.Desc != "" {
		params.Set("desc", evt.Desc)
	}
	u.RawQuery = params.Encode()
	return u, nil
}
