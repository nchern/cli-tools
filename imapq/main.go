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

type dateFlag time.Time

func (d *dateFlag) String() string {
	if d == nil || time.Time(*d) == zeroTime {
		return "empty"
	}
	return fmt.Sprintf("%s", time.Time(*d))
}

func (d *dateFlag) Set(value string) error {
	var v time.Time
	var err error
	for _, ft := range supportedFormats {
		v, err = time.Parse(ft, value)
		if err == nil {
			*d = dateFlag(v)
			return nil
		}
	}
	return fmt.Errorf("%s: date in unsupported format", value)
}

type imapFlags map[string]string

func (s *imapFlags) String() string {
	if s == nil {
		return "none"
	}
	var flags []string
	for k, _ := range *s {
		flags = append(flags, k)
	}
	return strings.Join(flags, ", ")
}

func (s *imapFlags) Set(value string) error {
	v, found := strToIMAPFlags[value]
	if !found {
		return fmt.Errorf("%s: unknown flag", value)
	}
	map[string]string(*s)[value] = v
	return nil
}

var (
	zeroTime         = time.UnixMicro(0)
	supportedFormats = []string{
		time.RFC822,
		time.RFC1123,
		time.RFC3339,
		"2006-01-02",
	}

	strToIMAPFlags = map[string]string{
		//S — Seen (read)
		"S": imap.SeenFlag,
		//R — Replied
		"R": imap.AnsweredFlag,
		// F — Flagged (important)
		"F": imap.FlaggedFlag,
		//T — Trashed
		"T": imap.DeletedFlag,
		// D — Draft
		"D": imap.DraftFlag,
		// P — Passed (forwarded)
		"P": imap.RecentFlag,
	}

	appHomeDir string
	cacheDir   string

	// CLI args
	addrArg           = flag.String("addr", "imap.gmail.com:993", "IMAP server address")
	mboxArg           = flag.String("mailbox", "INBOX", "mailbox on the server")
	passwordArg       = flag.String("pass", "", "IMAP password")
	userArg           = flag.String("user", "", "IMAP user")
	maxMailFetchCount = flag.Int("m", 100, "Maximum number of messages to fetch")
	// search criteria flags
	since   = dateFlag(zeroTime)
	with    = imapFlags{}
	without = imapFlags{}
)

func supporedIMAPFlags() string {
	var res []string
	for k, v := range strToIMAPFlags {
		res = append(res, fmt.Sprintf("- %s - %s", k, strings.TrimPrefix(v, "\\")))
	}
	return strings.Join(res, "\n")
}

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
	sinceHelp := "fetch messages later than this date. Supported formats:\n" +
		"- " + strings.Join(supportedFormats, "\n- ")
	flag.Var(&since, "since", sinceHelp)
	without["S"] = strToIMAPFlags["S"]
	flag.Var(&without, "without",
		"fetch messages _without_ specified flags; supports multiple args; available flags:\n"+
			supporedIMAPFlags())
	flag.Var(&with, "with",
		"fetch messages _with_ specified flags; supports multiple args; available flags:\n"+
			supporedIMAPFlags())

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
			dieIf(err)
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

func fetch(with map[string]string, without map[string]string, since time.Time) ([]*letter, error) {
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
	for _, v := range without {
		q.WithoutFlags = append(q.WithoutFlags, v)
	}
	for _, v := range with {
		q.WithFlags = append(q.WithFlags, v)
	}
	if since != zeroTime {
		q.Since = since
	}
	ids, err := c.Search(q)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%s@%s/%s", *userArg, *addrArg, *mboxArg)
	messages, err := fetchMails(c, name, ids)
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

	letters, err := fetch(with, without, time.Time(since))
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
		log.Printf("fatal: %T %s", err, err)
		os.Exit(errorToExitCode(err))
	}
}

func must(err error) { dieIf(err) }
