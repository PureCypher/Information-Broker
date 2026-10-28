package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"information-broker/config"
)

var (
	silentTestMetricsOnce sync.Once
	silentTestMetrics     *PrometheusMetrics
)

// NewPrometheusMetrics registers on the global registry, so build it once.
func sharedTestMetrics() *PrometheusMetrics {
	silentTestMetricsOnce.Do(func() { silentTestMetrics = NewPrometheusMetrics() })
	return silentTestMetrics
}

// Integration test for the Discord silent-onboarding window, through the real
// processArticle. It needs a scratch Postgres with the articles schema and is
// skipped unless SILENT_ONBOARDING_TEST_DSN is set. The scheduler is a stub with
// a queue and no worker, so nothing can ever be summarized or posted.
func TestProcessArticleSilentOnboardingMarksPosted(t *testing.T) {
	dsn := os.Getenv("SILENT_ONBOARDING_TEST_DSN")
	if dsn == "" {
		t.Skip("set SILENT_ONBOARDING_TEST_DSN to run against a scratch Postgres")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() }) // registered first, so it runs after the row cleanup below

	page := `<html><body><article><p>` + strings.Repeat("Real article prose for the silent onboarding test. ", 20) + `</p></article></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM articles WHERE url LIKE $1`, srv.URL+"%"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	cfg := config.Load()
	cfg.Discord.SilentFeeds = []string{"silent-onboarding-test-feed"}
	metrics := sharedTestMetrics()
	m := &RSSMonitor{
		db:           db,
		seenArticles: make(map[string]bool),
		config:       cfg,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		metrics:      metrics,
		scheduler:    &SummarizationScheduler{queue: make(chan SummarizationRequest, 10), metrics: metrics, config: cfg},
	}

	matching := srv.URL + "/silent-onboarding-test-feed.xml"
	other := srv.URL + "/some-other-feed.xml"
	published := time.Now().Add(-time.Hour)
	cases := []struct {
		name   string
		feed   string
		until  time.Time
		slug   string
		wantTo bool // expected posted_to_discord
	}{
		{"matching feed inside the window is stored as posted", matching, time.Now().Add(time.Hour), "/a1", true},
		{"matching feed after the window posts normally", matching, time.Now().Add(-time.Hour), "/a2", false},
		{"other feed inside the window posts normally", other, time.Now().Add(time.Hour), "/a3", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg.Discord.SilentUntil = c.until
			item := &gofeed.Item{Title: "Silent onboarding " + c.slug, Link: srv.URL + c.slug, PublishedParsed: &published}
			if !m.processArticle(item, c.feed) {
				t.Fatalf("processArticle did not store %s", item.Link)
			}
			var posted bool
			if err := db.QueryRow(`SELECT posted_to_discord FROM articles WHERE url = $1`, item.Link).Scan(&posted); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if posted != c.wantTo {
				t.Errorf("posted_to_discord = %v, want %v", posted, c.wantTo)
			}
		})
	}
}
