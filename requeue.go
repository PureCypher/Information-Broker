package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"information-broker/config"
)

const (
	requeueSyncTimeout     = 3 * time.Minute
	requeueMaxConsecutive  = 5
	requeueDiscordSettling = 5 * time.Second
)

type requeueArticle struct {
	URL         string
	Title       string
	Content     string
	FeedURL     string
	PublishDate time.Time
}

// selectRequeueCandidates keeps the articles the worker would actually post: the same gates as
// sendDiscordNotification (excluded feeds, publish-date cutoff), plus non-empty content, which
// is what generateSummaryAsync requires before enqueueing. Input order is preserved.
func selectRequeueCandidates(in []requeueArticle, isExcluded func(string) bool, cutoff time.Time) []requeueArticle {
	out := make([]requeueArticle, 0, len(in))
	for _, a := range in {
		if strings.TrimSpace(a.Content) == "" || a.PublishDate.IsZero() {
			continue
		}
		if isExcluded(a.FeedURL) || a.PublishDate.UTC().Before(cutoff.UTC()) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// withoutWebhooks returns a copy of the Discord settings with every webhook removed, so the
// summarization worker has nothing to post to. The input is left untouched.
func withoutWebhooks(d config.DiscordConfig) config.DiscordConfig {
	d.WebhookURL = ""
	d.WebhookURLs = nil
	return d
}

// markPosted records an article as posted without sending anything, so later runs skip it.
func markPosted(db *sql.DB, url string) error {
	_, err := db.Exec(`UPDATE articles SET posted_to_discord = TRUE, updated_at = NOW() WHERE url = $1`, url)
	return err
}

func loadRequeueArticles(db *sql.DB, since time.Time) ([]requeueArticle, error) {
	rows, err := db.Query(`
		SELECT url, title, COALESCE(full_content, ''), COALESCE(feed_url, ''), publish_date
		FROM articles
		WHERE created_at >= $1 AND NOT posted_to_discord AND publish_date IS NOT NULL
		ORDER BY created_at ASC`, since)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	var articles []requeueArticle
	for rows.Next() {
		var a requeueArticle
		if err := rows.Scan(&a.URL, &a.Title, &a.Content, &a.FeedURL, &a.PublishDate); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// loadMissingSummaryArticles selects already-posted articles whose summary is
// NULL: exactly the rows the 'repair' command reset after re-extracting stale
// content. Keeping the posted filter here preserves the original contract; the
// two modes stay disjoint by the summary predicates alone (NULL vs the literal
// failure marker), which is also what keeps the regular path (pending,
// unposted rows) from double-summarizing the same article.
func loadMissingSummaryArticles(db *sql.DB) ([]requeueArticle, error) {
	return loadCatchupArticles(db, "summary IS NULL AND posted_to_discord")
}

// failedSummaryMinContent is the smallest article body worth summarizing.
// Machine-generated CVE stubs store 40-100 character descriptions; condensing
// those would just restate the title, and since summarization of them keeps
// failing they would be re-selected by every run. Rows below the floor stay in
// the honest 'summary unavailable' state.
const failedSummaryMinContent = 300

// loadFailedSummaryArticles selects articles whose summary is the literal
// 'summary unavailable' marker the scheduler writes on failure: rows that
// failed during an outage (e.g. Ollama unreachable) but are summarizable now.
// Posted state is intentionally NOT filtered: rows that failed before the
// Discord post step are stranded — the live monitor never retries failed rows,
// so without this catch-up they never get a summary. Disjoint from both other
// modes by the summary predicate; see failedSummaryMinContent for the floor.
func loadFailedSummaryArticles(db *sql.DB) ([]requeueArticle, error) {
	return loadCatchupArticles(db, fmt.Sprintf(
		"summary = 'summary unavailable' AND length(COALESCE(full_content, '')) >= %d", failedSummaryMinContent))
}

// loadCatchupArticles is the shared catch-up query: content-bearing,
// publish-dated rows matching the given summary predicate, newest repair
// activity first (updated_at DESC — rows a repair just re-extracted regenerate
// first). Posted-state filtering belongs in the caller's predicate.
func loadCatchupArticles(db *sql.DB, summaryPredicate string) ([]requeueArticle, error) {
	query := `
		SELECT url, title, COALESCE(full_content, ''), COALESCE(feed_url, ''), publish_date
		FROM articles
		WHERE ` + summaryPredicate + ` AND COALESCE(full_content, '') <> '' AND publish_date IS NOT NULL
		ORDER BY updated_at DESC`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	var articles []requeueArticle
	for rows.Next() {
		var a requeueArticle
		if err := rows.Scan(&a.URL, &a.Title, &a.Content, &a.FeedURL, &a.PublishDate); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// capCandidates bounds a requeue batch: limit>0 keeps at most the first N
// candidates (selection order already encodes priority — newest-repaired for
// --missing-summary, oldest-stored for the regular path); 0 keeps everything.
func capCandidates(in []requeueArticle, limit int) []requeueArticle {
	if limit <= 0 || len(in) <= limit {
		return in
	}
	return in[:limit]
}

// runRequeue is a one-off maintenance command:
//
//	information-broker requeue [--since 48h] [--dry-run] [--skip-post]
//	           [--missing-summary] [--limit N]
//
// It feeds stored-but-unposted articles back through the normal summarization worker, so Discord
// posting, rate pacing and every filter behave exactly as for a freshly fetched article. Use it to
// catch up after an outage of the summarizer (e.g. Ollama unreachable).
//
// With --skip-post nothing is sent to Discord: the webhooks are removed from the run's config,
// and each article is marked posted once its summary is stored, so it is never picked up again.
//
// --missing-summary selects a disjoint set: already-posted articles whose summary is NULL — the
// rows 'repair' reset after re-extracting stale content. It never touches Discord (webhooks are
// stripped) and never re-marks posted; a regenerated summary alone clears the row from future
// selections.
//
// --failed-summaries rescues articles whose summary is the 'summary unavailable' failure marker,
// regardless of posted state: rows that failed before the Discord post step are stranded (the live
// monitor never retries failed rows). It never sends to Discord (webhooks are stripped) and marks
// each rescued row posted, so a later default-mode catch-up can't post months-old news. Summaries
// need real article text, so bodies under failedSummaryMinContent chars are left failed. All three
// modes stay disjoint by summary predicate. --limit bounds the batch in any mode.
func runRequeue(db *sql.DB, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("requeue", flag.ContinueOnError)
	window := fs.Duration("since", 48*time.Hour, "only articles stored within this window")
	dryRun := fs.Bool("dry-run", false, "list what would be requeued and exit")
	skipPost := fs.Bool("skip-post", false, "summarize and mark as posted without sending to Discord")
	missingSummary := fs.Bool("missing-summary", false, "requeue already-posted articles whose summary is NULL (e.g. after 'repair' reset it); never posts to Discord")
	failedSummaries := fs.Bool("failed-summaries", false, "requeue articles whose summary is the 'summary unavailable' failure marker (e.g. after an outage); never sends to Discord, marks rescued rows posted")
	limit := fs.Int("limit", 0, "max articles to requeue in this run (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var candidates []requeueArticle
	switch {
	case *missingSummary:
		stored, err := loadMissingSummaryArticles(db)
		if err != nil {
			return err
		}
		candidates = capCandidates(selectRequeueCandidates(stored, cfg.Discord.IsFeedExcluded, cfg.App.ArticleCutoffDate), *limit)
		log.Printf("requeue: %d posted articles with summary NULL, %d eligible", len(stored), len(candidates))
	case *failedSummaries:
		stored, err := loadFailedSummaryArticles(db)
		if err != nil {
			return err
		}
		candidates = capCandidates(selectRequeueCandidates(stored, cfg.Discord.IsFeedExcluded, cfg.App.ArticleCutoffDate), *limit)
		log.Printf("requeue: %d articles with 'summary unavailable' and %d+ chars, %d eligible", len(stored), failedSummaryMinContent, len(candidates))
	default:
		stored, err := loadRequeueArticles(db, time.Now().Add(-*window))
		if err != nil {
			return err
		}
		candidates = capCandidates(selectRequeueCandidates(stored, cfg.Discord.IsFeedExcluded, cfg.App.ArticleCutoffDate), *limit)
		log.Printf("requeue: %d unposted articles in the last %v, %d eligible", len(stored), *window, len(candidates))
	}
	if *dryRun {
		for _, a := range candidates {
			log.Printf("  would requeue %s %s", a.URL, a.Title)
		}
		return nil
	}
	if len(candidates) == 0 {
		return nil
	}

	runCfg := *cfg
	if *skipPost || *missingSummary || *failedSummaries {
		runCfg.Discord = withoutWebhooks(cfg.Discord)
		if *missingSummary {
			log.Printf("requeue: --missing-summary set, nothing will be sent to Discord")
		} else if *failedSummaries {
			log.Printf("requeue: --failed-summaries set, nothing will be sent to Discord")
		} else {
			log.Printf("requeue: --skip-post set, nothing will be sent to Discord")
		}
	}
	scheduler := NewSummarizationScheduler(db, &runCfg, NewPrometheusMetrics())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer scheduler.Stop()

	succeeded, failed, consecutive := 0, 0, 0
	for i, a := range candidates {
		request := SummarizationRequest{
			ArticleURL:   a.URL,
			ArticleTitle: a.Title,
			Content:      a.Content,
			Model:        cfg.OLLAMA.Model,
			Priority:     1,
		}
		resp, err := scheduler.EnqueueSummarizationSync(request, requeueSyncTimeout)
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		if err != nil {
			failed++
			consecutive++
			log.Printf("requeue: [%d/%d] FAIL %q: %v", i+1, len(candidates), a.Title, err)
			if consecutive >= requeueMaxConsecutive {
				return fmt.Errorf("aborting after %d consecutive failures (is Ollama reachable?)", consecutive)
			}
			continue
		}
		if *skipPost || *failedSummaries {
			// --failed-summaries never sends to Discord, but unposted rows it
			// rescues must still leave the stranded pool: left unposted, a
			// later default-mode catch-up run would pick them up and post
			// months-old news. markPosted records them as settled without
			// sending anything.
			if err := markPosted(db, a.URL); err != nil {
				return fmt.Errorf("mark %q posted: %w", a.Title, err)
			}
		}
		succeeded++
		consecutive = 0
		if succeeded%10 == 0 {
			log.Printf("requeue: progress %d/%d (ok=%d failed=%d)", i+1, len(candidates), succeeded, failed)
		}
	}

	// The worker posts to Discord in a goroutine after returning the summary; let the last ones finish.
	// Catch-up modes never post, so there is nothing to settle.
	if !(*skipPost || *missingSummary || *failedSummaries) {
		time.Sleep(cfg.Discord.Timeout + requeueDiscordSettling)
	}
	log.Printf("requeue done: summarized=%d failed=%d (total %d)", succeeded, failed, len(candidates))
	return nil
}
