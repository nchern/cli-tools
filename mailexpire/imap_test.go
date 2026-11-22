package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
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

func testRetentionFile(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	entries := make([]entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, entry{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), id})
	}
	writeTestRetentionEntries(t, dir, entries)
	return filepath.Join(dir, retentionFilename)
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
	steps := []imapStep{{want: `LOGIN "user" "secret"`, reply: "$tag OK [CAPABILITY IMAP4rev1 " + caps + "] logged in\r\n"}}
	if mutate {
		steps = append(steps, imapStep{want: `CAPABILITY`, reply: "* CAPABILITY IMAP4rev1 " + caps + "\r\n$tag OK capabilities\r\n"})
	}
	return steps
}

func imapSelectStep(mailbox string, dryRun bool) imapStep {
	command, mode := "SELECT", "READ-WRITE"
	if dryRun {
		command, mode = "EXAMINE", "READ-ONLY"
	}
	return imapStep{want: command + ` "?` + regexp.QuoteMeta(mailbox) + `"?`, reply: "* 5 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n$tag OK [" + mode + "] selected\r\n"}
}

func imapSearchStep(id, uids string) imapStep {
	return imapStep{want: `UID SEARCH CHARSET UTF-8 HEADER "Message-Id" "` + regexp.QuoteMeta(id) + `"`,
		reply: "* SEARCH " + uids + "\r\n$tag OK search\r\n"}
}

func imapFetchReply(seq, uid int, header string) string {
	return fmt.Sprintf("* %d FETCH (UID %d BODY[HEADER.FIELDS (MESSAGE-ID)] {%d}\r\n%s)\r\n", seq, uid, len(header), header)
}

func imapFetchStep(set, reply string) imapStep {
	return imapStep{want: `UID FETCH ` + set + ` \(UID BODY.PEEK\[HEADER.FIELDS \(Message-ID\)\]\)`, reply: reply + "$tag OK fetch\r\n"}
}

func imapDeleteSteps(set string) []imapStep {
	return []imapStep{
		{want: `UID STORE ` + set + ` \+FLAGS.SILENT \(\\Deleted\)`, reply: "$tag OK stored\r\n"},
		{want: `UID EXPUNGE ` + set, reply: "* 1 EXPUNGE\r\n$tag OK expunged\r\n"},
	}
}

