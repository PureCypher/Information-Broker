package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"information-broker/config"
)

// runBackfill re-fetches and re-extracts every article whose URL matches
// pattern (e.g. "theregister.com") with the current extractMainContent,
// updates full_content, and clears summary so the pipeline regenerates it.
// One-off maintenance command: `information-broker backfill <pattern>`.
func runBackfill(db *sql.DB, cfg *config.Config, pattern string) error {
	rows, err := db.Query(`SELECT id, url FROM articles WHERE url ILIKE '%' || $1 || '%' ORDER BY id`, pattern)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	type item struct {
		id  int64
		url string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.url); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	rows.Close()
	log.Printf("backfill: %d articles matching %q", len(items), pattern)

	client := &http.Client{Timeout: 30 * time.Second}
	maxLen := cfg.Performance.MaxArticleContentLength
	updated, skipped, failed := 0, 0, 0
	for i, it := range items {
		content, err := backfillFetch(client, cfg.API.UserAgent, it.url, maxLen)
		if err != nil {
			log.Printf("  [%d/%d] id=%d FAIL: %v", i+1, len(items), it.id, err)
			failed++
			time.Sleep(2 * time.Second)
			continue
		}
		if len(content) < 400 {
			log.Printf("  [%d/%d] id=%d only %d chars, skip", i+1, len(items), it.id, len(content))
			skipped++
			time.Sleep(2 * time.Second)
			continue
		}
		if _, err := db.Exec(`UPDATE articles SET full_content=$1, summary=NULL, updated_at=NOW() WHERE id=$2`,
			sanitizeUTF8(content), it.id); err != nil {
			log.Printf("  id=%d UPDATE FAIL: %v", it.id, err)
			failed++
			continue
		}
		updated++
		if updated%10 == 0 {
			log.Printf("  progress: %d/%d updated=%d", i+1, len(items), updated)
		}
		time.Sleep(1500 * time.Millisecond) // politeness / rate limit
	}
	log.Printf("backfill done: updated=%d skipped=%d failed=%d (total %d)", updated, skipped, failed, len(items))
	return nil
}

func backfillFetch(client *http.Client, ua, url string, maxLen int) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(extractMainContent(doc))
	if len(content) > maxLen {
		content = safeTruncate(content, maxLen) + "..."
	}
	return content, nil
}
