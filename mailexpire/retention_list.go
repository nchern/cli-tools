package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// retention.tsv stores one record per line:
// <expiration: RFC3339 UTC, whole seconds><TAB><RFC Message-ID>
// Reading also accepts fractional seconds and explicit timezone offsets.
// Blank lines and lines beginning with # are ignored.
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

type retentionList map[string]time.Time

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

func readRetentionList(dir string) (retentionList, error) {
	lst := make(retentionList)
	f, err := os.Open(filepath.Join(dir, retentionFilename))
	if errors.Is(err, os.ErrNotExist) {
		return lst, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, retentionCommentPrefix) {
			continue
		}
		fields := strings.Split(text, retentionFieldSeparator)
		if len(fields) != retentionFieldCount {
			return nil, fmt.Errorf("invalid retention record at line %d", line)
		}
		expiresAt, messageID := fields[0], fields[1]
		if err := validateID(messageID); err != nil {
			return nil, fmt.Errorf("invalid message ID at line %d: %w", line, err)
		}
		expires, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expiration at line %d: %w", line, err)
		}
		if _, exists := lst[messageID]; exists {
			return nil, fmt.Errorf("duplicate message ID at line %d", line)
		}
		lst[messageID] = expires
	}
	return lst, scanner.Err()
}

func writeRetentionList(dir string, lst retentionList) error {
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
	for _, id := range sortedIDs(lst) {
		if _, err := fmt.Fprintf(w, "%s%s%s\n",
			lst[id].UTC().Format(time.RFC3339), retentionFieldSeparator, id); err != nil {
			return err
		}
	}
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

func sortedIDs(lst retentionList) []string {
	ids := make([]string, 0, len(lst))
	for id := range lst {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func register(dir string, ids []string, duration time.Duration) error {
	lock, err := lockRetentionList(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	lst, err := readRetentionList(dir)
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(duration)
	for _, id := range ids {
		lst[id] = expires
	}
	return writeRetentionList(dir, lst)
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
	q, err := readRetentionList(dir)
	if err != nil {
		return err
	}
	return cleanExpired(dir, q, cfg, dryRun, out, time.Now(), cleanIMAP)
}

type cleanupFunc func(config, []string, bool, io.Writer) error

func cleanExpired(dir string,
	lst retentionList,
	cfg config,
	dryRun bool,
	out io.Writer,
	now time.Time,
	clean cleanupFunc) error {

	var expired []string
	for _, id := range sortedIDs(lst) {
		if !lst[id].After(now) {
			expired = append(expired, id)
		}
	}
	if len(expired) == 0 {
		return nil
	}
	if err := clean(cfg, expired, dryRun, out); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	for _, id := range expired {
		delete(lst, id)
	}
	return writeRetentionList(dir, lst)
}
