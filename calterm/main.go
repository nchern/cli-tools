package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	calendarScope = "https://www.googleapis.com/auth/calendar.readonly"
	tokenEndpoint = "https://oauth2.googleapis.com/token"
)

type credentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type tokenFile struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type calendarResponse struct {
	Items []struct {
		Summary string `json:"summary"`
		Start   struct {
			DateTime string `json:"dateTime"`
			Date     string `json:"date"`
		} `json:"start"`
		End struct {
			DateTime string `json:"dateTime"`
			Date     string `json:"date"`
		} `json:"end"`
	} `json:"items"`
}

func main() {
	var (
		calendarID = flag.String("calendar-id", "", "calendar ID to query (required)")
		hours      = flag.Int("hours", 1, "lookahead window in hours")
		credPath   = flag.String("cred-file", "", "path to client credential JSON")
		configDir  = flag.String("config-dir", "", "config directory (default $HOME/.config/calendar-cli)")
		initAuth   = flag.Bool("init-auth", false, "run desktop OAuth flow and exit")
	)
	flag.Parse()

	if *calendarID == "" {
		fmt.Fprintln(os.Stderr, "--calendar-id is required")
		flag.Usage()
		os.Exit(1)
	}

	if *credPath == "" {
		fmt.Fprintln(os.Stderr, "--cred-file is required")
		flag.Usage()
		os.Exit(1)
	}

	if *hours <= 0 {
		*hours = 1
	}

	cred, err := loadCredentials(*credPath)
	if err != nil {
		fatal(err)
	}

	configPath, err := resolveConfigDir(*configDir)
	if err != nil {
		fatal(err)
	}

	tokenPath := filepath.Join(configPath, "tokens.json")

	if *initAuth {
		if err := doInitAuth(cred, tokenPath); err != nil {
			fatal(err)
		}
		fmt.Println("tokens saved to", tokenPath)
		return
	}

	token, err := loadToken(tokenPath)
	if err != nil {
		fatal(fmt.Errorf("load token: %w", err))
	}

	accessToken, err := ensureAccessToken(cred, tokenPath, token)
	if err != nil {
		fatal(fmt.Errorf("access token: %w", err))
	}

	if err := fetchAndPrintEvents(*calendarID, *hours, accessToken); err != nil {
		fatal(err)
	}
}

func loadCredentials(path string) (*credentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var cred credentials
	if err := json.Unmarshal(raw, &cred); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if cred.ClientID == "" || cred.ClientSecret == "" {
		return nil, errors.New("credentials missing client_id or client_secret")
	}
	return &cred, nil
}

func resolveConfigDir(custom string) (string, error) {
	if custom != "" {
		return mkdirp(custom)
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return mkdirp(filepath.Join(dir, "calendar-cli"))
}

func mkdirp(path string) (string, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func doInitAuth(cred *credentials, tokenPath string) error {
	authURL := buildAuthURL(cred.ClientID)
	fmt.Println("Open the following URL in your browser, approve access, then paste the code here:")
	fmt.Println(authURL)
	fmt.Print("Code: ")
	reader := bufio.NewReader(os.Stdin)
	code, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read code: %w", err)
	}
	code = strings.TrimSpace(code)
	resp, err := exchangeCode(cred, code)
	if err != nil {
		return err
	}
	return writeToken(tokenPath, tokenFile{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second),
	})
}

func buildAuthURL(clientID string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", "urn:ietf:wg:oauth:2.0:oob")
	params.Set("scope", calendarScope)
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	return fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?%s", params.Encode())
}

func exchangeCode(cred *credentials, code string) (*tokenResponse, error) {
	values := url.Values{}
	values.Set("code", code)
	values.Set("client_id", cred.ClientID)
	values.Set("client_secret", cred.ClientSecret)
	values.Set("redirect_uri", "urn:ietf:wg:oauth:2.0:oob")
	values.Set("grant_type", "authorization_code")
	values.Set("scope", calendarScope)
	res, err := http.PostForm(tokenEndpoint, values)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint: %s", res.Status)
	}
	var tok tokenResponse
	if err := json.NewDecoder(res.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tok.RefreshToken == "" {
		return nil, errors.New("token response missing refresh_token")
	}
	return &tok, nil
}

func writeToken(path string, data tokenFile) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	return os.WriteFile(path, raw, 0o600)
}

func loadToken(path string) (*tokenFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tok tokenFile
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func ensureAccessToken(cred *credentials, path string, tok *tokenFile) (string, error) {
	if time.Now().Before(tok.Expiry.Add(-10 * time.Second)) {
		return tok.AccessToken, nil
	}
	resp, err := refreshToken(cred, tok.RefreshToken)
	if err != nil {
		return "", err
	}
	refreshToken := tok.RefreshToken
	if resp.RefreshToken != "" {
		refreshToken = resp.RefreshToken
	}
	updated := tokenFile{
		AccessToken:  resp.AccessToken,
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second),
	}
	if err := writeToken(path, updated); err != nil {
		return "", fmt.Errorf("persist refreshed token: %w", err)
	}
	return updated.AccessToken, nil
}

func refreshToken(cred *credentials, refresh string) (*tokenResponse, error) {
	values := url.Values{}
	values.Set("client_id", cred.ClientID)
	values.Set("client_secret", cred.ClientSecret)
	values.Set("refresh_token", refresh)
	values.Set("grant_type", "refresh_token")
	res, err := http.PostForm(tokenEndpoint, values)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh endpoint: %s", res.Status)
	}
	var tok tokenResponse
	if err := json.NewDecoder(res.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}
	return &tok, nil
}

func fetchAndPrintEvents(calendarID string, hours int, accessToken string) error {
	now := time.Now().UTC()
	timeMin := now.Format(time.RFC3339)
	timeMax := now.Add(time.Duration(hours) * time.Hour).Format(time.RFC3339)
	path := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/%s/events", url.PathEscape(calendarID))
	params := url.Values{}
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")
	params.Set("timeMin", timeMin)
	params.Set("timeMax", timeMax)
	req, err := http.NewRequest("GET", fmt.Sprintf("%s?%s", path, params.Encode()), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("calendar API: %s - %s", res.Status, strings.TrimSpace(string(body)))
	}
	var cal calendarResponse
	if err := json.NewDecoder(res.Body).Decode(&cal); err != nil {
		return err
	}
	for _, item := range cal.Items {
		start := parseEventTime(item.Start.DateTime, item.Start.Date)
		end := parseEventTime(item.End.DateTime, item.End.Date)
		summary := item.Summary
		if summary == "" {
			summary = "(no title)"
		}
		fmt.Printf("%s - %s : %s\n", start, end, summary)
	}
	return nil
}

func parseEventTime(dateTime, date string) string {
	if dateTime != "" {
		if t, err := time.Parse(time.RFC3339, dateTime); err == nil {
			return t.Format("2006-01-02 15:04")
		}
	}
	if date != "" {
		return date + " (all day)"
	}
	return "?"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
