package main

import (
	"strings"
	"testing"
	"time"
)

func TestDigestSinceOrDefault(t *testing.T) {
	// 2026-07-15 is a Wednesday, in Q3 and H2 of its year.
	wed := time.Date(2026, 7, 15, 14, 30, 0, 0, time.UTC)
	// 2026-02-10 is in Q1 and H1, to check the other half/quarter boundary.
	feb := time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		now        time.Time
		rangeParam string
		want       time.Time
	}{
		{"daily", wed, "daily", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)},
		{"weekly aligns to Monday", wed, "weekly", time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)},
		{"monthly", wed, "monthly", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"quarterly Q3", wed, "quarterly", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"quarterly Q1", feb, "quarterly", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"halfyearly H2", wed, "halfyearly", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"halfyearly H1", feb, "halfyearly", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"yearly", wed, "yearly", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"empty falls back to daily", wed, "", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)},
		{"garbage falls back to daily", wed, "garbage", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := digestSinceOrDefault(tt.rangeParam, tt.now); !got.Equal(tt.want) {
				t.Errorf("digestSinceOrDefault(%q, %v) = %v, want %v", tt.rangeParam, tt.now, got, tt.want)
			}
		})
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
