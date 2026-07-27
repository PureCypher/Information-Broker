package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// digestWindowDays is the rolling look-back for each digest range, in days.
//
// Rolling, NOT calendar-period-to-date. The calendar version ("weekly" =
// since Monday 00:00, "monthly" = since the 1st) collapsed at every period
// boundary: on a Monday the weekly digest covered the same window as the
// daily one and returned an identical, near-empty page; likewise monthly on
// the 1st, quarterly on Jan/Apr/Jul/Oct 1st. A digest is a "what mattered
// recently" view, so the window has to stay the advertised width whatever
// day it is asked for.
var digestWindowDays = map[string]int{
	"daily":      1,
	"weekly":     7,
	"monthly":    30,
	"quarterly":  90,
	"halfyearly": 182,
	"yearly":     365,
}

// digestSinceOrDefault maps a digest range parameter to the start of its
// rolling look-back window. Unknown or empty values fall back to "daily" —
// same whitelist-and-normalize style as buildArticlesQuery's sort param.
// now should be normalized to a single timezone (UTC) by the caller.
func digestSinceOrDefault(rangeParam string, now time.Time) time.Time {
	days, ok := digestWindowDays[rangeParam]
	if !ok {
		days = digestWindowDays["daily"]
	}
	return now.AddDate(0, 0, -days)
}

// digestCountSince returns the start of the cross-feed COUNTING window,
// which is deliberately wider than the display window.
//
// Corroboration accumulates over days, not hours: measured on the live
// corpus, only ~53% of second-feed pickups land within 24h of the first
// report (median 21h, p75 over four days). Counting distinct feeds only
// among articles inside a one-day display window therefore credited almost
// nothing — the daily digest surfaced 2 multi-feed stories out of 228
// clusters, because yesterday's corroborating coverage fell outside the
// count. Counting reaches back at least clusterWindowDays instead: that is
// how long ClusteringScheduler keeps a cluster eligible for new members, so
// it is exactly the span over which a story can still gain coverage.
//
// The listed rows are unaffected — only the count widens — so the digest
// still shows what was published in the requested window, now ranked by how
// much of the press has actually picked the story up.
func digestCountSince(since, now time.Time, clusterWindowDays int) time.Time {
	if clusterWindowDays <= 0 {
		return since
	}
	lifetime := now.AddDate(0, 0, -clusterWindowDays)
	if lifetime.Before(since) {
		return lifetime
	}
	return since
}

// minCrossFeedCountForImportant is the cross-feed coverage threshold for the
// "important" bucket of a digest: a story counts as important once at least
// this many *other* feeds ran something with a similar title in the window.
const minCrossFeedCountForImportant = 2

