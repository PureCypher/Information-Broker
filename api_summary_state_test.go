package main

import (
	"os"
	"regexp"
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
		content string
		want    string
	}{
		{"nil summary with content is pending", nil, "Real article text.", "pending"},
		{"nil summary without content is unavailable (nothing can summarize it)", nil, "", "unavailable"},
		{"nil summary with whitespace content is unavailable", nil, "  \n\t ", "unavailable"},
		{"empty-string summary is available (summarizer never writes empty)", ptr(""), "", "available"},
		{"fallback literal is failed", &unavailable, "", "failed"},
		{"fallback literal case-insensitive", &upper, "", "failed"},
		{"fallback literal with padding", &padded, "", "failed"},
		{"real summary is available", &content, "", "available"},
	}
	for _, c := range cases {
		if got := summaryStateOf(c.summary, c.content); got != c.want {
			t.Errorf("%s: summaryStateOf(%v, %q) = %q, want %q", c.name, c.summary, c.content, got, c.want)
		}
	}
}

func ptr(s string) *string { return &s }

// The list total's WHERE clause must exactly mirror the listing's WHERE clause,
// or pagination totals disagree with what's actually shown.
func TestBuildArticlesWhereMatchesQuery(t *testing.T) {
	cases := []struct {
		name       string
		feeds      []string
		q          string
		wantSubstr []string
		wantArgs   int
	}{
		{"no filters", nil, "", []string{""}, 0},
		{"feed only", []string{"https://example.com/rss"}, "", []string{"feed_url = $1"}, 1},
		{"search only", nil, "ransomware", []string{"title ILIKE $1", "summary ILIKE $2", "full_content ILIKE $3"}, 3},
		{"feed and search", []string{"https://example.com/rss"}, "cve", []string{"feed_url = $1", "title ILIKE $2", "summary ILIKE $3", "full_content ILIKE $4"}, 4},
		{"short search ignored", nil, "a", []string{""}, 0},
	}
	for _, c := range cases {
		where, args, nextIdx := buildArticlesWhere(c.feeds, c.q)
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

// TestArticleStatesBothRepos: the SmellyFeet frontend must show the same state
// this API decides for an article; a shared Go-side contract test guards it.
func TestArticleStatesBothRepos(t *testing.T) {
	if _, err := os.Stat("../../SmellyFeet/internal/server/templates/article.html"); err != nil {
		t.Skip("frontend repo not checked out next to backend")
	}
	// The backend's summary_state vocabulary: "available" is the default
	// branch (a summary renders directly), the rest are explicit template
	// branches. Every non-default state the backend can emit must have a
	// branch in BOTH templates, or the UI lies about it.
	wantStates := []string{"pending", "failed", "unavailable"}
	for _, tmpl := range []string{"article.html", "list.html"} {
		raw, err := os.ReadFile("../../SmellyFeet/internal/server/templates/" + tmpl)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, m := range regexp.MustCompile(`eq \.SummaryState "(\w+)"`).FindAllStringSubmatch(string(raw), -1) {
			got[m[1]] = true
		}
		for _, want := range wantStates {
			if !got[want] {
				t.Errorf("%s: no branch for summary_state %q (has %v) — frontend would misrender it", tmpl, want, got)
			}
		}
	}
}
