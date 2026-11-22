package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func commandHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".mailexpire")
}

func readState(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "retention.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRegistration(t *testing.T) {
	dir := commandHome(t)
	before := time.Now().UTC()
	args := []string{"-config", "/does/not/exist", "-r", "10d", "<b@example.org>", "<a@example.org>"}
	if err := run(args, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	q, err := readRetentionList(dir)
	if err != nil || len(q) != 2 {
		t.Fatalf("retention list=%v err=%v", q, err)
	}
	for _, expires := range q {
		if expires.Before(before.Add(240*time.Hour).Truncate(time.Second)) || expires.After(time.Now().Add(240*time.Hour).Truncate(time.Second)) {
			t.Fatalf("unexpected expiration: %v", expires)
		}
	}
	text := readState(t, dir)
	if !strings.Contains(text, "\t<a@example.org>\n") || !strings.Contains(text, "\t<b@example.org>\n") {
		t.Fatalf("unexpected TSV: %s", text)
	}
	if err := run([]string{"-r", "1h", "<a@example.org>"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	next, err := readRetentionList(dir)
	if err != nil || len(next) != 2 || !next["<a@example.org>"].Before(q["<a@example.org>"]) || !next["<b@example.org>"].Equal(q["<b@example.org>"]) {
		t.Fatalf("re-registration: %v, %v", next, err)
	}
	for path, want := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "retention.tsv"): 0600, filepath.Join(dir, "retention.lock"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions %s: %v %v", path, info, err)
		}
	}
}

func TestInvalidArgumentsDoNotWrite(t *testing.T) {
	dir := commandHome(t)
	cases := [][]string{
		{}, {"-r", "10d"}, {"-r", "0d", "<a>"}, {"-r", "-1d", "<a>"},
		{"-r", "1.5d", "<a>"}, {"-r", "1m", "<a>"}, {"-r", "1", "<a>"},
		{"-r", "999999999999999999w", "<a>"},
		{"-clean", "<a>"}, {"-clean", "-r", "1d"},
		{"-dry-run", "-r", "1d", "<a>"}, {"-unknown"},
		{"-r", "1d", "<" + strings.Repeat("a", 60*1024) + ">"},
	}
	for _, args := range cases {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid command wrote app directory: %v", err)
	}
	var help bytes.Buffer
	if err := run([]string{"-h"}, io.Discard, &help); err != nil || !strings.Contains(help.String(), "-config") {
		t.Fatalf("help: %s, %v", help.String(), err)
	}
}

func TestRegistrationUnits(t *testing.T) {
	dir := commandHome(t)
	for value, duration := range map[string]time.Duration{"2h": 2 * time.Hour, "3d": 72 * time.Hour, "2w": 336 * time.Hour} {
		start := time.Now()
		if err := run([]string{"-r", value, "<a>"}, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		q, err := readRetentionList(dir)
		if err != nil || q["<a>"].Before(start.Add(duration).Truncate(time.Second)) || q["<a>"].After(time.Now().Add(duration).Truncate(time.Second)) {
			t.Fatalf("%s: %v %v", value, q, err)
		}
	}
}

func TestRegistrationIDLength(t *testing.T) {
	dir := commandHome(t)
	for _, id := range []string{"", "bare-id", "<@>", "<id with spaces>", strings.Repeat("a", 60*1024)} {
		if err := run([]string{"-r", "1d", id}, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		q, err := readRetentionList(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := q[id]; !exists {
			t.Fatalf("registered ID missing (length %d)", len(id))
		}
	}
	original := readState(t, dir)
	if err := run([]string{"-r", "1d", strings.Repeat("a", 60*1024+1)}, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted ID over the length limit")
	}
	if readState(t, dir) != original {
		t.Fatal("length validation failure changed the retention list")
	}
}

func TestMalformedStatePreserved(t *testing.T) {
	dir := commandHome(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"invalid\n", "not-a-date\t<a>\n", "2026-11-21T01:03:25\t<a>\n",
		"2026-01-01T00:00:00Z\t" + strings.Repeat("a", 60*1024+1) + "\n",
		"2026-01-01T00:00:00Z <a>\n", "2026-01-01T00:00:00Z\t\t<a>\n",
		"2026-01-01T00:00:00Z\t<a>\textra\n",
		strings.Repeat("x", 70000),
	} {
		if err := os.WriteFile(filepath.Join(dir, "retention.tsv"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"-r", "1d", "<new>"}, io.Discard, io.Discard); err == nil {
			t.Fatal("accepted corrupt retention list")
		}
		if got := readState(t, dir); got != text {
			t.Fatal("modified corrupt retention list")
		}
	}
}

func TestConcurrentRegistrations(t *testing.T) {
	dir := commandHome(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if err := run([]string{"-r", "1d", fmt.Sprintf("<%d@example.org>", i)}, io.Discard, io.Discard); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	q, err := readRetentionList(dir)
	if err != nil || len(q) != 20 {
		t.Fatalf("lost registrations: %d, %v", len(q), err)
	}
}

func TestCleanupRetentionListContract(t *testing.T) {
	// Retention list preservation after partial IMAP failure is a critical safety contract.
	for _, mode := range []string{"success", "all-expired", "failure", "dry-run", "not-expired"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Date(2026, 11, 21, 1, 3, 25, 0, time.UTC)
			cutoff := now.In(time.FixedZone("UTC-05:30", -19800))
			entries := []entry{{now.Add(-time.Second), "<before>"}, {now, "<boundary>"}, {now.Add(time.Second), "<future>"}}
			if mode == "not-expired" {
				entries = entries[2:]
			} else if mode == "all-expired" {
				entries = entries[:2]
			}
			writeTestRetentionEntries(t, dir, entries)
			original := readState(t, dir)
			var cfg config
			var trust *tls.Config
			if mode != "not-expired" {
				dry := mode == "dry-run"
				steps := append(imapLoginSteps("UIDPLUS", !dry), imapSelectStep("INBOX", dry),
					imapSearchStep("<before>", ""), imapSearchStep("<boundary>", ""))
				if mode == "failure" {
					steps = append(steps, imapStep{want: `SELECT "?Archive"?`, reply: "$tag NO unavailable\r\n"})
				} else {
					steps = append(steps, imapLogoutStep())
				}
				cfg, trust = imapFakeServer(t, "UIDPLUS", steps)
				if mode == "failure" {
					cfg.Mailboxes = []string{"INBOX", "Archive"}
				}
			}
			err := cleanExpired(dir, cfg, mode == "dry-run", io.Discard, cutoff, trust, time.Second)
			if (err != nil) != (mode == "failure") {
				t.Fatalf("cleanup error=%v", err)
			}
			if mode != "success" && mode != "all-expired" {
				if readState(t, dir) != original {
					t.Fatal("retention list changed without successful deletion")
				}
				return
			}
			remaining, err := readRetentionList(dir)
			if mode == "all-expired" {
				if err != nil || len(remaining) != 0 {
					t.Fatalf("remaining=%v err=%v", remaining, err)
				}
				return
			}
			if err != nil || len(remaining) != 1 || !remaining["<future>"].Equal(now.Add(time.Second)) {
				t.Fatalf("remaining=%v err=%v", remaining, err)
			}
		})
	}
}
