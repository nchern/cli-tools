package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net/textproto"
	"os"
)

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

func main() {
	var err error
	r := textproto.NewReader(bufio.NewReader(os.Stdin))
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
