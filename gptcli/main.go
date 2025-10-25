package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultKeyFile = ".openapi.key"
	defaultModel   = "gpt-4.1-mini"

	url = "https://api.openai.com/v1/chat/completions"
)

type stringFlags []string

func (s *stringFlags) String() string {
	return fmt.Sprint(*s)
}

func (s *stringFlags) Set(value string) error {
	*s = append(*s, value)
	return nil
}

var (
	attachments stringFlags

	instructionText = flag.String("i", "", "instruction to LLM in text form")
	instructionPath = flag.String("f", "", "path to file with instructions to LLM")
	keyPath         = flag.String("k", filepath.Join(homePath(), defaultKeyFile), "path to API key file")
	model           = flag.String("m", defaultModel, "model name")
	timeout         = flag.Int("t", 30, "API timeout in seconds")
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

func readPrompt(args []string) string {
	if len(args) == 0 {
		data, _ := io.ReadAll(os.Stdin)
		return string(data)
	}
	return strings.Join(args, " ")
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
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func parse(resp *http.Response) (string, error) {
	var respData struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return "", err
	}
	for _, choice := range respData.Choices {
		return choice.Message.Content, nil
	}
	return "", nil
}

func handleError(req *http.Request, resp *http.Response) (string, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	} else {
		fmt.Fprintln(os.Stderr, string(body))
	}
	return "", fmt.Errorf("%s %s %s", resp.Status, req.Method, url)
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

func complete(key string, messages []*Message) (string, error) {
	payload := map[string]any{
		"stream":   false,
		"model":    *model,
		"messages": messages,
	}
	req, err := newRequest(key, payload)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: time.Duration(*timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return handleError(req, resp)
	}
	return parse(resp)
}

func prepare() (string, []*Message, error) {
	prompt := readPrompt(flag.Args())
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

func init() {
	log.SetFlags(0)

	flag.Var(&attachments, "a", "attach a file to prompt, supports multiple flags")
	flag.Parse()
}

func main() {
	key, messages, err := prepare()
	dieIf(err)

	resp, err := complete(key, messages)
	dieIf(err)

	fmt.Println(resp)
}

func must(err error) { dieIf(err) }

func dieIf(err error) {
	if err != nil {
		log.Fatalf("fatal: %s", err)
	}
}
