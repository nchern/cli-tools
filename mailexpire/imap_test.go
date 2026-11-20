package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type imapStep struct {
	want  string
	reply string
}

func imapTestTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "IMAP test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
		&tls.Config{RootCAs: roots}
}

func imapPasswordFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(" \tsecret\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func imapFakeServer(t *testing.T, caps string, steps []imapStep) (config, *tls.Config) {
	t.Helper()
	serverTLS, clientTLS := imapTestTLS(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			done <- err
			return
		}
		done <- imapServeScript(conn, caps, steps)
	}()
	t.Cleanup(func() {
		listener.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return config{Addr: listener.Addr().String(), User: "user", Pass: imapPasswordFile(t), Mailboxes: []string{"INBOX"}}, clientTLS
}

func imapServeScript(conn net.Conn, caps string, steps []imapStep) error {
	if _, err := fmt.Fprintf(conn, "* OK [CAPABILITY IMAP4rev1 %s] ready\r\n", caps); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	for _, step := range steps {
		line, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("waiting for %s: %w", step.want, err)
		}
		parts := strings.SplitN(strings.TrimSuffix(line, "\r\n"), " ", 2)
		if len(parts) != 2 || !regexp.MustCompile("^(?:"+step.want+")$").MatchString(parts[1]) {
			return fmt.Errorf("expected command %s, got %q", step.want, line)
		}
		if _, err := io.WriteString(conn, strings.ReplaceAll(step.reply, "$tag", parts[0])); err != nil {
			return err
		}
	}
	line, err := r.ReadString('\n')
	if err != io.EOF {
		return fmt.Errorf("expected disconnect, got %q, %v", line, err)
	}
	return nil
}

func imapLoginSteps(caps string, mutate bool) []imapStep {
	steps := []imapStep{{`LOGIN "user" "secret"`, "$tag OK [CAPABILITY IMAP4rev1 " + caps + "] logged in\r\n"}}
	if mutate {
		steps = append(steps, imapStep{`CAPABILITY`, "* CAPABILITY IMAP4rev1 " + caps + "\r\n$tag OK capabilities\r\n"})
	}
	return steps
}

func imapSelectStep(mailbox string, dryRun bool) imapStep {
	command, mode := "SELECT", "READ-WRITE"
	if dryRun {
		command, mode = "EXAMINE", "READ-ONLY"
	}
	return imapStep{command + ` "?` + regexp.QuoteMeta(mailbox) + `"?`, "* 5 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n$tag OK [" + mode + "] selected\r\n"}
}

func imapSearchStep(id, uids string) imapStep {
	return imapStep{`UID SEARCH CHARSET UTF-8 HEADER "Message-Id" "` + regexp.QuoteMeta(id) + `"`,
		"* SEARCH " + uids + "\r\n$tag OK search\r\n"}
}

func imapFetchReply(seq, uid int, header string) string {
	return fmt.Sprintf("* %d FETCH (UID %d BODY[HEADER.FIELDS (MESSAGE-ID)] {%d}\r\n%s)\r\n", seq, uid, len(header), header)
}

func imapFetchStep(set, reply string) imapStep {
	return imapStep{`UID FETCH ` + set + ` \(UID BODY.PEEK\[HEADER.FIELDS \(Message-ID\)\]\)`, reply + "$tag OK fetch\r\n"}
}

func imapDeleteSteps(set string) []imapStep {
	return []imapStep{
		{`UID STORE ` + set + ` \+FLAGS.SILENT \(\\Deleted\)`, "$tag OK stored\r\n"},
		{`UID EXPUNGE ` + set, "* 1 EXPUNGE\r\n$tag OK expunged\r\n"},
	}
}

func imapLogoutStep() imapStep {
	return imapStep{`LOGOUT`, "* BYE bye\r\n$tag OK logout\r\n"}
}

