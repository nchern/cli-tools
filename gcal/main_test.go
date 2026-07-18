package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/api/calendar/v3"
)

func TestCheckCredentialsPermissionsAccepts0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := checkCredentialsPermissions(path); err != nil {
		t.Fatalf("checkCredentialsPermissions() error = %v, want nil", err)
	}
}

func TestCheckCredentialsPermissionsRejectsBroadPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := checkCredentialsPermissions(path); err == nil {
		t.Fatal("checkCredentialsPermissions() error = nil, want error")
	}
}

func TestFormatEventTimeIncludesDateForTimedEvents(t *testing.T) {
	start := time.Date(2026, 7, 18, 14, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 18, 15, 0, 0, 0, time.Local)
	event := &calendar.Event{
		Start: &calendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:   &calendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
	}

	got := formatEventTime(event)
	want := "2026-07-18 14:00-15:00"
	if got != want {
		t.Fatalf("formatEventTime() = %q, want %q", got, want)
	}
}

func TestFormatEventTimeAllDayEvents(t *testing.T) {
	event := &calendar.Event{
		Start: &calendar.EventDateTime{Date: "2026-07-18"},
		End:   &calendar.EventDateTime{Date: "2026-07-19"},
	}

	got := formatEventTime(event)
	want := "2026-07-18 00:00-23:59"
	if got != want {
		t.Fatalf("formatEventTime() = %q, want %q", got, want)
	}
}
