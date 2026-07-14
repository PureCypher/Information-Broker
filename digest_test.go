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

	if strings.Contains(q, "regexp_replace") || strings.Contains(q, "normTitleSQL") {
		t.Fatalf("query must not use the old title-normalization GROUP BY: %s", q)
	}
	if !strings.Contains(q, "GROUP BY story_cluster_id") {
		t.Fatalf("missing GROUP BY on story_cluster_id: %s", q)
	}
	if !strings.Contains(q, "story_cluster_id IS NOT NULL") {
		t.Fatalf("subquery must exclude unclustered rows: %s", q)
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

func TestBuildDigestQueryIncludesUnclusteredArticles(t *testing.T) {
	since := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	q, _ := buildDigestQuery(since)

	if strings.Contains(q, "\n\t\tJOIN (") || !strings.Contains(q, "LEFT JOIN (") {
		t.Fatalf("query must use LEFT JOIN so unclustered articles (story_cluster_id IS NULL) aren't silently dropped from the result set: %s", q)
	}
	if !strings.Contains(q, "COALESCE(cluster_counts.distinct_feeds - 1, 0)") {
		t.Fatalf("cross_feed_count must default to 0 via COALESCE for unclustered articles, not NULL: %s", q)
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
