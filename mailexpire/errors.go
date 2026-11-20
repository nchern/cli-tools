package main

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// EX_UNAVAILABLE from sysexits.h: service unavailable.
const exitUnavailable = 69

func errorToExitCode(err error) int {
	if err == nil {
		return 0
	}
	var netErr net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) ||
		(errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())) {
		return exitUnavailable
	}
	return 1
}

// go-imap hides reader errors from callers but passes them to ErrorLog.
type imapErrorLogger struct{}

func (l imapErrorLogger) Println(args ...any) {
	l.exitOnError(args...)
	fmt.Fprintln(os.Stderr, args...)
}

func (l imapErrorLogger) Printf(format string, args ...any) {
	l.exitOnError(args...)
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func (imapErrorLogger) exitOnError(args ...any) {
	for _, arg := range args {
		// Closing our own connection during cleanup is not a server failure.
		if err, ok := arg.(error); ok && err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintln(os.Stderr, appName+":", err)
			os.Exit(errorToExitCode(err))
		}
	}
}
