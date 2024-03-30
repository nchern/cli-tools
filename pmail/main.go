package main

import (
	"flag"
	"fmt"
	"log"
	"net/mail"
	"os"
	"sort"

	"github.com/DusanKasan/parsemail"
)

const (
	cmdCC       = "cc"
	cmdTo       = "to"
	cmdID       = "id"
	cmdBCC      = "bcc"
	cmdFrom     = "from"
	cmdSubject  = "subject"
	cmdHTMLBody = "html"
	cmdTextBody = "text"
)

type cmdFn func(parsemail.Email)

var (
	commands = map[string]cmdFn{
		cmdSubject:  func(m parsemail.Email) { fmt.Println(m.Subject) },
		cmdHTMLBody: func(m parsemail.Email) { fmt.Println(m.HTMLBody) },
		cmdFrom:     func(m parsemail.Email) { printAddrs(m.From) },
		cmdTo:       func(m parsemail.Email) { printAddrs(m.To) },
		cmdCC:       func(m parsemail.Email) { printAddrs(m.Cc) },
		cmdBCC:      func(m parsemail.Email) { printAddrs(m.Bcc) },
		cmdTextBody: func(m parsemail.Email) { fmt.Println(m.TextBody) },
		cmdID:       func(m parsemail.Email) { fmt.Println(m.MessageID) },
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

	flag.PrintDefaults()
}

func init() {
	flag.Usage = usage

	flag.Parse()
}

func main() {
	cmd := cmdTextBody
	if len(os.Args) > 1 {
		cmd = os.Args[1]
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
