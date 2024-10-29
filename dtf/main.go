package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	formats = map[string]string{
		"iso":       "2006-01-02T15:04:05",
		"short-iso": "2006-01-02",
	}
)

func parseRelativeTime(val string) (time.Time, error) {
	tokens := strings.Split(val, " ")
	if len(tokens) != 3 {
		return time.Time{}, fmt.Errorf("%s: bad relative time format", val)
	}
	n, err := strconv.Atoi(tokens[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: bad integer in relative time: %v", tokens[0], err)
	}
	now := time.Now().Local()
	unit := strings.TrimSuffix(tokens[1], "s")

	switch unit {
	case "sec":
		return now.Add(-time.Duration(n) * time.Second), nil
	case "min":
		return now.Add(-time.Duration(n) * time.Minute), nil
	case "hour":
		return now.Add(-time.Duration(n) * time.Hour), nil
	case "day":
		return now.AddDate(0, 0, -n), nil
	default:
		return time.Time{}, fmt.Errorf("%s: unknown or unsupported units", unit)
	}
}

func parseDate(val, dateFmt string) (time.Time, error) {
	if dateFmt == "" {
		dateFmt = formats["iso"]
	}
	if strings.HasSuffix(val, " ago") {
		return parseRelativeTime(val)
	}
	return time.ParseInLocation(dateFmt, val, time.Local)
}

type Args struct {
	Since    time.Time
	Until    time.Time
	Format   string
	FieldIdx int
	Verbose  bool
}

func parseArgs() (*Args, error) {
	since := flag.String("since", "", "start period")
	until := flag.String("until", "now", "end period")
	dateFmt := flag.String("format", "2006-01-02T15:04:05", "date and time format")
	fieldIdx := flag.String("f", "1", "index of the date time field, starts with 1")
	verbose := flag.Bool("v", false, "print out all line processing errors")
	flag.Parse()

	parsedSince, err := parseDate(*since, *dateFmt)
	if err != nil {
		return nil, fmt.Errorf("error parsing --since: %v", err)
	}

	parsedUntil := time.Now().Local()
	if *until != "now" {
		parsedUntil, err = parseDate(*until, *dateFmt)
		if err != nil {
			return nil, fmt.Errorf("error parsing --until: %v", err)
		}
	}

	idx, err := strconv.Atoi(*fieldIdx)
	if err != nil || idx <= 0 {
		return nil, fmt.Errorf("%s should be greater than zero", *fieldIdx)
	}
	idx--

	return &Args{Since: parsedSince, Until: parsedUntil, Format: *dateFmt, FieldIdx: idx, Verbose: *verbose}, nil
}

func shouldSkip(fields []string, args *Args) (bool, error) {
	if args.FieldIdx < 0 || args.FieldIdx >= len(fields) {
		return true, fmt.Errorf("out of range: %d", args.FieldIdx)
	}
	dt, err := parseDate(fields[args.FieldIdx], args.Format)
	if err != nil {
		return true, err
	}
	return dt.Before(args.Since) || dt.After(args.Until), nil
}

func processLine(line string, args *Args) error {
	fields := strings.Fields(line)
	skip, err := shouldSkip(fields, args)
	if err != nil {
		return fmt.Errorf("warn: problem: %v, skipping line: %s", err, line)
	}
	if !skip {
		fmt.Println(line)
	}
	return nil
}

func run() error {
	args, err := parseArgs()
	if err != nil {
		return fmt.Errorf("parse args: %v", err)
	}
	i := 1
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if err := processLine(line, args); err != nil && args.Verbose {
			fmt.Fprintf(os.Stderr, "%d: %s\n", i, err)
		}
		i++
	}
	return scanner.Err()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
