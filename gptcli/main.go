package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/nchern/cli-tools/gptcli/genai"
)

const (
	defaultKeyFile = ".gptcli.key"
	defaultModel   = "gpt-5-nano"

	// /usr/include/sysexits.h:101: EX_UNAVAILABLE - service unavailable
	exitUnavailable = 69
)

type stringFlags []string

func (s *stringFlags) String() string {
	return fmt.Sprint(*s)
}

func (s *stringFlags) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type perfLogFlag string

func (pf *perfLogFlag) String() string {
	if pf == nil {
		return ""
	}
	return string(*pf)
}

func (pf *perfLogFlag) Set(value string) error {
	*pf = perfLogFlag(value)
	return nil
}

func (pf *perfLogFlag) Write(cs *genai.CallStat) error {
	if pf == nil || *pf == "" {
		return nil
	}
	f, err := os.OpenFile(string(*pf), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, string(b))
	return err
}

type promptSource string

const (
	auto      promptSource = "auto"
	combine   promptSource = "combine"
	argsOnly  promptSource = "args"
	stdinOnly promptSource = "stdin"
)

var (
	// CLI flags
	attachments     stringFlags // -a flag - see in init()
	instructionText = flag.String("i", "", "instruction to LLM in text form")
	instructionPath = flag.String("f", "", "path to file with instructions to LLM")
	keyPath         = flag.String("k", filepath.Join(homePath(), defaultKeyFile), "path to API key file")
	model           = flag.String("m", defaultModel, "model name")
	timeout         = flag.Int("t", 600, "API timeout in seconds")
	// stdin/args/combine/auto
	promptSrc      = flag.String("p", string(auto), "prompt source")
	performanceLog perfLogFlag // -perflog flag - see in init()
	raw            = flag.Bool("r", false, "if set, expects raw messages on stdin in JSON format")
	stream         = flag.Bool("s", false, "if set, use streaming API")
	url            = flag.String("u", "https://api.openai.com/v1/chat/completions", "AI API url")
	verbose        = flag.Bool("v", false, "if set, verbose mode shows timings")
)

func homePath() string {
	u, err := user.Current()
	dieIf(err)
	return u.HomeDir
}

func apiKey() (string, error) {
	fi, err := os.Stat(*keyPath)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm() != 0600 {
		return "", fmt.Errorf("%s: must have permissions 600(-rw-------); actual: %s",
			*keyPath, fi.Mode().Perm())
	}
	data, err := os.ReadFile(*keyPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readInstructions() (string, error) {
	if *instructionText != "" {
		return *instructionText, nil
	}
	if *instructionPath == "" {
		return "", nil
	}
	data, err := os.ReadFile(*instructionPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readPrompt(src promptSource, args []string) (string, error) {
	switch src {
	case combine:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(b) + "\n\n" + strings.Join(args, " "), nil
	case argsOnly:
		return strings.Join(args, " "), nil
	case stdinOnly:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case auto:
		// auto by default
		if len(args) == 0 {
			data, _ := io.ReadAll(os.Stdin)
			return string(data), nil
		}
		return strings.Join(args, " "), nil
	}
	return "", fmt.Errorf("unknown source: %s", src)
}

func errorToExitCode(err error) int {
	if os.IsTimeout(err) {
		return exitUnavailable
	}
	if opErr, ok := err.(*net.OpError); ok {
		if opErr.Temporary() || opErr.Timeout() {
			return exitUnavailable
		}
	}
	return 1
}

func mkMessages(instructions string, prompt string, attachPaths ...string) ([]*genai.Message, error) {
	messages := []*genai.Message{}
	if instructions != "" {
		messages = append(messages, genai.NewMessage(genai.System, instructions))
	}
	for _, path := range attachPaths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		content := fmt.Sprintf("file: %s\n%s", name, string(b))
		messages = append(messages, genai.NewMessage(genai.User, content))
	}
	messages = append(messages, genai.NewMessage(genai.User, prompt))
	return messages, nil
}

func init() {
	log.SetFlags(0)
	defaultUsage := flag.Usage
	flag.Usage = func() {
		fmt.Printf("%s - CLI interface to LLM APIs.\n\n", os.Args[0])
		defaultUsage()
	}

	flag.Var(&attachments, "a", "attach a file to prompt; multiple attachments are supported")
	flag.Var(&performanceLog, "perflog", "enables profiling: will write perf stats into a given log")
	flag.Parse()
}

type aiClient interface {
	Complete(messages []*genai.Message, w io.Writer) (*genai.CallStat, error)
}

type rawModeClientDecorator struct {
	client aiClient
}

func (c *rawModeClientDecorator) Complete(messages []*genai.Message, w io.Writer) (*genai.CallStat, error) {
	var buf bytes.Buffer
	cs, err := c.client.Complete(messages, &buf)
	if err != nil {
		return cs, err
	}
	messages = append(messages, &genai.Message{Role: genai.Assistant, Content: buf.String()})
	return cs, json.NewEncoder(w).Encode(messages)
}

func prepare() (aiClient, []*genai.Message, error) {
	key, err := apiKey()
	if err != nil {
		return nil, nil, err
	}

	var res aiClient = genai.NewClient(*url, key, *model).SetStreaming(*stream).
		SetTimeout(time.Duration(*timeout) * time.Second)
	if *raw {
		res = &rawModeClientDecorator{res}
		var msgs []*genai.Message
		if err := json.NewDecoder(os.Stdin).Decode(&msgs); err != nil {
			return nil, nil, err
		}
		return res, msgs, nil
	}
	prompt, err := readPrompt(promptSource(*promptSrc), flag.Args())
	if err != nil {
		return nil, nil, err
	}
	if prompt == "" {
		return nil, nil, errors.New("empty prompt")
	}
	instructions, err := readInstructions()
	if err != nil {
		return nil, nil, err
	}
	messages, err := mkMessages(instructions, prompt, attachments...)
	if err != nil {
		return nil, nil, err
	}
	return res, messages, nil
}

func main() {
	ai, messages, err := prepare()
	dieIf(err)
	cstat, err := ai.Complete(messages, os.Stdout)
	if *verbose {
		fmt.Fprintf(os.Stderr, "\ncomplete took: %fs\n", cstat.DurationSec)
	}
	dieIf(err)
	cstat.Timestamp = time.Now()
	must(performanceLog.Write(cstat))
}

func must(err error) { dieIf(err) }

func dieIf(err error) {
	if err != nil {
		log.Printf("fatal: %s", err)
		os.Exit(errorToExitCode(err))
	}
}
