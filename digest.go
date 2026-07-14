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

// normTitleSQL returns a SQL expression that normalizes a title column for
// cross-feed grouping: lowercases, strips a trailing "| Site Name" suffix
// (many outlets append their own name after a pipe), strips common
// punctuation -- including straight and typographic quotes, so ASCII vs.
// smart-quote variants of the same title still match -- and collapses
// whitespace. Uses dollar-quoted string literals ($re$...$re$) for the
// regex patterns so the embedded quote characters need no SQL escaping.
func normTitleSQL(column string) string {
	return `btrim(regexp_replace(regexp_replace(regexp_replace(lower(` + column + `),
		$re$\s*\|.*$re$, '', 'g'),
		$re$['"‘’“”.,:;!?()]$re$, '', 'g'),
		$re$\s+$re$, ' ', 'g'))`
}

// buildDigestQuery returns the SQL and args for the cross-feed importance
// heuristic: for every article published since `since`, count how many
// *other* feeds ran an article with the same normalized title in the same
// window, via a GROUP BY on normTitleSQL's normalized title -- not a
// pg_trgm similarity self-join. An earlier version joined articles to
// themselves on `title % title`; the trgm GIN index only accelerates
// `title % 'literal'`, not column-to-column comparisons, so that self-join
// fell back to a full nested loop and timed out in production past ~2k
// rows (the weekly window). GROUP BY is O(n) and needs no index at this
// scale. A first pass at the GROUP BY normalized on lower(btrim(title))
// alone, which was too strict in practice -- outlet-specific title
// formatting (trailing "| Site Name", differing punctuation/quote styles)
// kept genuinely-identical stories from matching, so daily/weekly digests
// rarely populated "important" at all. normTitleSQL strips that noise.
//
// ponytail: still an exact match after normalization, not fuzzy -- two
// outlets that genuinely reword a headline (not just punctuate or brand it
// differently) still won't match. Upgrade path: blocked similarity
// matching (bucket by shared significant words before comparing), or a
// precomputed clustering job (embeddings or the existing Ollama
// summarizer), if this proves too narrow.
func buildDigestQuery(since time.Time) (string, []interface{}) {
	normOuter := normTitleSQL("a.title")
	normSub := normTitleSQL("title")
	query := `SELECT a.id, a.title, a.url, a.summary, a.full_content, a.publish_date,
		a.fetch_duration_ms, a.feed_url, a.content_hash,
		(feed_counts.distinct_feeds - 1) AS cross_feed_count
		FROM articles a
		JOIN (
			SELECT ` + normSub + ` AS norm_title, COUNT(DISTINCT feed_url) AS distinct_feeds
			FROM articles
			WHERE publish_date >= $1
			GROUP BY ` + normSub + `
		) feed_counts ON feed_counts.norm_title = ` + normOuter + `
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
