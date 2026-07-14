package main

import "time"

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
// *other* feeds (feed_url <> a1.feed_url) ran a similarly-titled story
// (pg_trgm's `%` operator, backed by the existing idx_articles_title_trgm
// GIN index) in the same window.
//
// ponytail: title trigram similarity catches near-duplicate/syndicated
// headlines, not editorially-rewritten cross-outlet coverage of the same
// event — treat cross_feed_count as a duplication signal, not a true
// importance score. Upgrade path: embedding similarity or the existing
// Ollama summarizer, if this proves too weak in practice.
func buildDigestQuery(since time.Time) (string, []interface{}) {
	query := `SELECT a1.id, a1.title, a1.url, a1.summary, a1.full_content, a1.publish_date,
		a1.fetch_duration_ms, a1.feed_url, a1.content_hash, COUNT(DISTINCT a2.feed_url) AS cross_feed_count
		FROM articles a1
		LEFT JOIN articles a2
		  ON a2.publish_date >= $1 AND a2.feed_url <> a1.feed_url AND a1.title % a2.title
		WHERE a1.publish_date >= $1
		GROUP BY a1.id
		ORDER BY cross_feed_count DESC, a1.publish_date DESC`
	return query, []interface{}{since}
}

// splitImportant partitions digest rows into important (>= minCrossFeedCountForImportant
// other feeds) and everything else, preserving the query's incoming order in both groups.
func splitImportant(rows []ArticleView) (important, other []ArticleView) {
	for _, a := range rows {
		if a.CrossFeedCount >= minCrossFeedCountForImportant {
			important = append(important, a)
		} else {
			other = append(other, a)
		}
	}
	return important, other
}
