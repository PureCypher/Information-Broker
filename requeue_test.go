package main

import (
	"strings"
	"testing"
	"time"

	"information-broker/config"
)

func TestSelectRequeueCandidates(t *testing.T) {
	cutoff := time.Date(2025, 6, 25, 0, 0, 0, 0, time.UTC)
	published := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	isExcluded := func(feed string) bool { return strings.Contains(feed, "cvefeed.io") }

	articles := []requeueArticle{
		{URL: "https://a.example/1", Title: "kept", Content: "body", FeedURL: "https://a.example/feed", PublishDate: published},
		{URL: "https://cvefeed.io/2", Title: "excluded feed", Content: "body", FeedURL: "https://cvefeed.io/rssfeed", PublishDate: published},
		{URL: "https://a.example/3", Title: "no content", Content: "  \n", FeedURL: "https://a.example/feed", PublishDate: published},
		{URL: "https://a.example/4", Title: "before cutoff", Content: "body", FeedURL: "https://a.example/feed", PublishDate: cutoff.Add(-time.Hour)},
		{URL: "https://a.example/5", Title: "no publish date", Content: "body", FeedURL: "https://a.example/feed"},
	}

	got := selectRequeueCandidates(articles, isExcluded, cutoff)

	if len(got) != 1 || got[0].URL != "https://a.example/1" {
		t.Fatalf("want only the first article, got %+v", got)
	}
}

func TestSelectRequeueCandidatesKeepsOrder(t *testing.T) {
	published := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	in := []requeueArticle{
		{URL: "u1", Content: "x", PublishDate: published},
		{URL: "u2", Content: "x", PublishDate: published},
		{URL: "u3", Content: "x", PublishDate: published},
	}

	got := selectRequeueCandidates(in, func(string) bool { return false }, time.Time{})

	for i, want := range []string{"u1", "u2", "u3"} {
		if got[i].URL != want {
			t.Fatalf("position %d: want %s, got %s", i, want, got[i].URL)
		}
	}
}

func TestWithoutWebhooksDisablesPostingWithoutMutatingTheOriginal(t *testing.T) {
	original := config.DiscordConfig{
		WebhookURL:    "https://discord.invalid/single",
		WebhookURLs:   []string{"https://discord.invalid/a", "https://discord.invalid/b"},
		ExcludedFeeds: []string{"cvefeed.io"},
		Timeout:       30 * time.Second,
	}

	muted := withoutWebhooks(original)

	if got := muted.GetWebhookURLs(); len(got) != 0 {
		t.Fatalf("muted config must have no webhooks, got %v", got)
	}
	if !muted.IsFeedExcluded("https://cvefeed.io/rss") || muted.Timeout != original.Timeout {
		t.Fatalf("unrelated settings must be kept: %+v", muted)
	}
	if len(original.GetWebhookURLs()) != 2 || original.WebhookURL == "" {
		t.Fatalf("original must be untouched: %+v", original)
	}
}

func TestCapCandidatesBoundsBatchAndKeepsOrder(t *testing.T) {
	// Real-world use: 'repair' resets summaries in bounded batches and the
	// summarizer must catch up the same way — a run must be bounded by
	// --limit, keep the priority order, and pass through unchanged when the
	// batch is smaller than the limit or the limit is zero.
	in := []requeueArticle{
		{URL: "https://a.example/1", Title: "newest"},
		{URL: "https://a.example/2", Title: "middle"},
		{URL: "https://a.example/3", Title: "oldest"},
	}
	if got := capCandidates(in, 2); len(got) != 2 || got[0].URL != in[0].URL || got[1].URL != in[1].URL {
		t.Fatalf("limit 2 should keep the first two in order, got %+v", got)
	}
	if got := capCandidates(in, 10); len(got) != 3 {
		t.Fatalf("limit above batch size should keep everything, got %+v", got)
	}
	if got := capCandidates(in, 0); len(got) != 3 {
		t.Fatalf("limit 0 means unbounded, got %+v", got)
	}
	// The original slice must be untouched (the input can be reused by callers).
	if len(in) != 3 || in[0].URL != "https://a.example/1" {
		t.Fatalf("capCandidates must not mutate its input: %+v", in)
	}
}
