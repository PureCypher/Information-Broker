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
// *other* feeds ran an article with the same normalized title in the same
// window, via a GROUP BY on lower(btrim(title)) — not a pg_trgm similarity
// self-join. An earlier version joined articles to themselves on
// `title % title`; the trgm GIN index only accelerates `title % 'literal'`,
// not column-to-column comparisons, so that self-join fell back to a full
// nested loop and timed out in production past ~2k rows (the weekly
// window). GROUP BY is O(n) and needs no index at this scale.
//
// ponytail: exact (post-normalization) title matching catches only
// byte-identical wire-service/syndicated headlines, not near-duplicates or
// editorially-rewritten cross-outlet coverage of the same event — treat
// cross_feed_count as a duplication signal, not a true importance score.
// Upgrade path: an index on lower(btrim(title)) if this table grows enough
// for the GROUP BY to need one, or embedding similarity / the existing
// Ollama summarizer for real near-duplicate matching.
func buildDigestQuery(since time.Time) (string, []interface{}) {
	query := `SELECT a.id, a.title, a.url, a.summary, a.full_content, a.publish_date,
		a.fetch_duration_ms, a.feed_url, a.content_hash,
		(feed_counts.distinct_feeds - 1) AS cross_feed_count
		FROM articles a
		JOIN (
			SELECT lower(btrim(title)) AS norm_title, COUNT(DISTINCT feed_url) AS distinct_feeds
			FROM articles
			WHERE publish_date >= $1
			GROUP BY lower(btrim(title))
		) feed_counts ON feed_counts.norm_title = lower(btrim(a.title))
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
