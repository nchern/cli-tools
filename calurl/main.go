package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nchern/cli-tools/calurl/parse"
	"github.com/nchern/cli-tools/calurl/providers"
)

const (
	defaultDuration = "30m"
	defaultProvider = "google"
)

var (
	flagDesc     = flag.String("d", "", "Description")
	flagDuration = flag.String("u", defaultDuration, "Event duration (required)")
	flagGuests   = flag.String("g", "",
		"A list of guests, comma-separated emails. E.g. elf1@example.com,elf2@example.com")
	flagLocation = flag.String("l", "", "Location")
	flagProvider = flag.String("p", defaultProvider, "Provider: google|outlook|apple")
	flagTimezone = flag.String("z", "", "Timezone (default: system local)")
	flagTitle    = flag.String("t", "", "Event title (required)")
	flagWhen     = flag.String("w", "",
		"When event happens (reqiured). Either start datetime (YYYY-MM-DDTHH:MM) "+
			"or human readable string like 'next monday at 11:30am'")

	flagOpen = flag.Bool("o", false,
		"Open an url in browser instead of printing it out. "+
			fmt.Sprintf("Uses $VIEWER(%s) to open urls", getViewer()))
)

func getViewer() string {
	v := os.Getenv("VIEWER")
	if v == "" {
		return "xdg-open"
	}
	return v
}

func must(err error) {
	dieIf(err)
}

func dieIf(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s\n", err)
		os.Exit(1)
	}
}

func openURL(u *url.URL) error {
	cmd := exec.Command(getViewer(), u.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func init() {
	flag.Parse()
}

func main() {
	evt, err := parseAndValidate()
	dieIf(err)
	eventURL, err := mkUrl(evt)
	dieIf(err)
	if *flagOpen {
		must(openURL(eventURL))
		return
	}
	fmt.Println(eventURL.String())
}

func parseAndValidate() (*providers.Event, error) {
	if *flagTitle == "" || *flagWhen == "" {
		return nil, fmt.Errorf("-t and -w are required")
	}
	loc, err := parse.Timezone(*flagTimezone)
	if err != nil {
		return nil, fmt.Errorf("bad timezone: %w", err)
	}
	startTime, err := parse.Human(time.Now().In(loc), *flagWhen)
	if err != nil {
		return nil, fmt.Errorf("bad start time: %w", err)
	}
	d, err := parse.Duration(*flagDuration)
	if err != nil {
		return nil, fmt.Errorf("bad duration: %w", err)
	}
	return &providers.Event{
		Desc:     *flagDesc,
		Guests:   strings.TrimSpace(*flagGuests),
		Location: *flagLocation,
		Title:    *flagTitle,

		Start: startTime,
		End:   startTime.Add(d),
	}, nil
}

func mkUrl(evt *providers.Event) (*url.URL, error) {
	switch strings.ToLower(*flagProvider) {
	case "google":
		return providers.GoogleURL(evt)
	case "outlook":
		return providers.OutlookURL(evt)
	case "apple":
		return providers.AppleURL(evt)
	}
	return nil, fmt.Errorf("unknown provider: %s", *flagProvider)
}