func TestCleanIMAPExactAcrossMailboxes(t *testing.T) {
	id := "<target@example.org>"
	steps := imapLoginSteps("UIDPLUS", true)
	for _, mailbox := range []string{"INBOX", "Archive"} {
		steps = append(steps, imapSelectStep(mailbox, false), imapSearchStep(id, "11 22 33 44 55"))
		reply := imapFetchReply(1, 11, "Message-ID: <prefix"+id+">\r\n\r\n")
		reply += imapFetchReply(2, 22, "Message-ID: "+id+"\r\n\r\n")
		reply += imapFetchReply(3, 33, "Message-ID: "+id+".suffix\r\n\r\n")
		reply += imapFetchReply(4, 44, "Message-ID: "+id+"\r\nMessage-ID: <other@example.org>\r\n\r\n")
		reply += imapFetchReply(5, 55, "Message-ID:\r\n\t"+id+"\r\n\r\n")
		steps = append(steps, imapFetchStep("11,22,33,44,55", reply))
		steps = append(steps, imapDeleteSteps("22,55")...)
		steps = append(steps, imapSearchStep("<absent@example.org>", ""))
	}
	steps = append(steps, imapLogoutStep())
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	cfg.Mailboxes = []string{"INBOX", "Archive"}
	var out bytes.Buffer
	if err := cleanIMAPWithTLS(cfg, []string{id, "<absent@example.org>"}, false, &out, trust, time.Second); err != nil {
		t.Fatal(err)
	}
	for _, mailbox := range cfg.Mailboxes {
		for _, uid := range []int{22, 55} {
			want := fmt.Sprintf("deleted mailbox=%q id=%q uid=%d\n", mailbox, id, uid)
			if !strings.Contains(out.String(), want) {
				t.Errorf("output %q missing %q", out.String(), want)
			}
		}
	}
	if strings.Count(out.String(), "\n") != 4 {
		t.Fatalf("unexpected deletion output: %q", out.String())
	}
}

func TestCleanIMAPDryRunWithoutUIDPLUS(t *testing.T) {
	id := "<target@example.org>"
	steps := append(imapLoginSteps("", false), imapSelectStep("INBOX", true), imapSearchStep(id, "42"),
		imapFetchStep("42", imapFetchReply(1, 42, "Message-ID: "+id+"\r\n\r\n")), imapLogoutStep())
	cfg, trust := imapFakeServer(t, "", steps)
	var out bytes.Buffer
	if err := cleanIMAPWithTLS(cfg, []string{id}, true, &out, trust, time.Second); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("would delete mailbox=%q id=%q uid=42\n", "INBOX", id)
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCleanIMAPRequiresUIDPLUS(t *testing.T) {
	cfg, trust := imapFakeServer(t, "", imapLoginSteps("", true))
	err := cleanIMAPWithTLS(cfg, []string{"<target@example.org>"}, false, io.Discard, trust, time.Second)
	if err == nil || !strings.Contains(err.Error(), "UIDPLUS") {
		t.Fatalf("expected UIDPLUS error, got %v", err)
	}
}

func TestCleanIMAPAbsent(t *testing.T) {
	steps := imapLoginSteps("UIDPLUS", true)
	for _, mailbox := range []string{"INBOX", "Archive"} {
		steps = append(steps, imapSelectStep(mailbox, false), imapSearchStep("<absent@example.org>", ""))
	}
	steps = append(steps, imapLogoutStep())
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	cfg.Mailboxes = []string{"INBOX", "Archive"}
	var out bytes.Buffer
	if err := cleanIMAPWithTLS(cfg, []string{"<absent@example.org>"}, false, &out, trust, time.Second); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("absent ID produced output: %q", out.String())
	}
}

func TestCleanIMAPCommandFailures(t *testing.T) {
	id := "<target@example.org>"
	success := append(imapLoginSteps("UIDPLUS", true), imapSelectStep("INBOX", false), imapSearchStep(id, "42"),
		imapFetchStep("42", imapFetchReply(1, 42, "Message-ID: "+id+"\r\n\r\n")))
	success = append(success, imapDeleteSteps("42")...)
	success = append(success, imapLogoutStep())
	for i, step := range success {
		t.Run(step.want, func(t *testing.T) {
			steps := append([]imapStep(nil), success[:i+1]...)
			steps[i].reply = "$tag NO rejected\r\n"
			cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
			err := cleanIMAPWithTLS(cfg, []string{id}, false, io.Discard, trust, time.Second)
			if err == nil {
				t.Fatal("server failure was not propagated")
			}
		})
	}
}

