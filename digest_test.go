package main

import (
	"strings"
	"testing"
	"time"
)

func TestDigestWindowOrDefault(t *testing.T) {
	tests := []struct {
		rangeParam string
		want       time.Duration
	}{
		{"daily", 24 * time.Hour},
		{"weekly", 7 * 24 * time.Hour},
		{"monthly", 30 * 24 * time.Hour},
		{"", 24 * time.Hour},
		{"garbage", 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := digestWindowOrDefault(tt.rangeParam); got != tt.want {
			t.Errorf("digestWindowOrDefault(%q) = %v, want %v", tt.rangeParam, got, tt.want)
		}
	}
}

func TestBuildDigestQuery(t *testing.T) {
	since := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	q, args := buildDigestQuery(since)

	if !strings.Contains(q, "a2.feed_url <> a1.feed_url") {
		t.Fatalf("missing cross-feed condition: %s", q)
	}
	if !strings.Contains(q, "a1.title % a2.title") {
		t.Fatalf("missing trigram similarity condition: %s", q)
	}
	if !strings.Contains(q, "GROUP BY a1.id") {
		t.Fatalf("missing GROUP BY: %s", q)
	}
	if !strings.Contains(q, "ORDER BY cross_feed_count DESC, a1.publish_date DESC") {
		t.Fatalf("missing ORDER BY: %s", q)
	}
	if len(args) != 1 || args[0] != since {
		t.Fatalf("expected single since arg, got %v", args)
	}
}
