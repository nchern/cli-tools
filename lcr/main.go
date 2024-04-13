package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	fgRed     Color = 31
	fgGreen   Color = 32
	fgYellow  Color = 33
	fgMagenta Color = 35

	fgHiRed     Color = 91
	fgHiMagenta Color = 95

	// bold      = "1"
	// underline = "4"
)

// Color represents 256-term ANSI color
// Full list: https://gist.github.com/fnky/458719343aabd01cfb17a3a4f7296797
type Color byte

type Entity struct {
	color   Color
	matcher Matcher
}

type Matcher interface {
	Match(string) bool
}

type RegexpMatcher struct{ *regexp.Regexp }

func NewRegexpMatcher(s string) *RegexpMatcher {
	return &RegexpMatcher{regexp.MustCompile(s)}
}

func (m RegexpMatcher) Match(s string) bool { return m.MatchString(s) }

type NumberMatcher struct{}

func (m *NumberMatcher) Match(s string) bool {
	_, err := strconv.ParseFloat(s, 32)
	return err == nil
}

var (
	terminalSymbols = map[string]bool{
		" ": true,
		"[": true,
		"]": true,
		"(": true,
		")": true,
		"=": true,
	}

	entities = map[string]*Entity{
		"number": {
			color:   178,
			matcher: &NumberMatcher{},
		},
		"error": {
			color:   1,
			matcher: NewRegexpMatcher("(?i)error[:]?"),
		},
		"iso_date_time": {
			color:   48,
			matcher: NewRegexpMatcher(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\+[0-9]+?){0,1}$`),
		},
		"ip_v4_addr": {
			color:   202,
			matcher: NewRegexpMatcher(`[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}`),
		},
		"ip_v6_addr": {
			color:   202,
			matcher: NewRegexpMatcher(`^([0-9a-fA-F]{1,4}:){7}|(([0-9a-fA-F]{1,4}:){6}(:[0-9a-fA-F]{1,4}|((25[0-5]|2[0-4][0-9]|[01]?[0-9]{1,2})\.){3}([0-9]{1,3}))?|(([0-9a-fA-F]{1,4}:){5}:([0-9a-fA-F]{1,4})?)|(([0-9a-fA-F]{1,4}:){4}:(:[0-9a-fA-F]{1,4}){0,2}|((25[0-5]|2[0-4][0-9]|[01]?[0-9]{1,2})\.){3}([0-9]{1,3}))|(([0-9a-fA-F]{1,4}:){3}:(:[0-9a-fA-F]{1,4}){0,3}|((25[0-5]|2[0-4][0-9]|[01]?[0-9]{1,2})\.){3}([0-9]{1,3}))|(([0-9a-fA-F]{1,4}:){2}:(:[0-9a-fA-F]{1,4}){0,4}|((25[0-5]|2[0-4][0-9]|[01]?[0-9]{1,2})\.){3}([0-9]{1,3}))|(([0-9a-fA-F]{1,4}:){1}:([0-9a-fA-F]{1,4}){0,5}|((25[0-5]|2[0-4][0-9]|[01]?[0-9]{1,2})\.){3}([0-9]{1,3})))$`),
		},
	}
)

func tokenize(line string) <-chan string {
	toks := make(chan string, 256)
	emit := func(s string) {
		if s != "" {
			toks <- s
		}
	}
	go func() {
		cur := ""
		for _, v := range line {
			s := string(v)
			if terminalSymbols[s] {
				emit(cur)
				emit(s)
				cur = ""
				continue
			}
			cur += s
		}
		emit(cur)
		close(toks)
	}()
	return toks
}

func process(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	i := -1
	for scanner.Scan() {
		i++
		toks := []string{}
		for cur := range tokenize(scanner.Text()) {
			for _, entity := range entities {
				if entity.matcher.Match(cur) {
					cur = colorize256(cur, entity.color)
					break
				}
			}
			l := len(toks) - 1
			if l > -1 && !terminalSymbols[toks[l]] && cur == "=" {
				toks[l] = colorize256(toks[l], 13)
				cur = colorize256(cur, 13)
			}
			toks = append(toks, cur)
		}
		line := strings.Join(toks, "")
		if _, err := fmt.Println(line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func main() {
	must(process(os.Stdin))
}

// func colorize(s string, color Color, attrs ...string) string {
// RED='\033[0;31m'
// return fmt.Sprintf("\033[0;%d;%sm%s\033[0m", color, strings.Join(attrs, ";"), s)
// return fmt.Sprintf("\033[0;%dm%s\033[0m", color, s)
// }

func colorize256(s string, color Color, attrs ...string) string {
	return fmt.Sprintf("\033[38;5;%dm%s\033[0m", color, s)
}

func must(err error) {
	if err != nil {
		log.Fatal("fatal: ", err)
	}
}
