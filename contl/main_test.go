package main

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadContinuedLineBytes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello\n", "hello"},
		{"continued", "hello\n\tworld\n", "hello world"},
		{"empty line", "\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(bufio.NewReader(strings.NewReader(tt.input)))
			got, err := r.ReadContinuedLineBytes()
			if err != nil || got == nil || string(got) != tt.want {
				t.Fatalf("ReadContinuedLineBytes() = %q, %v; want non-nil %q, nil", got, err, tt.want)
			}
			got, err = r.ReadContinuedLineBytes()
			if got != nil || !errors.Is(err, io.EOF) {
				t.Fatalf("at EOF got %q, %v; want nil, EOF", got, err)
			}
		})
	}
}

func TestReadContinuedLineBytesOwnsStorage(t *testing.T) {
	r := NewReader(bufio.NewReader(strings.NewReader("first\n continued\nsecond\n continued\n")))
	first, err := r.ReadContinuedLineBytes()
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.ReadContinuedLineBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "first continued" || string(second) != "second continued" {
		t.Fatalf("lines = %q, %q; returned bytes changed after reading", first, second)
	}
	second[0] = 'S'
	if string(first) != "first continued" {
		t.Fatalf("first line changed after mutating second: %q", first)
	}
}
