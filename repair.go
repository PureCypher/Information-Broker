package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"information-broker/config"
)

// repairBoilerplateMarkers are extraction-artifact strings observed in stored
// full_content: publisher "related/recent posts" widgets and cookie banners
// that an older, selector-missing extractor swept in. The marker check only
// selects which rows to re-extract; an update happens only when the
// re-extracted text actually differs, so a false-positive marker costs one
// re-fetch and no data change.
var repairBoilerplateMarkers = []string{
	"You might also like", // securityaffairs.com related-posts heading
	"Recent Articles",     // securityaffairs.com recent-widget heading
	"We use cookies",      // cookie-consent banners (securityaffairs, many WP sites)
	"Share this article",  // techradar.com 2026 share rail (also rapid7, elastic, intel471)
	"Subscribe to our newsletter", // future plc inline newsletter capture ("Are you a pro? ..."); also csoonline/IDG JS-rendered email-capture tail
	"View Deal",           // future plc hawk affiliate deal cards ("Save up to ... View Deal")
	"Read more:",          // techrepublic/zdnet/therecord/helpnetsecurity read-more widgets
}

type repairRow struct {
	ID          int64
	URL         string
	Title       string
	FullContent string
}

// selectRepairCandidates filters rows for a repair run: contaminated rows
// (content containing a boilerplate marker, case-insensitively — publishers
// render widget headings in arbitrary casing; or a first line that restates
// the headline), or every row with --all.
// Order is stable by ID, and limit>0 caps the batch (0 = no cap), so runs
// are bounded and safe to rerun.
func selectRepairCandidates(rows []repairRow, all bool, limit int) []repairRow {
	out := make([]repairRow, 0, len(rows))
	for _, r := range rows {
		if !all {
			lower := strings.ToLower(r.FullContent)
			contaminated := false
			for _, m := range repairBoilerplateMarkers {
				if strings.Contains(lower, strings.ToLower(m)) {
					contaminated = true
					break
				}
			}
			// A body whose first line equals — or starts with — the article
			// title carries a body-embedded duplicate headline (publisher
			// title/byline block inside the prose container; securityaffairs
			// row 209's h2 appends an editorial subtitle to the headline).
			// Re-extracting with the leading-title drop is the only way to
			// clean those rows, so flag them too. A false positive costs one
			// re-fetch and no change — updates happen only when the extracted
			// text differs.
			if !contaminated {
				firstLine := strings.TrimSpace(strings.SplitN(r.FullContent, "\n", 2)[0])
				lowerFirst, lowerTitle := strings.ToLower(firstLine), strings.ToLower(strings.TrimSpace(r.Title))
				if lowerFirst != "" && lowerTitle != "" && strings.HasPrefix(lowerFirst, lowerTitle) {
					contaminated = true
				}
			}
			if !contaminated {
				continue
			}
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// parseRepairArgs makes flags work wherever they appear: Go's flag package
// stops parsing at the first positional argument, so `repair <pattern>
// --dry-run` would otherwise ignore --dry-run and run for real. Flag-like
// tokens (and their values) are moved ahead of positional tokens.
func parseRepairArgs(fs *flag.FlagSet, args []string) error {
	flagSet := map[string]bool{
		"limit": true, "dry-run": true, "all": true,
	}
	flags, positional := make([]string, 0, len(args)), make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && strings.TrimLeft(a, "-") != "" {
			name := strings.TrimLeft(a, "-")
			if eq := strings.Index(name, "="); eq >= 0 {
				flags = append(flags, a)
				continue
			}
			flags = append(flags, a)
			if flagSet[name] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				// boolean flags are consumed without a value only when
				// declared bool; limit takes a value — pass it through.
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return fs.Parse(append(flags, positional...))
}

// runRepair re-fetches stored articles matching pattern, re-extracts them
// with the current extractMainContent, and updates full_content/content_html
// ONLY when the re-extracted text differs from what is stored. A changed row
// also gets summary=NULL: its summary was generated from the stale content,
// so the summarizer regenerates it at its own pace. Rows whose content is
// unchanged are left completely untouched — summaries survive, and reruns
// after a successful pass select nothing.
//
//	information-broker repair <pattern> [--limit N] [--dry-run] [--all]
//
// Default scope is contaminated rows only; --all widens to every match.
// --limit bounds the batch; --dry-run lists what would be re-fetched.
func runRepair(db *sql.DB, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "max rows to process in this run (0 = all selected)")
	dryRun := fs.Bool("dry-run", false, "list what would be re-fetched and exit")
	all := fs.Bool("all", false, "re-extract every matching row, not just contaminated ones")
	if err := parseRepairArgs(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: repair <pattern> [--limit N] [--dry-run] [--all]")
	}
	pattern := fs.Arg(0)

	rows, err := db.Query(`
		SELECT id, url, title, COALESCE(full_content, '')
		FROM articles
		WHERE url ILIKE '%' || $1 || '%'
		ORDER BY id`, pattern)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	var allRows []repairRow
	for rows.Next() {
		var r repairRow
		if err := rows.Scan(&r.ID, &r.URL, &r.Title, &r.FullContent); err != nil {
			rows.Close()
			return err
		}
		allRows = append(allRows, r)
	}
	rows.Close()

	candidates := selectRepairCandidates(allRows, *all, *limit)
	log.Printf("repair: %d articles matching %q, %d selected (contaminated-only=%v, limit=%d)",
		len(allRows), pattern, len(candidates), !*all, *limit)
	if *dryRun {
		for _, r := range candidates {
			log.Printf("  would re-extract id=%d %s", r.ID, r.URL)
		}
		return nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	maxLen := cfg.Performance.MaxArticleContentLength
	maxHTMLLen := cfg.Performance.MaxArticleHTMLLength
	updated, unchanged, failed := 0, 0, 0
	for i, r := range candidates {
		content, contentHTML, err := backfillFetch(client, cfg.API.UserAgent, r.URL, r.Title, maxLen, maxHTMLLen)
		if err != nil {
			log.Printf("  [%d/%d] id=%d FAIL: %v", i+1, len(candidates), r.ID, err)
			failed++
			time.Sleep(2 * time.Second)
			continue
		}
		if content == strings.TrimSpace(r.FullContent) {
			// Re-extraction produced identical text: the stored content is
			// already what the current extractor produces — leave the row
			// (and its summary) alone.
			unchanged++
			log.Printf("  [%d/%d] id=%d unchanged (fetched %d chars == stored %d)",
				i+1, len(candidates), r.ID, len(content), len(strings.TrimSpace(r.FullContent)))
			time.Sleep(1500 * time.Millisecond)
			continue
		}
		if _, err := db.Exec(`UPDATE articles SET full_content=$1, content_html=$2, summary=NULL, updated_at=NOW() WHERE id=$3`,
			sanitizeUTF8(content), contentHTML, r.ID); err != nil {
			log.Printf("  id=%d UPDATE FAIL: %v", r.ID, err)
			failed++
			continue
		}
		updated++
		log.Printf("  [%d/%d] id=%d updated (%d -> %d chars), summary queued for regeneration",
			i+1, len(candidates), r.ID, len(r.FullContent), len(content))
		time.Sleep(1500 * time.Millisecond) // politeness / rate limit
	}
	log.Printf("repair done: updated=%d unchanged=%d failed=%d (selected %d)",
		updated, unchanged, failed, len(candidates))
	return nil
}
