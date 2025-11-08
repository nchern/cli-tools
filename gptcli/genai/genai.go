// Package genai provides a minimal Go client for GenAI-style chat APIs.
// This package focuses to abstract different providers and make using them
// transparent to a caller
package genai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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

	defaultTimeout = 30 * time.Second
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

type CallStat struct {
	Code int `json:"code"`

	DurationSec float64 `json:"duration_sec"`

	InCharsCount  int `json:"in_chars_count"`
	OutCharsCount int `json:"out_chars_count"`

	Payload map[string]any `json:"payload"`

	Timestamp time.Time `json:"timestamp"`
}

func newRequest(apiURL string, key string, payload any) (*http.Request, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", apiURL, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func parseOpenAIStream(resp *http.Response, w io.Writer, cs *CallStat) error {
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
			if err := writeStringTo(w, chunk.Choices[0].Delta.Content, cs); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseOllamaStream(resp *http.Response, w io.Writer, cs *CallStat) error {
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
			if err := writeStringTo(w, msg.Message.Content, cs); err != nil {
				return err
			}
		}
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
	return fmt.Errorf("%s %s %s", resp.Status, req.Method, req.URL)
}

func writeStringTo(w io.Writer, s string, cs *CallStat) error {
	cs.OutCharsCount += len(s)
	_, err := io.WriteString(w, s)
	return err
}

// Client represents a Gen AI API client
type Client struct {
	apiURL string

	key string

	model string

	stream bool

	timeout time.Duration
}

// NewClient creates a new Client instance with the given configuration.
func NewClient(apiURL string, key string, model string) *Client {
	return &Client{
		apiURL:  apiURL,
		key:     key,
		model:   model,
		timeout: defaultTimeout,
	}
}

// SetStreaming enables or disables streaming mode for completions
func (c *Client) SetStreaming(enabled bool) *Client {
	c.stream = enabled
	return c
}

// SetTimeout sets a custom timeout on this client
func (c *Client) SetTimeout(t time.Duration) *Client {
	c.timeout = t
	return c
}

// Complete calls GenAI to complete given messages and write results to a given
// writer
func (c *Client) Complete(messages []*Message, w io.Writer) (*CallStat, error) {
	payload := map[string]any{
		"stream":   c.stream,
		"model":    c.model,
		"messages": messages,
	}
	cs := &CallStat{Payload: payload}
	for _, m := range messages {
		if m.Role != Assistant {
			cs.InCharsCount += len(m.Content)
		}
	}
	req, err := newRequest(c.apiURL, c.key, payload)
	if err != nil {
		return cs, err
	}
	httpClient := &http.Client{Timeout: c.timeout}
	d, err := timeIt(func() error {
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		cs.Code = resp.StatusCode
		if resp.StatusCode != http.StatusOK {
			return handleError(req, resp)
		}
		return c.parse(resp, w, cs)
	})
	cs.DurationSec = float64(d) / float64(time.Second)
	return cs, err
}

func (c *Client) parse(resp *http.Response, w io.Writer, cs *CallStat) error {
	if c.stream {
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			// OpenAI response
			return parseOpenAIStream(resp, w, cs)
		}
		// Ollama response
		return parseOllamaStream(resp, w, cs)
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
		return writeStringTo(w, respData.Message.Content, cs)
	}
	// handle OpenAI response
	for _, choice := range respData.Choices {
		return writeStringTo(w, choice.Message.Content, cs)
	}
	return nil
}

func timeIt(fn func() error) (time.Duration, error) {
	started := time.Now()
	err := fn()
	return time.Since(started), err
}
