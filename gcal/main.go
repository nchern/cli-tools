package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

const defaultMaxEvents = 10

var (
	credentialsPath = flag.String("credentials", defaultCredentialsPath(), "path to Google OAuth client credentials JSON")
	tokenPath       = flag.String("token", defaultTokenPath(), "path to cached OAuth token JSON")
	calendarID      = flag.String("calendar", "primary", "calendar ID to read")
	sinceHour       = flag.Int("since", -100, "start offset in hours from now")
	untilHour       = flag.Int("until", 30, "end offset in hours from now")
	maxEvents       = flag.Int("max", defaultMaxEvents, "maximum events to return")
)

func main() {
	flag.Parse()
	now := time.Now()

	since := now.Add(time.Duration(*sinceHour) * time.Hour)
	until := now.Add(time.Duration(*untilHour) * time.Hour)

	events, err := fetchEvents(*calendarID, *credentialsPath, *tokenPath, since, until)
	if err != nil {
		log.Fatalf("failed to fetch events: %v", err)
	}
	for _, event := range events.Items {
		fmt.Printf("%s %s\n", formatEventTime(event), event.Summary)
	}
}

func fetchEvents(calendarID, credentialsPath, tokenPath string, since, until time.Time) (*calendar.Events, error) {
	ctx := context.Background()
	client, err := authenticate(ctx, credentialsPath, tokenPath)
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	service, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create calendar service: %w", err)
	}
	events, err := service.Events.List(calendarID).
		ShowDeleted(false).
		SingleEvents(true).
		TimeMin(since.Format(time.RFC3339)).
		TimeMax(until.Format(time.RFC3339)).
		MaxResults(int64(*maxEvents)).
		OrderBy("startTime").
		Do()
	if err != nil {
		return nil, fmt.Errorf("read calendar events: %w", err)
	}
	return events, nil
}

func authenticate(ctx context.Context, credentialsPath, tokenPath string) (*http.Client, error) {
	if err := checkCredentialsPermissions(credentialsPath); err != nil {
		return nil, err
	}

	credentials, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials file %q: %w", credentialsPath, err)
	}

	config, err := google.ConfigFromJSON(credentials, calendar.CalendarReadonlyScope)
	if err != nil {
		return nil, fmt.Errorf("parse credentials file: %w", err)
	}
	log.Println("DEBUG", "clientID", config.ClientID)
	log.Println("DEBUG", "secret", config.ClientSecret)
	log.Println("DEBUG", "secret", config.Endpoint.AuthURL)

	token, err := tokenFromFile(tokenPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		token, err = tokenFromWeb(ctx, config)
		if err != nil {
			return nil, err
		}

		if err := saveToken(tokenPath, token); err != nil {
			return nil, err
		}
	}

	return config.Client(ctx, token), nil
}

func checkCredentialsPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat credentials file %q: %w", path, err)
	}

	if info.Mode().Perm() != 0600 {
		return fmt.Errorf("credentials file %q must have permissions 0600, got %04o", path, info.Mode().Perm())
	}

	return nil
}

func tokenFromFile(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	token := new(oauth2.Token)
	if err := json.Unmarshal(data, token); err != nil {
		return nil, fmt.Errorf("parse token file %q: %w", path, err)
	}

	return token, nil
}

func tokenFromWeb(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start local OAuth callback server: %w", err)
	}
	defer listener.Close()

	state, err := randomState()
	if err != nil {
		return nil, err
	}

	config.RedirectURL = "http://" + listener.Addr().String() + "/callback"
	fmt.Printf("Listening for OAuth callback on %s\n", config.RedirectURL)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("state"); got != state {
			trySendError(errCh, fmt.Errorf("OAuth state mismatch"))
			http.Error(w, "OAuth state mismatch", http.StatusBadRequest)
			return
		}

		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			trySendError(errCh, fmt.Errorf("OAuth authorization failed: %s", oauthErr))
			http.Error(w, oauthErr, http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			trySendError(errCh, fmt.Errorf("OAuth callback did not include a code"))
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		select {
		case codeCh <- code:
		default:
		}
		fmt.Fprintln(w, "Authorization complete. You can close this tab.")
	})

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			trySendError(errCh, fmt.Errorf("run OAuth callback server: %w", err))
		}
	}()
	defer server.Shutdown(context.Background())

	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline)
	fmt.Printf("Open this URL in your browser to authorize calendar access:\n%v\n", authURL)

	var authCode string
	select {
	case authCode = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	token, err := config.Exchange(ctx, authCode)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code for token: %w", err)
	}

	return token, nil
}

func trySendError(ch chan<- error, err error) {
	select {
	case ch <- err:
	default:
	}
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("create OAuth state: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func saveToken(path string, token *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write token file %q: %w", path, err)
	}

	return nil
}

func defaultCredentialsPath() string {
	return filepath.Join(defaultConfigDir(), "credentials.json")
}

func defaultTokenPath() string {
	return filepath.Join(defaultConfigDir(), "token.json")
}

func defaultConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(configDir, "gcal")
}

func formatEventTime(event *calendar.Event) string {
	if event.Start == nil {
		return "unknown"
	}

	if event.Start.Date != "" {
		return event.Start.Date + " 00:00-23:59"
	}

	startTime, err := time.Parse(time.RFC3339, event.Start.DateTime)
	if err != nil || event.End == nil {
		return event.Start.DateTime
	}

	endTime, err := time.Parse(time.RFC3339, event.End.DateTime)
	if err != nil {
		return event.Start.DateTime
	}

	startTime = startTime.Local()
	endTime = endTime.Local()
	return fmt.Sprintf("%s %s-%s", startTime.Format("2006-01-02"), startTime.Format("15:04"), endTime.Format("15:04"))
}
