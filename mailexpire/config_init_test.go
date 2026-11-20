package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestInitConfig(t *testing.T) {
	for _, mode := range []string{"default", "custom"} {
		t.Run(mode, func(t *testing.T) {
			dir := commandHome(t)
			path := filepath.Join(dir, "config.toml")
			args := []string{"-init-config"}
			if mode == "custom" {
				path = filepath.Join(t.TempDir(), "custom settings", "mail", "config.toml")
				args = append(args, "-config", path)
			}
			if err := run(args, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			generated, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			example, err := os.ReadFile("config.example.toml")
			if err != nil || string(generated) != string(example) {
				t.Fatalf("generated config differs from shared example: %v", err)
			}
			var cfg config
			if _, err := toml.Decode(string(generated), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Addr != "" || cfg.User != "" || cfg.Pass != "" || len(cfg.Mailboxes) != 0 {
				t.Fatalf("template has configured account values: %+v", cfg)
			}
			for name, want := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
				info, err := os.Stat(name)
				if err != nil || info.Mode().Perm() != want {
					t.Fatalf("permissions for %s: %v, %v", name, info, err)
				}
			}
			if mode == "custom" {
				info, err := os.Stat(filepath.Dir(filepath.Dir(path)))
				if err != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("intermediate directory permissions: %v, %v", info, err)
				}
			}
			err = run([]string{"-config", path, "-clean"}, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "config requires") {
				t.Fatalf("cleanup accepted unconfigured template: %v", err)
			}
			for _, filename := range []string{"retention.tsv", "retention.lock"} {
				if _, err := os.Stat(filepath.Join(dir, filename)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("initialization touched %s: %v", filename, err)
				}
			}
		})
	}
}

func TestInitConfigRefusesExistingTargets(t *testing.T) {
	commandHome(t)
	for _, kind := range []string{"file", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			target := filepath.Join(dir, "target")
			switch kind {
			case "file":
				writeConfig(t, path, "existing config")
			case "directory":
				if err := os.Mkdir(path, privateDirectoryMode); err != nil {
					t.Fatal(err)
				}
			default:
				if kind == "symlink" {
					writeConfig(t, target, "existing target")
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			err := run([]string{"-config", path, "-init-config"}, io.Discard, io.Discard)
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("expected existing-file refusal, got %v", err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "existing config" {
					t.Fatalf("existing config changed: %q, %v", data, err)
				}
			case "directory":
				if !info.IsDir() {
					t.Fatal("existing directory replaced")
				}
			default:
				link, err := os.Readlink(path)
				if err != nil || link != target {
					t.Fatalf("existing symlink changed: %q, %v", link, err)
				}
				data, err := os.ReadFile(target)
				if kind == "symlink" && (err != nil || string(data) != "existing target") {
					t.Fatalf("symlink target changed: %q, %v", data, err)
				}
				if kind == "dangling symlink" && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("dangling symlink target created: %v", err)
				}
			}
		})
	}
}

func TestInitConfigRejectsConflictingFlags(t *testing.T) {
	dir := commandHome(t)
	for _, flags := range [][]string{
		{"-r", "1d"}, {"-r="}, {"-clean"}, {"-clean=false"},
		{"-dry-run"}, {"-dry-run=false"}, {"<id@example.org>"},
	} {
		args := append([]string{"-init-config"}, flags...)
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted conflicting flags: %q", args)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid initialization created app directory: %v", err)
	}
}

func TestMissingConfigExplainsInitialization(t *testing.T) {
	dir := commandHome(t)
	for _, args := range [][]string{{"-clean"}, {"-config", filepath.Join(dir, "custom.toml"), "-clean"}} {
		err := run(args, io.Discard, io.Discard)
		if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "-init-config") {
			t.Fatalf("missing initialization guidance: %v", err)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup created app directory for missing config: %v", err)
	}
}

func TestInitConfigPreservesRetentionList(t *testing.T) {
	dir := commandHome(t)
	if err := run([]string{"-r", "1d", "<id@example.org>"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	before := readState(t, dir)
	if err := run([]string{"-init-config"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if readState(t, dir) != before {
		t.Fatal("initialization changed the retention list")
	}
}

func TestInitConfigDirectoryFailure(t *testing.T) {
	commandHome(t)
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	writeConfig(t, parent, "original")
	err := run([]string{"-config", filepath.Join(parent, "config.toml"), "-init-config"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("initialization succeeded with a file as parent directory")
	}
	data, err := os.ReadFile(parent)
	if err != nil || string(data) != "original" {
		t.Fatalf("initialization changed parent file: %q, %v", data, err)
	}
}
