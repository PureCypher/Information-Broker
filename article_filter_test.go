package main

import (
	"testing"
	"time"
)

func TestClampFuturePublishDate(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		publishDate time.Time
		want        time.Time
	}{
		{
			name:        "past date passes through unchanged",
			publishDate: now.Add(-3 * time.Hour),
			want:        now.Add(-3 * time.Hour),
		},
		{
			name:        "slightly future date passes through",
			publishDate: now.Add(23 * time.Hour),
			want:        now.Add(23 * time.Hour),
		},
		{
			name:        "date 24h in the future passes through",
			publishDate: now.Add(24 * time.Hour),
			want:        now.Add(24 * time.Hour),
		},
		{
			name:        "far-future event date clamped to now",
			publishDate: now.Add(25 * time.Hour),
			want:        now,
		},
		{
			name:        "conference promo a year out clamped to now",
			publishDate: now.AddDate(1, 0, 0),
			want:        now,
		},
	}
	for _, c := range cases {
		if got := clampFuturePublishDate(c.publishDate, now); !got.Equal(c.want) {
			t.Errorf("%s: clampFuturePublishDate() = %v, want %v", c.name, got, c.want)
		}
	}
}
