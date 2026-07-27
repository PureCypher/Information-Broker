package main

import (
	"strings"
	"testing"
	"time"
)

func TestDigestSinceOrDefault(t *testing.T) {
	wed := time.Date(2026, 7, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name       string
		rangeParam string
		want       time.Time
	}{
		{"daily", "daily", wed.Add(-24 * time.Hour)},
		{"weekly", "weekly", wed.AddDate(0, 0, -7)},
		{"monthly", "monthly", wed.AddDate(0, 0, -30)},
		{"quarterly", "quarterly", wed.AddDate(0, 0, -90)},
		{"halfyearly", "halfyearly", wed.AddDate(0, 0, -182)},
		{"yearly", "yearly", wed.AddDate(0, 0, -365)},
		{"empty falls back to daily", "", wed.Add(-24 * time.Hour)},
		{"garbage falls back to daily", "garbage", wed.Add(-24 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := digestSinceOrDefault(tt.rangeParam, wed); !got.Equal(tt.want) {
				t.Errorf("digestSinceOrDefault(%q, %v) = %v, want %v", tt.rangeParam, wed, got, tt.want)
			}
		})
	}
}

// Windows are rolling, not calendar-period-to-date. The calendar version
// collapsed at every period boundary -- on a Monday "weekly" started at
// 00:00 that morning and returned exactly the same window as "daily",
// which is the bug this replaced. Each range must stay strictly wider than
// the one below it no matter what instant it is evaluated at.
func TestDigestWindowsAreStrictlyNestedAtEveryBoundary(t *testing.T) {
	boundaries := []struct {
		name string
		now  time.Time
	}{
		{"Monday 00:00 (old weekly==daily)", time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)},
		{"1st of month 00:00", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"1st of quarter and half-year", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"mid-week afternoon", time.Date(2026, 7, 15, 14, 30, 0, 0, time.UTC)},
	}
	order := []string{"daily", "weekly", "monthly", "quarterly", "halfyearly", "yearly"}
	for _, b := range boundaries {
		t.Run(b.name, func(t *testing.T) {
			for i := 1; i < len(order); i++ {
				narrow := digestSinceOrDefault(order[i-1], b.now)
				wide := digestSinceOrDefault(order[i], b.now)
				if !wide.Before(narrow) {
					t.Errorf("%q window (since %v) must reach strictly further back than %q (since %v)",
						order[i], wide, order[i-1], narrow)
				}
			}
		})
	}
}

// The cross-feed COUNTING window is deliberately decoupled from the display
// window: corroboration accumulates over days (median ~21h to a second feed,
// p75 over four days on the live corpus), so counting only inside a one-day
// display window credited almost nothing. Counting always reaches back at
// least the clustering window, since that is how long a story cluster can
// legitimately keep collecting coverage.
func TestDigestCountSince(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 30, 0, 0, time.UTC)
	clusterWindow := 35

	t.Run("short display window still counts back the full clustering window", func(t *testing.T) {
		since := digestSinceOrDefault("daily", now)
		want := now.AddDate(0, 0, -clusterWindow)
		if got := digestCountSince(since, now, clusterWindow); !got.Equal(want) {
			t.Errorf("digestCountSince = %v, want %v", got, want)
		}
	})

	t.Run("long display window counts over the display window itself", func(t *testing.T) {
		since := digestSinceOrDefault("yearly", now)
		if got := digestCountSince(since, now, clusterWindow); !got.Equal(since) {
			t.Errorf("digestCountSince = %v, want the display since %v", got, since)
		}
	})

	t.Run("never narrower than the display window", func(t *testing.T) {
		for _, r := range []string{"daily", "weekly", "monthly", "quarterly", "halfyearly", "yearly"} {
			since := digestSinceOrDefault(r, now)
			if got := digestCountSince(since, now, clusterWindow); got.After(since) {
				t.Errorf("%s: counting window (%v) must not start after the display window (%v)", r, got, since)
			}
		}
	})

	t.Run("non-positive clustering window degrades to the display window", func(t *testing.T) {
		since := digestSinceOrDefault("daily", now)
		if got := digestCountSince(since, now, 0); !got.Equal(since) {
			t.Errorf("digestCountSince with 0 cluster days = %v, want %v", got, since)
		}
	})
}

