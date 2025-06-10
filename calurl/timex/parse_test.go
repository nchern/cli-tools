package timex

import (
	"testing"
	"time"
)

func TestParseHumanShould(t *testing.T) {
	now := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		given    string
		expected time.Time
	}{
		{
			given:    "2025-05-03T14:17",
			expected: time.Date(2025, 5, 3, 14, 17, 0, 0, time.UTC),
		},
		{
			given:    "2025-05-03 at 14:10",
			expected: time.Date(2025, 5, 3, 14, 10, 0, 0, time.UTC),
		},
		{
			given:    "2025-05-04 at 9am",
			expected: time.Date(2025, 5, 4, 9, 0, 0, 0, time.UTC),
		},
		{
			given:    "tomorrow at 10am",
			expected: time.Date(2025, 5, 2, 10, 0, 0, 0, time.UTC),
		},
		{
			given:    "next day at 10am",
			expected: time.Date(2025, 5, 2, 10, 0, 0, 0, time.UTC),
		},
		{
			given:    "next week at 15:30",
			expected: time.Date(2025, 5, 8, 15, 30, 0, 0, time.UTC),
		},
		{
			given:    "next sun at 4:57pm", // sunday start of next week
			expected: time.Date(2025, 5, 4, 16, 57, 0, 0, time.UTC),
		},
		{
			given:    "next mon at 9am", // monday after the current week
			expected: time.Date(2025, 5, 5, 9, 0, 0, 0, time.UTC),
		},
		{
			given:    "next fri at 18:00", // fri of current week
			expected: time.Date(2025, 5, 2, 18, 0, 0, 0, time.UTC),
		},
		{
			given:    "today at 7pm",
			expected: time.Date(2025, 5, 1, 19, 0, 0, 0, time.UTC),
		},
		{
			given:    "2025-06-12 at 15:48",
			expected: time.Date(2025, 6, 12, 15, 48, 0, 0, time.UTC),
		},
		{
			given:    "jul 17th",
			expected: time.Date(2025, 7, 17, 0, 0, 0, 0, time.UTC),
		},
		{
			given:    "august 2nd at 14:00",
			expected: time.Date(2025, 8, 2, 14, 0, 0, 0, time.UTC),
		},
		{
			given:    "jul 3rd at 10am",
			expected: time.Date(2025, 7, 3, 10, 0, 0, 0, time.UTC),
		},
		{
			given:    "dec 31 at 23:00",
			expected: time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC),
		},
		{
			given:    "in 3 days at 10am",
			expected: time.Date(2025, 5, 4, 10, 0, 0, 0, time.UTC),
		},
		{
			given:    "in 1 week",
			expected: time.Date(2025, 5, 8, 0, 0, 0, 0, time.UTC),
		},
		{
			given:    "in 2 weeks at 13:00",
			expected: time.Date(2025, 5, 15, 13, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		tt := tt // capture loop variable
		t.Run(tt.given, func(t *testing.T) {
			got, err := ParseHuman(now, tt.given)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.expected) {
				t.Errorf("got %v; want %v", got, tt.expected)
			}
		})
	}
}

func TestParseHumanShouldFailOn(t *testing.T) {
	now := time.Date(2025, 5, 3, 12, 0, 0, 0, time.UTC)
	tests := []string{
		"",
		"next",
		"next unknown at 10am",
		"at",
		"tomorrow at invalid",
		"foo bar",
	}

	for _, given := range tests {
		given := given
		t.Run(given, func(t *testing.T) {
			_, err := ParseHuman(now, given)
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}
