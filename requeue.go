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

// runRequeue is a one-off maintenance command:
//
//	information-broker requeue [--since 48h] [--dry-run] [--skip-post]
//
// It feeds stored-but-unposted articles back through the normal summarization worker, so Discord
// posting, rate pacing and every filter behave exactly as for a freshly fetched article. Use it to
// catch up after an outage of the summarizer (e.g. Ollama unreachable).
//
// With --skip-post nothing is sent to Discord: the webhooks are removed from the run's config,
// and each article is marked posted once its summary is stored, so it is never picked up again.
func runRequeue(db *sql.DB, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("requeue", flag.ContinueOnError)
	window := fs.Duration("since", 48*time.Hour, "only articles stored within this window")
	dryRun := fs.Bool("dry-run", false, "list what would be requeued and exit")
	skipPost := fs.Bool("skip-post", false, "summarize and mark as posted without sending to Discord")
	if err := fs.Parse(args); err != nil {
		return err
	}

	stored, err := loadRequeueArticles(db, time.Now().Add(-*window))
	if err != nil {
		return err
	}
	candidates := selectRequeueCandidates(stored, cfg.Discord.IsFeedExcluded, cfg.App.ArticleCutoffDate)
	log.Printf("requeue: %d unposted articles in the last %v, %d eligible", len(stored), *window, len(candidates))
	if *dryRun || len(candidates) == 0 {
		return nil
	}

	runCfg := *cfg
	if *skipPost {
		runCfg.Discord = withoutWebhooks(cfg.Discord)
		log.Printf("requeue: --skip-post set, nothing will be sent to Discord")
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
		if *skipPost {
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
	if !*skipPost {
		time.Sleep(cfg.Discord.Timeout + requeueDiscordSettling)
	}
	log.Printf("requeue done: summarized=%d failed=%d (total %d)", succeeded, failed, len(candidates))
	return nil
}
