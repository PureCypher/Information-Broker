package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// digestWindowOrDefault maps a digest range parameter to a lookback window.
// Unknown or empty values fall back to the daily (24h) window — same
// whitelist-and-normalize style as buildArticlesQuery's sort param.
func digestWindowOrDefault(rangeParam string) time.Duration {
	switch rangeParam {
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// minCrossFeedCountForImportant is the cross-feed coverage threshold for the
// "important" bucket of a digest: a story counts as important once at least
// this many *other* feeds ran something with a similar title in the window.
const minCrossFeedCountForImportant = 2


// buildDigestQuery returns the SQL and args for the cross-feed importance
// heuristic: for every article published since `since`, count how many
// *other* feeds have an article in the same precomputed story cluster
// (story_cluster_id, assigned by ClusteringScheduler via title embedding
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
func buildDigestQuery(since time.Time) (string, []interface{}) {
	query := `SELECT a.id, a.title, a.url, a.summary, a.full_content, a.publish_date,
		a.fetch_duration_ms, a.feed_url, a.content_hash,
		COALESCE(cluster_counts.distinct_feeds - 1, 0) AS cross_feed_count
		FROM articles a
		LEFT JOIN (
			SELECT story_cluster_id, COUNT(DISTINCT feed_url) AS distinct_feeds
			FROM articles
			WHERE publish_date >= $1 AND story_cluster_id IS NOT NULL
			GROUP BY story_cluster_id
		) cluster_counts ON cluster_counts.story_cluster_id = a.story_cluster_id
		WHERE a.publish_date >= $1
		ORDER BY cross_feed_count DESC, a.publish_date DESC`
	return query, []interface{}{since}
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

var validDigestRanges = map[string]bool{"daily": true, "weekly": true, "monthly": true}

// getArticlesDigest returns articles bucketed into "important" (multi-feed
// coverage) and "other" for the requested daily/weekly/monthly window.
func (s *APIServer) getArticlesDigest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rangeParam := r.URL.Query().Get("range")
	if !validDigestRanges[rangeParam] {
		rangeParam = "daily"
	}
	since := time.Now().Add(-digestWindowOrDefault(rangeParam))

	query, args := buildDigestQuery(since)
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
		err := rows.Scan(
			&a.ID, &a.Title, &a.URL, &a.Summary, &a.Content, &a.PublishedAt,
			&fetchDurationMs, &a.FeedURL, &a.ContentHash, &a.CrossFeedCount,
		)
		if err != nil {
			log.Printf("Row scan error: %v", err)
			continue
		}
		a.FetchDuration = time.Duration(fetchDurationMs) * time.Millisecond
		all = append(all, a)
	}

	important, other := splitImportant(all)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(DigestResult{
		Range: rangeParam, Since: since, Important: important, Other: other,
	})
}
