package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const (
	imapTimeout       = 20 * time.Second
	uidPlusCapability = "UIDPLUS"
	uidExpungeCommand = "UID EXPUNGE"
	messageIDHeader   = "Message-ID"
)

func cleanIMAP(cfg config, retentionPath string, cutoff time.Time, dryRun bool, out io.Writer) error {
	return cleanIMAPWithTLS(cfg, retentionPath, cutoff, dryRun, out, nil, imapTimeout)
}

// The TLS configuration and timeout are injectable for local protocol tests.
func cleanIMAPWithTLS(cfg config, retentionPath string, cutoff time.Time, dryRun bool, out io.Writer, tlsConfig *tls.Config, timeout time.Duration) error {
	pass, err := os.ReadFile(cfg.Pass)
	if err != nil {
		return fmt.Errorf("read IMAP password: %w", err)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", cfg.Addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("connect IMAP: %w", err)
	}
	defer conn.Close()
	if err := resetIMAPDeadline(conn, timeout); err != nil {
		return err
	}
	c, err := client.New(conn)
	if err != nil {
		return fmt.Errorf("IMAP greeting: %w", err)
	}
	c.Timeout = timeout
	c.ErrorLog = imapErrorLogger{}
	if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
		return err
	}
	if err := c.Login(cfg.User, strings.TrimSpace(string(pass))); err != nil {
		return fmt.Errorf("IMAP login: %w", err)
	}
	if !dryRun {
		if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
			return err
		}
		caps, err := c.Capability()
		if err != nil {
			return err
		}
		if !caps[uidPlusCapability] {
			return fmt.Errorf("IMAP server does not support %s", uidPlusCapability)
		}
	}
	for _, mailbox := range cfg.Mailboxes {
		if err := cleanMailbox(c, conn, mailbox, retentionPath, cutoff, dryRun, out); err != nil {
			return fmt.Errorf("mailbox %q: %w", mailbox, err)
		}
	}
	if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
		return err
	}
	return c.Logout()
}

func resetIMAPDeadline(conn net.Conn, timeout time.Duration) error {
	// Client.Timeout alone does not bound a stalled response literal read.
	return conn.SetDeadline(time.Now().Add(timeout))
}

func cleanMailbox(c *client.Client, conn net.Conn, mailbox, retentionPath string, cutoff time.Time, dryRun bool, out io.Writer) error {
	if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
		return err
	}
	status, err := c.Select(mailbox, dryRun)
	if err != nil {
		return err
	}
	if !dryRun && status.ReadOnly {
		return fmt.Errorf("mailbox is read-only")
	}
	f, err := os.Open(retentionPath)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for {
		e, err := readRetentionEntry(scanner)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if e.ExpiresAt.After(cutoff) {
			break
		}
		id := e.MessageID
		uids, err := matchingUIDs(c, conn, id)
		if err != nil {
			return fmt.Errorf("message %q: %w", id, err)
		}
		if len(uids) == 0 {
			continue
		}
		if !dryRun {
			if err := deleteUIDs(c, conn, uids); err != nil {
				return fmt.Errorf("delete message %q: %w", id, err)
			}
		}
		action := "deleted"
		if dryRun {
			action = "would delete"
		}
		for _, uid := range uids {
			if _, err := fmt.Fprintf(out, "%s mailbox=%q id=%q uid=%d\n", action, mailbox, id, uid); err != nil {
				return err
			}
		}
	}
	return nil
}

func matchingUIDs(c *client.Client, conn net.Conn, id string) ([]uint32, error) {
	criteria := imap.NewSearchCriteria()
	criteria.Header.Set(messageIDHeader, id)
	if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
		return nil, err
	}
	candidates, err := c.UidSearch(criteria)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	set := &imap.SeqSet{}
	set.AddNum(candidates...)
	section := &imap.BodySectionName{
		Peek: true,
		BodyPartName: imap.BodyPartName{
			Specifier: imap.HeaderSpecifier,
			Fields:    []string{messageIDHeader},
		},
	}
	messages := make(chan *imap.Message, len(candidates))
	if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
		return nil, err
	}
	if err := c.UidFetch(set, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, messages); err != nil {
		return nil, err
	}
	var matches []uint32
	for msg := range messages {
		body := msg.GetBody(section)
		if msg.Uid == 0 || !set.Contains(msg.Uid) || body == nil {
			return nil, fmt.Errorf("incomplete IMAP header response")
		}
		header, err := mail.ReadMessage(body)
		if err != nil {
			return nil, fmt.Errorf("read Message-ID header: %w", err)
		}
		values := textproto.MIMEHeader(header.Header).Values(messageIDHeader)
		if len(values) == 1 && strings.TrimSpace(values[0]) == id {
			matches = append(matches, msg.Uid)
		}
	}
	return matches, nil
}

func deleteUIDs(c *client.Client, conn net.Conn, uids []uint32) error {
	set := &imap.SeqSet{}
	set.AddNum(uids...)
	if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
		return err
	}
	if err := c.UidStore(set, imap.FormatFlagsOp(imap.AddFlags, true), []any{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	if err := resetIMAPDeadline(conn, c.Timeout); err != nil {
		return err
	}
	status, err := c.Execute(&imap.Command{
		Name: uidExpungeCommand, Arguments: []any{set},
	}, nil)
	if err != nil {
		return err
	}
	return status.Err()
}
