package main

import (
	"strings"
	"testing"
)

func TestBuildArticlesQuery(t *testing.T) {
	t.Run("no filters", func(t *testing.T) {
		q, args := buildArticlesQuery("", "", 50, 0)
		if strings.Contains(q, "WHERE") {
			t.Fatalf("expected no WHERE clause, got: %s", q)
		}
		if !strings.Contains(q, "ORDER BY publish_date DESC") {
			t.Fatalf("missing ORDER BY: %s", q)
		}
		if len(args) != 2 { // limit, offset
			t.Fatalf("expected 2 args, got %d: %v", len(args), args)
		}
	})

	t.Run("feed only", func(t *testing.T) {
		q, args := buildArticlesQuery("https://example.com/rss", "", 50, 0)
		if !strings.Contains(q, "feed_url = $1") {
			t.Fatalf("missing feed filter: %s", q)
		}
		if len(args) != 3 || args[0] != "https://example.com/rss" {
			t.Fatalf("unexpected args: %v", args)
		}
	})

	t.Run("query only", func(t *testing.T) {
		q, args := buildArticlesQuery("", "ransomware", 50, 0)
		if !strings.Contains(q, "ILIKE") {
			t.Fatalf("missing ILIKE search: %s", q)
		}
		if len(args) != 5 { // 3 like args + limit + offset
			t.Fatalf("expected 5 args, got %d: %v", len(args), args)
		}
		if args[0] != "%ransomware%" {
			t.Fatalf("expected wrapped like arg, got %v", args[0])
		}
	})

	t.Run("feed and query", func(t *testing.T) {
		q, args := buildArticlesQuery("https://example.com/rss", "cve", 10, 20)
		if !strings.Contains(q, "feed_url = $1") || !strings.Contains(q, "ILIKE $2") {
			t.Fatalf("expected both filters with correct placeholders: %s", q)
		}
		if len(args) != 6 {
			t.Fatalf("expected 6 args, got %d: %v", len(args), args)
		}
		if args[0] != "https://example.com/rss" {
			t.Fatalf("expected feed arg first, got %v", args[0])
		}
		if args[1] != "%cve%" {
			t.Fatalf("expected wrapped like arg second, got %v", args[1])
		}
		if args[len(args)-2] != 10 || args[len(args)-1] != 20 {
			t.Fatalf("limit/offset should be last: %v", args)
		}
	})

	t.Run("short query ignored", func(t *testing.T) {
		q, args := buildArticlesQuery("", "a", 50, 0)
		if strings.Contains(q, "ILIKE") {
			t.Fatalf("expected no ILIKE search for short query, got: %s", q)
		}
		if len(args) != 2 { // just limit, offset
			t.Fatalf("expected 2 args, got %d: %v", len(args), args)
		}

		q, args = buildArticlesQuery("", "   ", 50, 0)
		if strings.Contains(q, "ILIKE") {
			t.Fatalf("expected no ILIKE search for whitespace query, got: %s", q)
		}
		if len(args) != 2 { // just limit, offset
			t.Fatalf("expected 2 args, got %d: %v", len(args), args)
		}
	})
}
