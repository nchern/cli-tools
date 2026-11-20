package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupConfiguration(t *testing.T) {
	dir := commandHome(t)
	if err := run([]string{"-r", "1d", "<a>"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	text := "user = 'me@example.org'\npass = '/no/password'\nmailboxes = ['INBOX', 'Old Notifications']\n"
	defaultPath := filepath.Join(dir, "config.toml")
	writeConfig(t, defaultPath, text)
	cfg, err := readConfig(defaultPath)
	if err != nil || cfg.Addr != "imap.gmail.com:993" || cfg.Mailboxes[1] != "Old Notifications" {
		t.Fatalf("config=%v err=%v", cfg, err)
	}
	// Nothing is expired: no password access or server connection is needed.
	if err := run([]string{"-clean"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(t.TempDir(), "custom.toml")
	writeConfig(t, customPath, "addr = 'localhost:993'\n"+text)
	writeConfig(t, defaultPath, "invalid TOML")
	if err := run([]string{"-config", customPath, "-clean", "-dry-run"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-clean"}, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted invalid default config")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	commandHome(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	for _, text := range []string{
		"", "[", "user = 123", "user='u'\npass='p'", "user='u'\npass='p'\nmailboxes=[]",
		"user='u'\npass='p'\nmailboxes=['']", "user='u'\npass='p'\nmailboxes=['INBOX','INBOX']",
		"user='u'\npass='p'\nmailboxes=['INBOX']\nunknown=1",
	} {
		writeConfig(t, path, text)
		if err := run([]string{"-config", path, "-clean"}, io.Discard, io.Discard); err == nil {
			t.Errorf("accepted config %q", text)
		}
	}
	if err := run([]string{"-config", path + ".missing", "-clean"}, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted missing config")
	}
}

func TestCleanupFailurePreservesRetentionList(t *testing.T) {
	dir := commandHome(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := "# comment\n\n2000-01-01T00:00:00Z\t<a@example.org>\n"
	writeConfig(t, filepath.Join(dir, "retention.tsv"), state)
	writeConfig(t, filepath.Join(dir, "config.toml"), "user='u'\npass='/missing/password'\nmailboxes=['INBOX']")
	err := run([]string{"-clean"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("unexpected cleanup error: %v", err)
	}
	if got := readState(t, dir); got != state {
		t.Fatal("retention list changed on cleanup failure")
	}
}

func TestConfigPasswordPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.toml")
	cases := map[string]string{
		"~/.secrets/imap-password": filepath.Join(home, ".secrets", "imap-password"),
		"~/":                       home,
		"/absolute/password":       "/absolute/password",
		"relative/password":        "relative/password",
		"../password":              "../password",
		"~":                        "~",
		"~other/password":          "~other/password",
		"relative/~/password":      "relative/~/password",
		"$HOME/password":           "$HOME/password",
		"${HOME}/password":         "${HOME}/password",
	}
	for input, want := range cases {
		text := fmt.Sprintf("addr='imap.example.org:993'\nuser='u'\npass='%s'\nmailboxes=['~/Archive']", input)
		writeConfig(t, path, text)
		cfg, err := readConfig(path)
		if err != nil || cfg.Pass != want {
			t.Fatalf("pass=%q: got %q, want %q, err=%v", input, cfg.Pass, want, err)
		}
		if cfg.Mailboxes[0] != "~/Archive" {
			t.Fatal("expanded an IMAP mailbox name")
		}
	}
}

func TestConfigPasswordHomeUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("HOME", "")
	for _, pass := range []string{"~/password", "/absolute/password"} {
		writeConfig(t, path, fmt.Sprintf("user='u'\npass='%s'\nmailboxes=['INBOX']", pass))
		cfg, err := readConfig(path)
		if strings.HasPrefix(pass, "~/") {
			if err == nil || !strings.Contains(err.Error(), "home directory") {
				t.Fatalf("missing home-directory error: %v", err)
			}
		} else if err != nil || cfg.Pass != pass {
			t.Fatalf("absolute path requires home directory: %q, %v", cfg.Pass, err)
		}
	}
}

func TestIMAPLoginWithHomePasswordPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, filepath.Join(home, "password"), " \tsecret\r\n")
	id := "<absent@example.org>"
	steps := append(imapLoginSteps("", false), imapSelectStep("INBOX", true), imapSearchStep(id, ""), imapLogoutStep())
	serverConfig, trust := imapFakeServer(t, "", steps)
	path := filepath.Join(t.TempDir(), "config.toml")
	text := fmt.Sprintf("addr='%s'\nuser='user'\npass='~/password'\nmailboxes=['INBOX']", serverConfig.Addr)
	writeConfig(t, path, text)
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanIMAPWithTLS(cfg, []string{id}, true, io.Discard, trust, time.Second); err != nil {
		t.Fatal(err)
	}
}