func imapLogoutStep() imapStep {
	return imapStep{want: `LOGOUT`, reply: "* BYE bye\r\n$tag OK logout\r\n"}
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
	if err := cleanIMAPWithTLS(cfg, testRetentionFile(t, id, "<absent@example.org>"), time.Now(), false, &out, trust, time.Second); err != nil {
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
	if err := cleanIMAPWithTLS(cfg, testRetentionFile(t, id), time.Now(), true, &out, trust, time.Second); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("would delete mailbox=%q id=%q uid=42\n", "INBOX", id)
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCleanIMAPRequiresUIDPLUS(t *testing.T) {
	cfg, trust := imapFakeServer(t, "", imapLoginSteps("", true))
	err := cleanIMAPWithTLS(cfg, testRetentionFile(t, "<target@example.org>"), time.Now(), false, io.Discard, trust, time.Second)
	if err == nil || !strings.Contains(err.Error(), "UIDPLUS") {
		t.Fatalf("expected UIDPLUS error, got %v", err)
	}
}

func TestCleanIMAPAbsent(t *testing.T) {
	dir := t.TempDir()
	state := "2000-01-01T00:00:00Z\t<absent@example.org>\n2999-01-01T00:00:00Z\t<future@example.org>\n"
	if err := os.WriteFile(filepath.Join(dir, retentionFilename), []byte(state), privateFileMode); err != nil {
		t.Fatal(err)
	}
	steps := imapLoginSteps("UIDPLUS", true)
	for _, mailbox := range []string{"INBOX", "Archive"} {
		steps = append(steps, imapSelectStep(mailbox, false), imapSearchStep("<absent@example.org>", ""))
	}
	steps = append(steps, imapLogoutStep())
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	cfg.Mailboxes = []string{"INBOX", "Archive"}
	var out bytes.Buffer
	if err := cleanExpired(dir, cfg, false, &out, time.Now(), trust, time.Second); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("absent ID produced output: %q", out.String())
	}
	remaining, err := readRetentionList(dir)
	if err != nil || len(remaining) != 1 || remaining["<future@example.org>"].IsZero() {
		t.Fatalf("remaining=%v, err=%v", remaining, err)
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
			err := cleanIMAPWithTLS(cfg, testRetentionFile(t, id), time.Now(), false, io.Discard, trust, time.Second)
			if err == nil {
				t.Fatal("server failure was not propagated")
			}
			if code := errorToExitCode(err); code != 1 {
				t.Fatalf("IMAP rejection exit code = %d, want 1: %v", code, err)
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
			if err := cleanIMAPWithTLS(cfg, testRetentionFile(t, id), time.Now(), false, io.Discard, trust, time.Second); err == nil {
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
	err := cleanIMAPWithTLS(cfg, testRetentionFile(t, id), time.Now(), false, &out, trust, time.Second)
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
	if err := cleanIMAPWithTLS(cfg, testRetentionFile(t, id), time.Now(), true, imapFailWriter{}, trust, time.Second); err == nil {
		t.Fatal("output failure was not propagated")
	}
}

func TestCleanIMAPReadOnlyMailbox(t *testing.T) {
	step := imapSelectStep("INBOX", false)
	step.reply = strings.ReplaceAll(step.reply, "READ-WRITE", "READ-ONLY")
	steps := append(imapLoginSteps("UIDPLUS", true), step)
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	err := cleanIMAPWithTLS(cfg, testRetentionFile(t, "<target@example.org>"), time.Now(), false, io.Discard, trust, time.Second)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only error, got %v", err)
	}
}

func TestCleanIMAPStalledLiteral(t *testing.T) {
	const childEnv = "MAILEXPIRE_STALLED_LITERAL_DIR"
	if dir := os.Getenv(childEnv); dir != "" {
		id := "<target@example.org>"
		steps := append(imapLoginSteps("UIDPLUS", true), imapSelectStep("INBOX", false), imapSearchStep(id, "42"))
		step := imapFetchStep("42", "")
		step.reply = "* 1 FETCH (UID 42 BODY[HEADER.FIELDS (MESSAGE-ID)] {100}\r\nMessage-ID: "
		steps = append(steps, step)
		cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
		err := cleanExpired(dir, cfg, false, io.Discard, time.Now(), trust, 100*time.Millisecond)
		t.Fatalf("expected fatal logger to exit the subprocess; cleanup returned %v", err)
	}

	dir := t.TempDir()
	state := "2000-01-01T00:00:00Z\t<target@example.org>\n2999-01-01T00:00:00Z\t<future@example.org>\n"
	if err := os.WriteFile(filepath.Join(dir, retentionFilename), []byte(state), privateFileMode); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCleanIMAPStalledLiteral$")
	// The child exits without running test cleanups; keep its fixtures under
	// the parent's temporary directory so the parent removes them afterward.
	cmd.Env = append(os.Environ(), childEnv+"="+dir, "TMPDIR="+dir)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("stalled literal exceeded subprocess deadline: %v\n%s", ctx.Err(), output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 69 {
		t.Fatalf("expected exit 69, got %v\n%s", err, output)
	}
	if readState(t, dir) != state {
		t.Fatal("stalled literal changed the retention list")
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
	if err := cleanIMAP(cfg, testRetentionFile(t, "<target@example.org>"), time.Now(), false, io.Discard); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected certificate verification failure, got %v", err)
	}
	<-done
}

func TestCleanIMAPPasswordReadFailure(t *testing.T) {
	cfg := config{Pass: filepath.Join(t.TempDir(), "missing")}
	if err := cleanIMAP(cfg, testRetentionFile(t, "<target@example.org>"), time.Now(), false, io.Discard); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("expected password read error, got %v", err)
	}
}

func TestCleanIMAPRetentionReadError(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry-run=%v", dryRun), func(t *testing.T) {
			id := "<absent@example.org>"
			steps := append(imapLoginSteps("UIDPLUS", !dryRun), imapSelectStep("INBOX", dryRun), imapSearchStep(id, ""))
			cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
			path := filepath.Join(t.TempDir(), retentionFilename)
			writeConfig(t, path, "2000-01-01T00:00:00Z\t"+id+"\ninvalid record\n")
			if err := cleanIMAPWithTLS(cfg, path, time.Now(), dryRun, io.Discard, trust, time.Second); err == nil {
				t.Fatal("malformed retention record was not propagated")
			}
		})
	}
}
