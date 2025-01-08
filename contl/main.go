package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
)

var delimeter = flag.String("d", " ", "is used to concatenate continued lines")

func init() {
	usage := flag.Usage
	flag.Usage = func() {
		fmt.Println("The utility reads possibly continued lines from stdin, turns each continued")
		fmt.Println("line into a single one that does not break.")
		fmt.Println()

		usage()
	}
	flag.Parse()
}

// A Reader implements convenience methods for reading requests
// or responses from a text protocol network connection.
type Reader struct {
	delim []byte
	R     *bufio.Reader
	buf   []byte // a re-usable buffer for readContinuedLineSlice
}

// NewReader returns a new Reader reading from r.
//
// To avoid denial of service attacks, the provided bufio.Reader
// should be reading from an io.LimitReader or similar Reader to bound
// the size of responses.
//
// THIS CODE IS COPIED FROM textproto package as the original struct
// DOES NOT ALLOW TO PROVIDE CUSTOM DELIMETER FOR CONTINUED LINES
func NewReader(r *bufio.Reader) *Reader {
	return &Reader{R: r, delim: []byte{' '}}
}

func (r *Reader) readLineSlice() ([]byte, error) {
	var line []byte
	for {
		l, more, err := r.R.ReadLine()
		if err != nil {
			return nil, err
		}
		// Avoid the copy if the first call produced a full line.
		if line == nil && !more {
			return l, nil
		}
		line = append(line, l...)
		if !more {
			break
		}
	}
	return line, nil
}

// ReadContinuedLine reads a possibly continued line from r,
// eliding the final trailing ASCII white space.
// Lines after the first are considered continuations if they
// begin with a space or tab character. In the returned data,
// continuation lines are separated from the previous line
// only by a single space: the newline and leading white space
// are removed.
//
// For example, consider this input:
//
//	Line 1
//	  continued...
//	Line 2
//
// The first call to ReadContinuedLine will return "Line 1 continued..."
// and the second will return "Line 2".
//
// Empty lines are never continued.
func (r *Reader) ReadContinuedLine() (string, error) {
	line, err := r.readContinuedLineSlice(noValidation)
	return string(line), err
}

// trim returns s with leading and trailing spaces and tabs removed.
// It does not assume Unicode or UTF-8.
func trim(s []byte) []byte {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	n := len(s)
	for n > i && (s[n-1] == ' ' || s[n-1] == '\t') {
		n--
	}
	return s[i:n]
}

// ReadContinuedLineBytes is like ReadContinuedLine but
// returns a []byte instead of a string.
func (r *Reader) ReadContinuedLineBytes() ([]byte, error) {
	line, err := r.readContinuedLineSlice(noValidation)
	if line != nil {
		buf := make([]byte, len(line))
		copy(buf, line)
		line = buf
	}
	return line, err
}

// readContinuedLineSlice reads continued lines from the reader buffer,
// returning a byte slice with all lines. The validateFirstLine function
// is run on the first read line, and if it returns an error then this
// error is returned from readContinuedLineSlice.
func (r *Reader) readContinuedLineSlice(validateFirstLine func([]byte) error) ([]byte, error) {
	if validateFirstLine == nil {
		return nil, fmt.Errorf("missing validateFirstLine func")
	}

	// Read the first line.
	line, err := r.readLineSlice()
	if err != nil {
		return nil, err
	}
	if len(line) == 0 { // blank line - no continuation
		return line, nil
	}

	if err := validateFirstLine(line); err != nil {
		return nil, err
	}

	// Optimistically assume that we have started to buffer the next line
	// and it starts with an ASCII letter (the next header key), or a blank
	// line, so we can avoid copying that buffered data around in memory
	// and skipping over non-existent whitespace.
	if r.R.Buffered() > 1 {
		peek, _ := r.R.Peek(2)
		if len(peek) > 0 && (isASCIILetter(peek[0]) || peek[0] == '\n') ||
			len(peek) == 2 && peek[0] == '\r' && peek[1] == '\n' {
			return trim(line), nil
		}
	}

	// ReadByte or the next readLineSlice will flush the read buffer;
	// copy the slice into buf.
	r.buf = append(r.buf[:0], trim(line)...)

	// Read continuation lines.
	for r.skipSpace() > 0 {
		line, err := r.readLineSlice()
		if err != nil {
			break
		}
		r.buf = append(r.buf, r.delim...)
		r.buf = append(r.buf, trim(line)...)
	}
	return r.buf, nil
}

// skipSpace skips R over all spaces and returns the number of bytes skipped.
func (r *Reader) skipSpace() int {
	n := 0
	for {
		c, err := r.R.ReadByte()
		if err != nil {
			// Bufio will keep err until next read.
			break
		}
		if c != ' ' && c != '\t' {
			r.R.UnreadByte()
			break
		}
		n++
	}
	return n
}

// noValidation is a no-op validation func for readContinuedLineSlice
// that permits any lines.
func noValidation(_ []byte) error { return nil }

func isASCIILetter(b byte) bool {
	b |= 0x20 // make lower case
	return 'a' <= b && b <= 'z'
}

func main() {
	var err error
	r := NewReader(bufio.NewReader(os.Stdin))
	r.delim = []byte(*delimeter)
	for {
		var s string
		s, err = r.ReadContinuedLine()
		if err != nil {
			break
		}
		fmt.Println(s)
	}
	if err != nil && err != io.EOF {
		log.Fatalf("fatal: %s", err)
	}
}