// buildDigestQuery returns the SQL and args for the cross-feed importance
// heuristic: for every article published since `since`, count how many
// *other* feeds have an article in the same precomputed story cluster
// (story_cluster_id, assigned by ClusteringScheduler via summary embedding
// similarity) in the same window. This replaces two earlier live-computed
// approaches: a pg_trgm self-join (timed out past ~2k rows -- trigram GIN
// indexes don't accelerate column-to-column joins) and a GROUP BY on
// normalized title (fast, but too strict -- outlets reword headlines for
// the same event, so daily/weekly digests rarely populated "important").
// Precomputing via embeddings (see clustering_scheduler.go) catches those
// reworded-but-same-story cases; this query is now a plain indexed GROUP BY.
//
// ponytail: story_cluster_id is NULL for articles the clustering job hasn't
// reached yet (its own ticker interval, gated further by summarization
// activity) -- they're excluded from cross_feed_count here but still show
// up in the digest's "everything else" bucket via the outer WHERE clause,
// and get a cluster on the next cycle.
//
// The two time bounds are intentionally different (see digestCountSince):
// $1 bounds which articles are LISTED (the display window), $2 bounds how
// far back coverage is COUNTED (the corroboration window). Collapsing them
// back into one parameter reintroduces the empty-daily-digest bug.
//
// The `<= now()` upper bound keeps the digest retrospective. A few feeds
// (brighttalk.com, darkreading.com) date scheduled webinars at the event
// date, weeks ahead; with no upper bound those satisfy every window until
// the event happens, so an unaired webcast pinned itself to the top of the
// daily digest. They are legitimate entries, just not "what happened
// recently" -- the frontend lists them separately via splitUpcoming. The
// count subquery is deliberately left unbounded: a future-dated sibling
// still evidences that a story is being covered.
func buildDigestQuery(since, countSince time.Time) (string, []interface{}) {
	query := `SELECT a.id, a.title, a.url, a.summary, a.full_content, a.publish_date,
		a.fetch_duration_ms, a.feed_url, a.content_hash,
		COALESCE(cluster_counts.distinct_feeds - 1, 0) AS cross_feed_count, a.story_cluster_id
		FROM articles a
		LEFT JOIN (
			SELECT story_cluster_id, COUNT(DISTINCT feed_url) AS distinct_feeds
			FROM articles
			WHERE publish_date >= $2 AND story_cluster_id IS NOT NULL
			GROUP BY story_cluster_id
		) cluster_counts ON cluster_counts.story_cluster_id = a.story_cluster_id
		WHERE a.publish_date >= $1 AND a.publish_date <= now()
		ORDER BY cross_feed_count DESC, a.publish_date DESC`
	return query, []interface{}{since, countSince}
}

// splitImportant partitions digest rows into important (>= minCrossFeedCountForImportant
// other feeds) and everything else, preserving the query's incoming order in both groups.
func splitImportant(rows []ArticleView) (important, other []ArticleView) {
	important = []ArticleView{}
	other = []ArticleView{}
	for _, a := range rows {
		if a.CrossFeedCount >= minCrossFeedCountForImportant {
			important = append(important, a)
		} else {
			other = append(other, a)
		}
	}
	return important, other
}

// DigestResult is the response envelope for GET /articles/digest.
type DigestResult struct {
	Range     string        `json:"range"`
	Since     time.Time     `json:"since"`
	Important []ArticleView `json:"important"`
	Other     []ArticleView `json:"other"`
}

var validDigestRanges = map[string]bool{
	"daily": true, "weekly": true, "monthly": true,
	"quarterly": true, "halfyearly": true, "yearly": true,
}

// getArticlesDigest returns articles bucketed into "important" (multi-feed
// coverage) and "other" since the start of the requested calendar period
// (day/week/month/quarter/half-year/year).
func (s *APIServer) getArticlesDigest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rangeParam := r.URL.Query().Get("range")
	if !validDigestRanges[rangeParam] {
		rangeParam = "daily"
	}
	now := time.Now().UTC()
	since := digestSinceOrDefault(rangeParam, now)
	countSince := digestCountSince(since, now, s.config.Clustering.WindowDays)

	query, args := buildDigestQuery(since, countSince)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		log.Printf("Database query error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	all := []ArticleView{}
	for rows.Next() {
		var a ArticleView
		var fetchDurationMs int64
		var clusterID sql.NullInt64
		err := rows.Scan(
			&a.ID, &a.Title, &a.URL, &a.Summary, &a.Content, &a.PublishedAt,
			&fetchDurationMs, &a.FeedURL, &a.ContentHash, &a.CrossFeedCount, &clusterID,
		)
		if err != nil {
			log.Printf("Row scan error: %v", err)
			continue
		}
		a.FetchDuration = time.Duration(fetchDurationMs) * time.Millisecond
		if clusterID.Valid {
			// clusterID is redeclared each iteration, so every article gets
			// its own pointer rather than aliasing a shared loop variable.
			a.StoryClusterID = &clusterID.Int64
		}
		all = append(all, a)
	}

	important, other := splitImportant(all)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(DigestResult{
		Range: rangeParam, Since: since, Important: important, Other: other,
	})
}
