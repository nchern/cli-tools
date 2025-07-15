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

var (
	keyPath = flag.String("k", filepath.Join(homePath(), defaultKeyFile), "path to API key file")
	model   = flag.String("m", defaultModel, "model name")
	timeout = flag.Int("t", 30, "API timeout in seconds")
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

func makePayload(model, prompt string) []byte {
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream": false,
	}
	data, err := json.Marshal(payload)
	dieIf(err)
	return data
}

func readPrompt(args []string) string {
	if len(args) == 0 {
		data, _ := io.ReadAll(os.Stdin)
		return string(data)
	}
	return strings.Join(args, " ")
}

func complete(key string, data []byte) (string, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: time.Duration(*timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
		} else {
			fmt.Fprintln(os.Stderr, string(body))
		}
		return "", fmt.Errorf("%s %s %s", resp.Status, req.Method, url)
	}
	var respData struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
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

func init() {
	log.SetFlags(0)
}

func main() {
	flag.Parse()

	prompt := readPrompt(flag.Args())
	if prompt == "" {
		dieIf(errors.New("empty prompt"))
	}

	key, err := apiKey()
	dieIf(err)

	data := makePayload(*model, prompt)
	resp, err := complete(key, data)
	dieIf(err)

	fmt.Println(resp)
}

func must(err error) {
	dieIf(err)
}

func dieIf(err error) {
	if err != nil {
		log.Fatalf("fatal: %s", err)
	}
}
