package main

import (
	"flag"
	"fmt"
	"log"
	"net/mail"
	"os"
	"sort"
	"time"

	"github.com/DusanKasan/parsemail"
)

const (
	cmdBCC      = "bcc"
	cmdCC       = "cc"
	cmdDate     = "date"
	cmdFrom     = "from"
	cmdHTMLBody = "html"
	cmdID       = "id"
	cmdSubject  = "subject"
	cmdTextBody = "text"
	cmdTo       = "to"

	defaultDateFmt = time.RFC1123Z
)

type cmdFn func(parsemail.Email)

var (
	optTimeFormat = flag.String("f", defaultDateFmt, "date and time format in go notation")

	commands = map[string]cmdFn{
		cmdBCC:      func(m parsemail.Email) { printAddrs(m.Bcc) },
		cmdCC:       func(m parsemail.Email) { printAddrs(m.Cc) },
		cmdDate:     func(m parsemail.Email) { fmt.Println(m.Date.Format(*optTimeFormat)) },
		cmdFrom:     func(m parsemail.Email) { printAddrs(m.From) },
		cmdHTMLBody: func(m parsemail.Email) { fmt.Println(m.HTMLBody) },
		cmdID:       func(m parsemail.Email) { fmt.Println(m.MessageID) },
		cmdSubject:  func(m parsemail.Email) { fmt.Println(m.Subject) },
		cmdTextBody: func(m parsemail.Email) { fmt.Println(m.TextBody) },
		cmdTo:       func(m parsemail.Email) { printAddrs(m.To) },
	}
)

func printAddrs(addrs []*mail.Address) {
	for i, addr := range addrs {
		if i > 0 {
			fmt.Print(",")
		}
		fmt.Print(addr)
	}
	fmt.Println()
}

func usage() {
	fmt.Fprintln(os.Stderr, "Pmail - Parse Mail - is a tool to extract parts of email from a raw SMTP message.")
	fmt.Fprintln(os.Stderr, "The message is expected on stdin.")
	fmt.Fprintf(os.Stderr, "\nUsage:\n\n\t%s <mail-part>\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "\nParts:")

	sortedCmds := []string{}
	for cmd := range commands {
		sortedCmds = append(sortedCmds, cmd)
	}
	sort.Strings(sortedCmds)
	for _, cmd := range sortedCmds {
		fmt.Fprintf(os.Stderr, "\t%s\n", cmd)
	}
	fmt.Println("Flags:")
	flag.PrintDefaults()
}

func init() {
	flag.Usage = usage

	flag.Parse()
}

func main() {
	cmd := cmdTextBody
	if len(flag.Args()) > 0 {
		cmd = flag.Args()[0]
	}

	fn, found := commands[cmd]
	if !found {
		dieIf(fmt.Errorf("unknown command: %s", cmd))
	}

	email, err := parsemail.Parse(os.Stdin)
	dieIf(err)

	fn(email)
}

func dieIf(err error) {
	if err != nil {
		log.Fatal("fatal: ", err)
	}
}
