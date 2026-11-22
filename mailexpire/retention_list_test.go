package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readRetentionList(dir string) (map[string]time.Time, error) {
	entries := make(map[string]time.Time)
	f, err := os.Open(filepath.Join(dir, retentionFilename))
	if os.IsNotExist(err) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for {
		e, err := readRetentionEntry(scanner)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entries[e.MessageID] = e.ExpiresAt
	}
	return entries, nil
}

func writeTestRetentionEntries(t *testing.T, dir string, entries []entry) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, retentionFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, e := range entries {
		if err := e.Write(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestReadRetentionEntry(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("# comment\r\n\r\n2026-02-01T00:00:00Z\t<first>\r\n\n# comment\n2000-01-01T00:00:00Z\t<second>\n# end\n"))
	for _, want := range []entry{
		{time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "<first>"},
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), "<second>"},
	} {
		got, err := readRetentionEntry(scanner)
		if err != nil || got.MessageID != want.MessageID || !got.ExpiresAt.Equal(want.ExpiresAt) {
			t.Fatalf("entry=%+v, err=%v, want %+v", got, err, want)
		}
	}
	for range 2 {
		if _, err := readRetentionEntry(scanner); err != io.EOF {
			t.Fatalf("exhausted scanner error=%v, want EOF", err)
		}
	}
}

