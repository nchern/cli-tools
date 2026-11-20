package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if !strings.HasSuffix(output, "2026-11-28T12:00:00Z\t<old@example.org>\n") {
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
	if err := writeRetentionList(dir, retentionList{"<new@example.org>": time.Now()}); err == nil {
		t.Fatal("replacement of a directory unexpectedly succeeded")
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
