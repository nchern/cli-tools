package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// retention.tsv stores one record per line:
// <expiration: RFC3339 UTC, whole seconds><TAB><RFC Message-ID>
// Reading also accepts fractional seconds and explicit timezone offsets.
// Blank lines and lines beginning with # are ignored.
// Records must be ordered by expiration, earliest first.
const (
	retentionFilename       = "retention.tsv"
	retentionLockFilename   = "retention.lock"
	retentionTempPattern    = ".retention-*"
	retentionFieldSeparator = "\t"
	retentionFieldCount     = 2
	retentionCommentPrefix  = "#"
	retentionHeader         = "# mailexpire retention v1\n# expires-at" + retentionFieldSeparator + "message-id"
	privateDirectoryMode    = 0700
	privateFileMode         = 0600
)

type entry struct {
	ExpiresAt time.Time
	MessageID string
}

func (e *entry) Parse(s string) error {
	fields := strings.Split(s, retentionFieldSeparator)
	if len(fields) != retentionFieldCount {
		return errors.New("invalid retention record")
	}
	if err := validateID(fields[1]); err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}
	expires, err := time.Parse(time.RFC3339Nano, fields[0])
	if err != nil {
		return fmt.Errorf("invalid expiration: %w", err)
	}
	e.ExpiresAt, e.MessageID = expires, fields[1]
	return nil
}

func lockRetentionList(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, privateDirectoryMode); err != nil {
		return nil, err
	}
	if err := syncDirectory(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, retentionLockFilename), os.O_CREATE|os.O_RDWR, privateFileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func readRetentionEntry(scanner *bufio.Scanner) (entry, error) {
	for scanner.Scan() {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, retentionCommentPrefix) {
			continue
		}
		var e entry
		err := e.Parse(text)
		return e, err
	}
	if err := scanner.Err(); err != nil {
		return entry{}, err
	}
	return entry{}, io.EOF
}

func (e entry) Write(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s%s%s\n",
		e.ExpiresAt.UTC().Format(time.RFC3339), retentionFieldSeparator, e.MessageID)
	return err
}

func commitRetentionList(dir string, f *os.File, w *bufio.Writer) error {
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, retentionFilename)); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync directory %q: %w", dir, err)
	}
	return nil
}

func register(dir string, ids []string, duration time.Duration) error {
	lock, err := lockRetentionList(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	expires := time.Now().UTC().Add(duration).Truncate(time.Second)
	return rewriteRetentionList(dir, ids, expires)
}

// A nil ID slice removes expired entries; otherwise merge registrations.
func rewriteRetentionList(dir string, ids []string, expires time.Time) error {
	src, err := os.Open(filepath.Join(dir, retentionFilename))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var scanner *bufio.Scanner
	if src != nil {
		defer src.Close()
		scanner = bufio.NewScanner(src)
	}
	replacements := make(map[string]struct{}, len(ids))
	var pending []string
	for _, id := range ids {
		if _, exists := replacements[id]; !exists {
			replacements[id] = struct{}{}
			pending = append(pending, id)
		}
	}
	f, err := os.CreateTemp(dir, retentionTempPattern)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	w := bufio.NewWriter(f)
	if _, err := fmt.Fprintln(w, retentionHeader); err != nil {
		return err
	}
	for scanner != nil {
		e, err := readRetentionEntry(scanner)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if !e.ExpiresAt.Before(expires) {
			for _, id := range pending {
				if err := (entry{expires, id}).Write(w); err != nil {
					return err
				}
			}
			pending = nil
		}
		if _, replaced := replacements[e.MessageID]; replaced || (ids == nil && !e.ExpiresAt.After(expires)) {
			continue
		}
		if err := e.Write(w); err != nil {
			return err
		}
	}
	for _, id := range pending {
		if err := (entry{expires, id}).Write(w); err != nil {
			return err
		}
	}
	return commitRetentionList(dir, f, w)
}

func cleanRetentionList(dir, configPath string, dryRun bool, out io.Writer) error {
	cfg, err := readConfig(configPath)
	if err != nil {
		return err
	}
	lock, err := lockRetentionList(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	return cleanExpired(dir, cfg, dryRun, out, time.Now(), nil, imapTimeout)
}

func cleanExpired(dir string, cfg config, dryRun bool, out io.Writer, now time.Time, tlsConfig *tls.Config, timeout time.Duration) error {
	path := filepath.Join(dir, retentionFilename)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	e, err := readRetentionEntry(bufio.NewScanner(f))
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.ExpiresAt.After(now) {
		return nil
	}
	if err := cleanIMAPWithTLS(cfg, path, now, dryRun, out, tlsConfig, timeout); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	return rewriteRetentionList(dir, nil, now)
}