func TestRetentionFileFormat(t *testing.T) {
	dir := commandHome(t)
	if err := os.MkdirAll(dir, privateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	input := "# existing retention list\r\n\r\n2026-11-28T14:00:00.987654321+02:00\t<old@example.org>\r\n"
	if err := os.WriteFile(filepath.Join(dir, "retention.tsv"), []byte(input), privateFileMode); err != nil {
		t.Fatal(err)
	}
	existing, err := readRetentionList(dir)
	if err != nil || !existing["<old@example.org>"].Equal(time.Date(2026, 11, 28, 12, 0, 0, 987654321, time.UTC)) {
		t.Fatalf("failed to read fractional timestamp with offset: %v, %v", existing, err)
	}
	if err := run([]string{"-r", "1d", "<new@example.org>"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	output := readState(t, dir)
	if !strings.HasPrefix(output, "# mailexpire retention v1\n# expires-at\tmessage-id\n") {
		t.Fatalf("missing format header: %q", output)
	}
	if !strings.Contains(output, "2026-11-28T12:00:00Z\t<old@example.org>\n") {
		t.Fatalf("existing expiration was not truncated to whole-second UTC: %q", output)
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if !strings.HasPrefix(line, "#") && strings.Count(line, "\t") != 1 {
			t.Fatalf("expected exactly two tab-separated fields: %q", line)
		}
		if !strings.HasPrefix(line, "#") {
			stamp := strings.Split(line, "\t")[0]
			if strings.Contains(stamp, ".") || !strings.HasSuffix(stamp, "Z") {
				t.Fatalf("expected whole-second UTC timestamp: %q", stamp)
			}
		}
	}
}

func TestRegistrationWaitsForLock(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "registration"
		if existing {
			name = "re-registration"
		}
		t.Run(name, func(t *testing.T) {
			dir := commandHome(t)
			args := []string{"-r", "1h", "<waiting@example.org>"}
			if existing {
				if err := run([]string{"-r", "1d", "<waiting@example.org>"}, io.Discard, io.Discard); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := lockRetentionList(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			done := make(chan error, 1)
			go func() { done <- run(args, io.Discard, io.Discard) }()
			select {
			case err := <-done:
				t.Fatalf("registration completed while lock was held: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			released := time.Now()
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("registration did not resume after lock release")
			}
			q, err := readRetentionList(dir)
			if err != nil {
				t.Fatal(err)
			}
			if expires := q["<waiting@example.org>"]; expires.Before(released.Add(time.Hour).Truncate(time.Second)) {
				t.Fatalf("lock wait consumed retention: released=%s expiration=%s", released, expires)
			}
		})
	}
}

func TestRetentionRenameFailure(t *testing.T) {
	// Exercise the atomic replacement failure directly: CLI parsing would reject
	// this destination before reaching the writer.
	dir := t.TempDir()
	target := filepath.Join(dir, "retention.tsv")
	if err := os.Mkdir(target, privateDirectoryMode); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "untouched")
	if err := os.WriteFile(sentinel, []byte("original"), privateFileMode); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(dir, ".retention-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	w := bufio.NewWriter(f)
	if err := (entry{time.Now(), "<new@example.org>"}).Write(w); err != nil {
		t.Fatal(err)
	}
	if err := commitRetentionList(dir, f, w); err == nil {
		t.Fatal("replacement of a directory unexpectedly succeeded")
	}
	if err := os.Remove(f.Name()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "original" {
		t.Fatalf("failed replacement changed the destination: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "retention.tsv" {
		t.Fatalf("failed replacement left temporary files: %v, %v", entries, err)
	}
}

func TestRegistrationMergesSortedEntries(t *testing.T) {
	for _, position := range []string{"before", "between", "after", "equal"} {
		t.Run(position, func(t *testing.T) {
			dir := t.TempDir()
			target := time.Now().Add(time.Hour).Truncate(time.Second)
			offsets := map[string][]time.Duration{
				"before": {time.Hour, 2 * time.Hour}, "between": {-time.Hour, time.Hour},
				"after": {-2 * time.Hour, -time.Hour}, "equal": {0, 0},
			}[position]
			existing := []entry{{target.Add(offsets[0]), "<z>"}, {target.Add(offsets[1]), "<a>"}}
			writeTestRetentionEntries(t, dir, existing)
			if err := register(dir, []string{"<new>", "<other>", "<new>"}, time.Until(target)); err != nil {
				t.Fatal(err)
			}
			want := map[string]time.Time{"<z>": existing[0].ExpiresAt, "<a>": existing[1].ExpiresAt, "<new>": target, "<other>": target}
			assertRetentionEntries(t, dir, want)
		})
	}
}

func assertRetentionEntries(t *testing.T, dir string, want map[string]time.Time) {
	t.Helper()
	seen := make(map[string]bool)
	var previous time.Time
	f, err := os.Open(filepath.Join(dir, retentionFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for {
		e, err := readRetentionEntry(scanner)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.ExpiresAt.Before(previous) {
			t.Fatalf("expiration order regressed: %s after %s", e.ExpiresAt, previous)
		}
		if seen[e.MessageID] || !e.ExpiresAt.Equal(want[e.MessageID]) {
			t.Fatalf("duplicate or unexpected entry: %+v, want %v", e, want)
		}
		seen[e.MessageID] = true
		previous = e.ExpiresAt
	}
	if len(seen) != len(want) {
		t.Fatalf("entries=%v, want %v", seen, want)
	}
}

func TestRegistrationMovesExistingExpiration(t *testing.T) {
	for _, offset := range []time.Duration{-time.Hour, time.Hour} {
		t.Run(offset.String(), func(t *testing.T) {
			dir := t.TempDir()
			target := time.Now().Add(2 * time.Hour).Truncate(time.Second)
			existing := []entry{
				{target.Add(-2 * time.Hour), "<before>"},
				{target.Add(offset), "<registered>"},
				{target.Add(2 * time.Hour), "<after>"},
			}
			writeTestRetentionEntries(t, dir, existing)
			if err := register(dir, []string{"<registered>", "<registered>"}, time.Until(target)); err != nil {
				t.Fatal(err)
			}
			assertRetentionEntries(t, dir, map[string]time.Time{
				"<before>": existing[0].ExpiresAt, "<registered>": target, "<after>": existing[2].ExpiresAt,
			})
		})
	}
}

func TestRetentionReadFailurePreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	original := "# original\n2000-01-01T00:00:00Z\t<original>\ninvalid record\n"
	writeConfig(t, filepath.Join(dir, retentionFilename), original)
	if err := rewriteRetentionList(dir, []string{"<replacement>"}, time.Now()); err == nil {
		t.Fatal("rewrite accepted malformed retention file")
	}
	if readState(t, dir) != original {
		t.Fatal("read failure replaced the original retention file")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != retentionFilename {
		t.Fatalf("failed rewrite left temporary files: %v, %v", files, err)
	}
}

func TestCleanupStopsAtFutureEntry(t *testing.T) {
	for _, mode := range []string{"dry-run", "not-expired"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			state := "2999-01-01T00:00:00Z\t<future>\ninvalid unread record\n"
			if mode == "dry-run" {
				state = "2000-01-01T00:00:00Z\t<expired>\n" + state
			}
			writeConfig(t, filepath.Join(dir, retentionFilename), state)
			var cfg config
			var trust *tls.Config
			if mode == "dry-run" {
				steps := append(imapLoginSteps("", false), imapSelectStep("INBOX", true), imapSearchStep("<expired>", ""), imapLogoutStep())
				cfg, trust = imapFakeServer(t, "", steps)
			}
			if err := cleanExpired(dir, cfg, mode == "dry-run", io.Discard, time.Now(), trust, time.Second); err != nil {
				t.Fatalf("parsed beyond the first future entry: %v", err)
			}
			if readState(t, dir) != state {
				t.Fatal("early stopping changed the retention file")
			}
		})
	}
}

func TestCleanupRewriteErrorPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	state := "2000-01-01T00:00:00Z\t<expired>\n2999-01-01T00:00:00Z\t<future>\ninvalid trailing record\n"
	writeConfig(t, filepath.Join(dir, retentionFilename), state)
	steps := append(imapLoginSteps("UIDPLUS", true), imapSelectStep("INBOX", false), imapSearchStep("<expired>", ""), imapLogoutStep())
	cfg, trust := imapFakeServer(t, "UIDPLUS", steps)
	if err := cleanExpired(dir, cfg, false, io.Discard, time.Now(), trust, time.Second); err == nil {
		t.Fatal("rewrite accepted malformed trailing record")
	}
	if readState(t, dir) != state {
		t.Fatal("streamed rewrite error changed the original")
	}
}

func TestLargeRetentionStream(t *testing.T) {
	dir := t.TempDir()
	const count = 100000
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f, err := os.Create(filepath.Join(dir, retentionFilename))
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	for i := range count {
		if err := (entry{start.Add(time.Duration(i) * time.Second), fmt.Sprintf("<%d@example.org>", i)}).Write(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		f, err := os.Open(filepath.Join(dir, retentionFilename))
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		got := 0
		for {
			e, err := readRetentionEntry(scanner)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.MessageID != fmt.Sprintf("<%d@example.org>", got) || !e.ExpiresAt.Equal(start.Add(time.Duration(got)*time.Second)) {
				t.Fatalf("unexpected entry at %d: %+v", got, e)
			}
			got++
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if got != count {
			t.Fatalf("read %d entries, want %d", got, count)
		}
	}
}
