package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
)

func TestErrorToExitCode(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 1},
		{"ordinary", errors.New("failure"), 1},
		{"wrapped ordinary", fmt.Errorf("request failed: %w", errors.New("failure")), 1},
		{"os timeout", os.ErrDeadlineExceeded, 69},
		{"temporary without OpError", &net.DNSError{IsTemporary: true}, 1},
		{"wrapped temporary without OpError", fmt.Errorf("request failed: %w", &net.DNSError{IsTemporary: true}), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorToExitCode(tt.err); got != tt.want {
				t.Errorf("errorToExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}

	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{"timeout", &net.DNSError{IsTimeout: true}, 69},
		{"temporary", &net.DNSError{IsTemporary: true}, 69},
		{"permanent", errors.New("connection refused"), 1},
	} {
		err := &net.OpError{Op: "dial", Net: "tcp", Err: tt.err}
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", tt.name, wrapped), func(t *testing.T) {
				var input error = err
				if wrapped {
					input = fmt.Errorf("request failed: %w", err)
				}
				if got := errorToExitCode(input); got != tt.want {
					t.Errorf("errorToExitCode(%v) = %d, want %d", input, got, tt.want)
				}
			})
		}
	}
}