func TestCleanIMAPFetchFailures(t *testing.T) {
	id := "<target@example.org>"
	for _, header := range []string{"malformed\r\n\r\n", ""} {
		t.Run(fmt.Sprintf("header=%q", header), func(t *testing.T) {
			reply := imapFetchReply(1, 42, header)
			if header == "" {
				reply = "* 1 FETCH (UID 42)\r\n"
			}
			steps := append(imapLoginSteps("UIDPLUS", true), imapSelectStep("INBOX", false),
				imapSearchStep(id, "42"), imapFetchStep("42", reply))
			cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
			if err := cleanIMAPWithTLS(cfg, []string{id}, false, io.Discard, trust, time.Second); err == nil {
				t.Fatal("invalid header response was accepted")
			}
		})
	}
}

func TestCleanIMAPLaterMailboxFailure(t *testing.T) {
	id := "<target@example.org>"
	steps := append(imapLoginSteps("UIDPLUS", true), imapSelectStep("INBOX", false), imapSearchStep(id, "42"),
		imapFetchStep("42", imapFetchReply(1, 42, "Message-ID: "+id+"\r\n\r\n")))
	steps = append(steps, imapDeleteSteps("42")...)
	step := imapSelectStep("Archive", false)
	step.reply = "$tag NO unavailable\r\n"
	steps = append(steps, step)
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	cfg.Mailboxes = []string{"INBOX", "Archive"}
	var out bytes.Buffer
	err := cleanIMAPWithTLS(cfg, []string{id}, false, &out, trust, time.Second)
	if err == nil || !strings.Contains(err.Error(), "Archive") {
		t.Fatalf("later mailbox failure was not propagated: %v", err)
	}
	if !strings.Contains(out.String(), "deleted mailbox=\"INBOX\"") {
		t.Fatalf("missing completed deletion: %q", out.String())
	}
}

type imapFailWriter struct{}

func (imapFailWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestCleanIMAPOutputFailure(t *testing.T) {
	id := "<target@example.org>"
	steps := append(imapLoginSteps("", false), imapSelectStep("INBOX", true), imapSearchStep(id, "42"),
		imapFetchStep("42", imapFetchReply(1, 42, "Message-ID: "+id+"\r\n\r\n")))
	cfg, trust := imapFakeServer(t, "", steps)
	if err := cleanIMAPWithTLS(cfg, []string{id}, true, imapFailWriter{}, trust, time.Second); err == nil {
		t.Fatal("output failure was not propagated")
	}
}

func TestCleanIMAPReadOnlyMailbox(t *testing.T) {
	step := imapSelectStep("INBOX", false)
	step.reply = strings.ReplaceAll(step.reply, "READ-WRITE", "READ-ONLY")
	steps := append(imapLoginSteps("UIDPLUS", true), step)
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	err := cleanIMAPWithTLS(cfg, []string{"<target@example.org>"}, false, io.Discard, trust, time.Second)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only error, got %v", err)
	}
}

func TestCleanIMAPStalledLiteral(t *testing.T) {
	id := "<target@example.org>"
	steps := append(imapLoginSteps("UIDPLUS", true), imapSelectStep("INBOX", false), imapSearchStep(id, "42"))
	step := imapFetchStep("42", "")
	step.reply = "* 1 FETCH (UID 42 BODY[HEADER.FIELDS (MESSAGE-ID)] {100}\r\nMessage-ID: "
	steps = append(steps, step)
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	start := time.Now()
	if err := cleanIMAPWithTLS(cfg, []string{id}, false, io.Discard, trust, 100*time.Millisecond); err == nil {
		t.Fatal("stalled literal did not fail")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stalled read took %s", elapsed)
	}
}

func TestCleanIMAPVerifiesTLS(t *testing.T) {
	serverTLS, _ := imapTestTLS(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(time.Second))
			conn.(*tls.Conn).Handshake()
		}
	}()
	cfg := config{Addr: listener.Addr().String(), Pass: imapPasswordFile(t)}
	if err := cleanIMAP(cfg, nil, false, io.Discard); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected certificate verification failure, got %v", err)
	}
	<-done
}

func TestCleanIMAPPasswordReadFailure(t *testing.T) {
	cfg := config{Pass: filepath.Join(t.TempDir(), "missing")}
	if err := cleanIMAP(cfg, nil, false, io.Discard); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("expected password read error, got %v", err)
	}
}
