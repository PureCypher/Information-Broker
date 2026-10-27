package main

import (
	"strings"
	"testing"
)

func TestSummaryStateOf(t *testing.T) {
	unavailable := "summary unavailable"
	upper := "Summary Unavailable"
	padded := "  summary unavailable  "
	content := "An actual LLM summary of the article."
	cases := []struct {
		name    string
		summary *string
		want    string
	}{
		{"nil summary is pending", nil, "pending"},
		{"empty-string summary is available (summarizer never writes empty)", ptr(""), "available"},
		{"fallback literal is failed", &unavailable, "failed"},
		{"fallback literal case-insensitive", &upper, "failed"},
		{"fallback literal with padding", &padded, "failed"},
		{"real summary is available", &content, "available"},
	}
	for _, c := range cases {
		if got := summaryStateOf(c.summary); got != c.want {
			t.Errorf("%s: summaryStateOf(%v) = %q, want %q", c.name, c.summary, got, c.want)
		}
	}
}

func ptr(s string) *string { return &s }

// The list total's WHERE clause must exactly mirror the listing's WHERE clause,
// or pagination totals disagree with what's actually shown.
func TestBuildArticlesWhereMatchesQuery(t *testing.T) {
	cases := []struct {
		name       string
		feed, q    string
		wantSubstr []string
		wantArgs   int
	}{
		{"no filters", "", "", []string{""}, 0},
		{"feed only", "https://example.com/rss", "", []string{"feed_url = $1"}, 1},
		{"search only", "", "ransomware", []string{"title ILIKE $1", "summary ILIKE $2", "full_content ILIKE $3"}, 3},
		{"feed and search", "https://example.com/rss", "cve", []string{"feed_url = $1", "title ILIKE $2", "summary ILIKE $3", "full_content ILIKE $4"}, 4},
		{"short search ignored", "", "a", []string{""}, 0},
	}
	for _, c := range cases {
		where, args, nextIdx := buildArticlesWhere(c.feed, c.q)
		for _, sub := range c.wantSubstr {
			if sub != "" && !strings.Contains(where, sub) {
				t.Errorf("%s: WHERE %q missing %q", c.name, where, sub)
			}
		}
		if c.wantSubstr[0] == "" && where != "" {
			t.Errorf("%s: want empty WHERE, got %q", c.name, where)
		}
		if len(args) != c.wantArgs {
			t.Errorf("%s: got %d args, want %d", c.name, len(args), c.wantArgs)
		}
		if nextIdx != c.wantArgs+1 {
			t.Errorf("%s: nextIdx = %d, want %d", c.name, nextIdx, c.wantArgs+1)
		}
	}
}
