package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultKeyFile = ".openapi.key"
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
	timeout         = flag.Int("t", 30, "API timeout in seconds")
	// stdin/args/combine/auto
	promptSrc = flag.String("p", string(auto), "prompt source")
	stream    = flag.Bool("s", false, "if set, use streaming API")
	url       = flag.String("u", "https://api.openai.com/v1/chat/completions", "AI API url")
	verbose   = flag.Bool("v", false, "if set, verbose mode shows timings")
)

func homePath() string {
	u, err := user.Current()
	dieIf(err)
	return u.HomeDir
}

func apiKey() (string, error) {
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

// Role represents a role in LLM chat
type Role string

const (
	// User - message from a human user
	User Role = "user"
	// Assistant - message from an AI assistant
	Assistant Role = "assistant"
	// System - setup instructions or behavior context
	System Role = "system"
	// Examples:
	//    { "role": "system", "content": "You are a helpful assistant." },
	//    { "role": "user", "content": "What's 5 + 3?" },
	//    { "role": "assistant", "content": "8" }
)

// Message represents a message for LLM
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// NewMessage creates a new instance of a message
func NewMessage(role Role, s string) *Message {
	return &Message{Role: role, Content: s}
}

func newRequest(key string, payload any) (*http.Request, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", *url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func parseOpenAIStream(resp *http.Response, w io.Writer) error {
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "data: ") {
			// OpenAI sends "data: ..." lines; ignore keepalives
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if strings.TrimSpace(data) == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		if len(chunk.Choices) > 0 {
			if _, err := io.WriteString(w, chunk.Choices[0].Delta.Content); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseOllamaStream(resp *http.Response, w io.Writer) error {
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg struct {
			Done    bool    `json:"done"`
			Model   string  `json:"model"`
			Message Message `json:"message"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return err
		}
		if msg.Done {
			break
		}
		if msg.Message.Content != "" {
			if _, err := io.WriteString(w, msg.Message.Content); err != nil {
				return err
			}
		}
	}
	return nil
}

func parse(resp *http.Response, w io.Writer) error {
	if *stream {
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			// OpenAI response
			return parseOpenAIStream(resp, w)
		}
		// Ollama response
		return parseOllamaStream(resp, w)
	}
	var respData struct {
		// OpenAI response
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		// Ollama response
		Message *Message `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return err
	}
	// handle Ollama response
	if respData.Message != nil {
		_, err := io.WriteString(w, respData.Message.Content)
		return err
	}
	// handle OpenAI response
	for _, choice := range respData.Choices {
		_, err := io.WriteString(w, choice.Message.Content)
		return err
	}
	return nil
}

func handleError(req *http.Request, resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	} else {
		fmt.Fprintln(os.Stderr, string(body))
	}
	return fmt.Errorf("%s %s %s", resp.Status, req.Method, *url)
}

func mkMessages(instructions string, prompt string, attachPaths ...string) ([]*Message, error) {
	messages := []*Message{}
	if instructions != "" {
		messages = append(messages, NewMessage(System, instructions))
	}
	for _, path := range attachPaths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		content := fmt.Sprintf("file: %s\n%s", name, string(b))
		messages = append(messages, NewMessage(User, content))
	}
	messages = append(messages, NewMessage(User, prompt))
	return messages, nil
}

func complete(key string, messages []*Message, w io.Writer) error {
	payload := map[string]any{
		"stream":   *stream,
		"model":    *model,
		"messages": messages,
	}
	req, err := newRequest(key, payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: time.Duration(*timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return handleError(req, resp)
	}
	return parse(resp, w)
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

func prepare() (string, []*Message, error) {
	prompt, err := readPrompt(promptSource(*promptSrc), flag.Args())
	if err != nil {
		return "", nil, err
	}
	if prompt == "" {
		return "", nil, errors.New("empty prompt")
	}
	key, err := apiKey()
	if err != nil {
		return "", nil, err
	}
	instructions, err := readInstructions()
	if err != nil {
		return "", nil, err
	}
	messages, err := mkMessages(instructions, prompt, attachments...)
	if err != nil {
		return "", nil, err
	}
	return key, messages, nil
}

func timeIt(fn func(), msg string) {
	started := time.Now()
	fn()
	elapsed := time.Since(started)
	if *verbose {
		fmt.Fprintf(os.Stderr, "\n%s took: %s\n", msg, elapsed)
	}
}

func init() {
	log.SetFlags(0)

	flag.Var(&attachments, "a", "attach a file to prompt, multiple attachments are supported")
	flag.Parse()
}

func main() {
	key, messages, err := prepare()
	dieIf(err)

	timeIt(func() {
		err = complete(key, messages, os.Stdout)
	}, "complete")
	dieIf(err)
}

func must(err error) { dieIf(err) }

func dieIf(err error) {
	if err != nil {
		log.Printf("fatal: %s", err)
		os.Exit(errorToExitCode(err))
	}
}
