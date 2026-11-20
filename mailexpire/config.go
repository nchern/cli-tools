package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed config.example.toml
var configTemplate string

const (
	configFilename     = "config.toml"
	defaultIMAPAddress = "imap.gmail.com:993"
)

type config struct {
	Addr      string   `toml:"addr"`
	User      string   `toml:"user"`
	Pass      string   `toml:"pass"`
	Mailboxes []string `toml:"mailboxes"`
}

func readConfig(path string) (config, error) {
	cfg := config{Addr: defaultIMAPAddress}
	meta, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("config %q is missing; create it with %s -config %q -init-config: %w", path, appName, path, err)
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if len(meta.Undecoded()) != 0 {
		return cfg, fmt.Errorf("unknown config keys: %v", meta.Undecoded())
	}
	if strings.TrimSpace(cfg.Addr) == "" || strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Pass) == "" {
		return cfg, fmt.Errorf("config requires addr, user, and pass (password-file path)")
	}
	if len(cfg.Mailboxes) == 0 {
		return cfg, fmt.Errorf("config requires a nonempty mailboxes array")
	}
	seen := make(map[string]bool)
	for _, mailbox := range cfg.Mailboxes {
		if strings.TrimSpace(mailbox) == "" || strings.ContainsAny(mailbox, "\x00\r\n") || seen[mailbox] {
			return cfg, fmt.Errorf("invalid or duplicate mailbox %q", mailbox)
		}
		seen[mailbox] = true
	}
	if strings.HasPrefix(cfg.Pass, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("expand password-file home directory: %w", err)
		}
		cfg.Pass = filepath.Join(home, strings.TrimPrefix(cfg.Pass, "~/"))
	}
	return cfg, nil
}

func initConfig(path string) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), privateDirectoryMode); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return fmt.Errorf("create config %q (existing files are never overwritten): %w", path, err)
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(path)
		}
	}()
	if _, err := f.WriteString(configTemplate); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync config: %w", err)
	}
	return f.Close()
}
