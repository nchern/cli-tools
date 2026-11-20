package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	appName           = "mailexpire"
	appDirectoryName  = "." + appName
	maxMessageIDBytes = 60 * 1024
	retentionDay      = 24 * time.Hour
	retentionWeek     = 7 * retentionDay
)

type commandFlags struct {
	retention  string
	clean      bool
	dryRun     bool
	initConfig bool
	configPath string
}

func initFlags(defaultConfigPath string, errOut io.Writer) (*flag.FlagSet, *commandFlags) {
	values := &commandFlags{}
	flags := flag.NewFlagSet(appName, flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.StringVar(&values.retention, "r", "", "retain messages for a positive duration (e.g. 12h, 10d, 2w)")
	flags.BoolVar(&values.clean, "clean", false, "delete expired messages from configured mailboxes")
	flags.BoolVar(&values.dryRun, "dry-run", false, "report cleanup actions without changing messages or retention list")
	flags.BoolVar(&values.initConfig, "init-config", false, "create an unconfigured TOML template without overwriting an existing file")
	flags.StringVar(&values.configPath, "config", defaultConfigPath, "TOML configuration path (cleanup or initialization)")
	return flags, values
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, appName+":", err)
		os.Exit(1)
	}
}

func run(args []string, out, errOut io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, appDirectoryName)
	flags, values := initFlags(filepath.Join(dir, configFilename), errOut)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if values.initConfig {
		conflict := flags.NArg() != 0
		flags.Visit(func(f *flag.Flag) {
			if f.Name != "init-config" && f.Name != "config" {
				conflict = true
			}
		})
		if conflict {
			return fmt.Errorf("-init-config cannot be combined with -r, -clean, -dry-run, or message IDs")
		}
		return initConfig(values.configPath)
	}
	if values.clean {
		if values.retention != "" || flags.NArg() != 0 {
			return fmt.Errorf("-clean cannot be combined with -r or message IDs")
		}
		return cleanRetentionList(dir, values.configPath, values.dryRun, out)
	}
	if values.dryRun || values.retention == "" || flags.NArg() == 0 {
		return fmt.Errorf("use -r duration message-id ..., -clean [-dry-run], or -init-config")
	}
	duration, err := parseRetention(values.retention)
	if err != nil {
		return err
	}
	for _, id := range flags.Args() {
		if err := validateID(id); err != nil {
			return err
		}
	}
	return register(dir, flags.Args(), duration)
}

func parseRetention(value string) (time.Duration, error) {
	units := map[byte]time.Duration{'h': time.Hour, 'd': retentionDay, 'w': retentionWeek}
	if len(value) < 2 {
		return 0, fmt.Errorf("invalid retention %q: use a positive integer followed by h, d, or w", value)
	}
	unit := units[value[len(value)-1]]
	n, err := strconv.ParseUint(value[:len(value)-1], 10, 64)
	if err != nil || unit == 0 || n == 0 || n > uint64(math.MaxInt64/unit) {
		return 0, fmt.Errorf("invalid or overflowing retention %q", value)
	}
	return time.Duration(n) * unit, nil
}

func validateID(id string) error {
	// Leave room for the timestamp within the retention list scanner's 64 KiB line limit.
	if len(id) > maxMessageIDBytes {
		return fmt.Errorf("message ID exceeds %d bytes", maxMessageIDBytes)
	}
	return nil
}