func TestBuildDigestQuery(t *testing.T) {
	since := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	countSince := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	q, args := buildDigestQuery(since, countSince)

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
	if len(args) != 2 || args[0] != since || args[1] != countSince {
		t.Fatalf("expected args [since, countSince], got %v", args)
	}
}

// The row filter and the feed count read different placeholders: rows are
// limited to the display window ($1) while the count reaches back over the
// wider corroboration window ($2). Binding both to $1 would silently restore
// the original bug, so assert the two clauses use different parameters.
func TestBuildDigestQueryCountsOverTheWiderWindow(t *testing.T) {
	since := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	countSince := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	q, _ := buildDigestQuery(since, countSince)

	subquery, _, ok := strings.Cut(q, ") cluster_counts")
	if !ok {
		t.Fatalf("could not locate the cluster_counts subquery: %s", q)
	}
	_, subquery, _ = strings.Cut(subquery, "LEFT JOIN (")

	if !strings.Contains(subquery, "publish_date >= $2") {
		t.Fatalf("feed count must range over the corroboration window ($2): %s", subquery)
	}
	if strings.Contains(subquery, "publish_date >= $1") {
		t.Fatalf("feed count must not be limited to the display window ($1): %s", subquery)
	}

	outer := q[strings.Index(q, ") cluster_counts"):]
	if !strings.Contains(outer, "WHERE a.publish_date >= $1") {
		t.Fatalf("listed rows must still be limited to the display window ($1): %s", outer)
	}
}

// Some feeds date entries in the future on purpose -- brighttalk.com and
// darkreading.com publish scheduled webinars whose publish_date is the event
// date, weeks out. With no upper bound they satisfy `publish_date >= since`
// in every window until the event happens, so an unaired webcast ranked at
// the top of the "last 24 hours" digest and stayed there. The digest is
// retrospective; the frontend surfaces these separately (splitUpcoming).
func TestBuildDigestQueryExcludesFutureDatedArticles(t *testing.T) {
	since := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	q, _ := buildDigestQuery(since, since.AddDate(0, 0, -35))

	outer := q[strings.Index(q, ") cluster_counts"):]
	if !strings.Contains(outer, "a.publish_date <= now()") {
		t.Fatalf("digest must not list articles dated in the future: %s", outer)
	}
}

func TestBuildDigestQueryIncludesUnclusteredArticles(t *testing.T) {
	since := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	q, _ := buildDigestQuery(since, since.AddDate(0, 0, -35))

	if strings.Contains(q, "\n\t\tJOIN (") || !strings.Contains(q, "LEFT JOIN (") {
		t.Fatalf("query must use LEFT JOIN so unclustered articles (story_cluster_id IS NULL) aren't silently dropped from the result set: %s", q)
	}
	if !strings.Contains(q, "COALESCE(cluster_counts.distinct_feeds - 1, 0)") {
		t.Fatalf("cross_feed_count must default to 0 via COALESCE for unclustered articles, not NULL: %s", q)
	}
}

func TestBuildDigestQuerySelectsStoryClusterID(t *testing.T) {
	since := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	q, _ := buildDigestQuery(since, since.AddDate(0, 0, -35))

	// The frontend groups digest rows by cluster, so the cluster key itself
	// has to come back on the row -- cross_feed_count alone says how many
	// feeds covered a story but not which articles those were. Asserted
	// against the SELECT list, not the whole query: a.story_cluster_id also
	// appears in the LEFT JOIN's ON clause, which would false-green a plain
	// substring check.
	selectList, _, _ := strings.Cut(q, "\n\t\tFROM articles a")
	if !strings.Contains(selectList, "AS cross_feed_count, a.story_cluster_id") {
		t.Fatalf("digest SELECT list must expose a.story_cluster_id for client-side grouping: %s", selectList)
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
