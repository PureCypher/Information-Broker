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

	if strings.Contains(q, "a1.title % a2.title") || strings.Contains(q, "JOIN articles a2") {
		t.Fatalf("query must not use the pg_trgm self-join (O(n^2), times out past ~2k rows): %s", q)
	}
	// normTitleSQL is applied three times: subquery SELECT, subquery GROUP
	// BY, and the outer join's a.title -- each is a 3-call regexp_replace chain.
	if strings.Count(q, "regexp_replace") != 9 {
		t.Fatalf("expected normTitleSQL's 3-step regexp_replace chain applied 3x (subquery SELECT, subquery GROUP BY, outer join), got %d: %s",
			strings.Count(q, "regexp_replace"), q)
	}
	if !strings.Contains(q, "GROUP BY") {
		t.Fatalf("missing GROUP BY on normalized title: %s", q)
	}
	if !strings.Contains(q, "COUNT(DISTINCT feed_url)") {
		t.Fatalf("missing distinct-feed count: %s", q)
	}
	if !strings.Contains(q, "ORDER BY cross_feed_count DESC, a.publish_date DESC") {
		t.Fatalf("missing ORDER BY: %s", q)
	}
	if len(args) != 1 || args[0] != since {
		t.Fatalf("expected single since arg (bound twice via $1), got %v", args)
	}
}

func TestNormTitleSQLPatterns(t *testing.T) {
	expr := normTitleSQL("title")
	for _, want := range []string{
		`\s*\|.*`, // trailing "| Site Name" suffix
		`.,:;!?()`, // common punctuation, alongside smart quotes elsewhere in the class
		`\s+`,      // whitespace collapse
		"lower(title)",
		"btrim(",
	} {
		if !strings.Contains(expr, want) {
			t.Fatalf("normTitleSQL(%q) missing expected fragment %q: %s", "title", want, expr)
		}
	}
}

func TestSplitImportant(t *testing.T) {
	rows := []ArticleView{
		{ID: 1, CrossFeedCount: 3},
		{ID: 2, CrossFeedCount: 1},
		{ID: 3, CrossFeedCount: 2},
		{ID: 4, CrossFeedCount: 0},
	}
	important, other := splitImportant(rows)
	if len(important) != 2 || important[0].ID != 1 || important[1].ID != 3 {
		t.Fatalf("important = %+v, want IDs 1,3 in order", important)
	}
	if len(other) != 2 || other[0].ID != 2 || other[1].ID != 4 {
		t.Fatalf("other = %+v, want IDs 2,4 in order", other)
	}

	// Verify both buckets are non-nil empty slices when input is empty
	importantEmpty, otherEmpty := splitImportant([]ArticleView{})
	if importantEmpty == nil {
		t.Error("important should be non-nil empty slice, got nil")
	}
	if otherEmpty == nil {
		t.Error("other should be non-nil empty slice, got nil")
	}

	// Verify both buckets are non-nil when one is empty
	onlyOther := []ArticleView{{ID: 1, CrossFeedCount: 0}}
	imp, oth := splitImportant(onlyOther)
	if imp == nil {
		t.Error("important should be non-nil even when empty, got nil")
	}
	if len(imp) != 0 {
		t.Errorf("important should be empty, got %d items", len(imp))
	}
	if len(oth) != 1 {
		t.Errorf("other should have 1 item, got %d", len(oth))
	}
}
