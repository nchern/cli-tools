package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const (
	appName = "imapq"

	defaultDirPerms = 0700

	imapTimeout = 20 * time.Second

	// /usr/include/sysexits.h:101: EX_UNAVAILABLE - service unavailable
	exitUnavailable = 69
)

var (
	appHomeDir string
	cacheDir   string

	// CLI args
	addrArg           = flag.String("addr", "imap.gmail.com:993", "IMAP server address")
	mboxArg           = flag.String("mailbox", "INBOX", "mailbox on the server")
	passwordArg       = flag.String("pass", "", "IMAP password")
	userArg           = flag.String("user", "", "IMAP user")
	maxMailFetchCount = flag.Int("m", 100, "Maximum number of messages to fetch")
	// persist           = flag.Bool("persist", false, "Persit results to file")
)

type letter struct {
	Date    string `json:"date"`
	From    string `json:"from"`
	SeqNum  uint32 `json:"seq_num"`
	Subject string `json:"subject"`
}

func letterFromMessage(m *imap.Message) *letter {
	res := &letter{
		Date:    m.Envelope.Date.Format(time.RFC3339),
		SeqNum:  m.SeqNum,
		Subject: m.Envelope.Subject,
	}
	var addrs []string
	for _, addr := range m.Envelope.From {
		addrs = append(addrs, addr.Address())
	}
	res.From = strings.Join(addrs, ",")
	return res
}

func init() {
	log.SetFlags(0)

	must(initPaths())
}

func errorToExitCode(err error) int {
	if os.IsTimeout(err) {
		return exitUnavailable
	}
	if opErr, ok := err.(*net.OpError); ok {
		if opErr.Temporary() || opErr.Timeout() {
			return exitUnavailable
		}
	}
	return 1
}

func dieOnNetError(v ...interface{}) {
	for _, it := range v {
		switch err := it.(type) {
		case error:
			log.Printf("fatal: dieOnNetError: %T %s", err, err)
			os.Exit(errorToExitCode(err))
		}
	}
}

type nwTimeoutFatalLogger struct{}

func (l *nwTimeoutFatalLogger) Printf(format string, v ...interface{}) {
	dieOnNetError(v...)
	log.Printf(format, v...)
}

func (l *nwTimeoutFatalLogger) Println(v ...interface{}) {
	dieOnNetError(v...)
	log.Println(v...)
}

func initPaths() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	appHomeDir = filepath.Join(homeDir, "."+appName)
	cacheDir = filepath.Join(appHomeDir, "cache")

	for _, dir := range []string{appHomeDir, cacheDir} {
		if err := os.MkdirAll(dir, defaultDirPerms); err != nil {
			return err
		}
	}
	return nil
}

func dialAndLogin(passwd string) (*client.Client, error) {
	dialer := &net.Dialer{Timeout: imapTimeout}
	c, err := client.DialWithDialerTLS(dialer, *addrArg, nil)
	if err != nil {
		return nil, err
	}
	c.Timeout = imapTimeout
	// HACK: go-imap tries to be smart and handle timeouts itself.
	// Wich does not work well for cli usecase.
	// However it reports such erros to custom logger. This logger simply
	// aborts on network timeouts for now.
	c.ErrorLog = &nwTimeoutFatalLogger{}

	if err := c.Login(*userArg, passwd); err != nil {
		return nil, err
	}
	if _, err = c.Select(*mboxArg, false); err != nil {
		return nil, err
	}
	return c, nil
}

func fetchMails(c *client.Client, name string, ids []uint32) ([]*imap.Message, error) {
	if len(ids) < 1 {
		return nil, nil
	}
	if len(ids) > *maxMailFetchCount {
		log.Printf("WARN %s: found %d mails; will fetch %d ",
			name, len(ids), maxMailFetchCount)
		ids = ids[0:*maxMailFetchCount]
	}
	set := &imap.SeqSet{}
	set.AddNum(ids...)
	done := make(chan error, 1)
	msgChan := make(chan *imap.Message, 2)
	messages := make([]*imap.Message, 0, len(ids))
	go func() {
		done <- c.Fetch(set, []imap.FetchItem{imap.FetchEnvelope}, msgChan)
	}()

	for msg := range msgChan {
		messages = append(messages, msg)
	}
	// TODO: add timeout channel here. Otherwise there is a risk of infinite blocking
	if err := <-done; err != nil {
		return nil, fmt.Errorf("%w %T", err, err)
	}
	return messages, nil
}

func fetch() ([]*letter, error) {
	passwd, err := readPassword()
	if err != nil {
		return nil, err
	}
	c, err := dialAndLogin(passwd)
	if err != nil {
		return nil, err
	}
	defer c.Logout()
	q := imap.NewSearchCriteria()
	q.WithoutFlags = []string{imap.SeenFlag}

	ids, err := c.Search(q)
	if err != nil {
		return nil, err
	}
	messages, err := fetchMails(c, fmt.Sprintf("%s@%s/%s", *userArg, *addrArg, *mboxArg), ids)
	if err != nil {
		return nil, err
	}
	letters := []*letter{}
	for _, m := range messages {
		letters = append(letters, letterFromMessage(m))
	}
	return letters, nil
}

func main() {
	flag.Parse()

	letters, err := fetch()
	dieIf(err)

	enc := json.NewEncoder(os.Stdout)
	for _, lt := range letters {
		must(enc.Encode(lt))
	}
}

func readPassword() (string, error) {
	b, err := os.ReadFile(*passwordArg)
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, *passwordArg)
	}
	res := strings.TrimSpace(string(b))
	return res, nil
}

func dieIf(err error) {
	if err != nil {
		log.Fatalf("fatal: %T %s", err, err)
	}
}

func must(err error) { dieIf(err) }
