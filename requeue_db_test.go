package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// TestRequeueCatchupLoadersContract verifies the catch-up loader predicates
// against a real database: the three requeue modes must stay disjoint, and
// --failed-summaries must rescue stranded rows (failed summaries on unposted
// articles — the live monitor never retries them) while leaving sub-floor
// bodies alone. Skipped unless DB_HOST/DB_PORT/DB_NAME/DB_USER/DB_PASSWORD
// point at a disposable database, e.g. the local preview container.
func TestRequeueCatchupLoadersContract(t *testing.T) {
	host := os.Getenv("DB_HOST")
	if host == "" {
		t.Skip("DB_HOST not set; catch-up loader contract needs a disposable database")
	}
	dsn := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=disable",
		host, getenvDefault("DB_PORT", "5432"), getenvDefault("DB_NAME", "information_broker"),
		getenvDefault("DB_USER", "postgres"), os.Getenv("DB_PASSWORD"))
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("database unreachable: %v", err)
	}

	const prefix = "https://requeue-contract-test.invalid/"
	cleanup := func() {
		db.Exec(`DELETE FROM articles WHERE url LIKE $1`, prefix+"%")
	}
	cleanup()
	defer cleanup()

	seed := func(t *testing.T, suffix, summary string, posted bool, contentLen int, withPublishDate bool) string {
		url := prefix + suffix
		var content sql.NullString
		if contentLen > 0 {
			content = sql.NullString{String: strings.Repeat("word ", contentLen/5), Valid: true}
		}
		var pub sql.NullTime
		if withPublishDate {
			pub = sql.NullTime{Time: time.Now().AddDate(0, 0, -2), Valid: true}
		}
		var summ sql.NullString
		if summary != "" {
			summ = sql.NullString{String: summary, Valid: true}
		}
		_, err := db.Exec(`INSERT INTO articles (url, title, full_content, summary, posted_to_discord, fetch_time, feed_url, publish_date)
			VALUES ($1, $2, $3, $4, $5, now(), $6, $7)`,
			url, suffix, content, summ, posted, "https://requeue-contract-test.invalid/feed", pub)
		if err != nil {
			t.Fatalf("seed %s: %v", suffix, err)
		}
		return url
	}

	posteds := seed(t, "failed-posted", "summary unavailable", true, 500, true)
	unposteds := seed(t, "failed-unposted", "summary unavailable", false, 500, true)
	tiny := seed(t, "failed-tiny", "summary unavailable", false, 40, true)
	missing := seed(t, "missing-posted", "", true, 500, true)

	failed, err := loadFailedSummaryArticles(db)
	if err != nil {
		t.Fatalf("loadFailedSummaryArticles: %v", err)
	}
	got := map[string]bool{}
	for _, a := range failed {
		got[a.URL] = true
	}
	if !got[posteds] || !got[unposteds] {
		t.Errorf("--failed-summaries must rescue both posted and unposted failed rows, got %v", got)
	}
	if got[tiny] {
		t.Errorf("--failed-summaries must skip sub-floor bodies, but selected %s", tiny)
	}
	if got[missing] {
		t.Errorf("--failed-summaries must stay disjoint from NULL-summary rows, but selected %s", missing)
	}

	missingRows, err := loadMissingSummaryArticles(db)
	if err != nil {
		t.Fatalf("loadMissingSummaryArticles: %v", err)
	}
	for _, a := range missingRows {
		if a.URL == posteds || a.URL == unposteds || a.URL == tiny {
			t.Errorf("--missing-summary must stay disjoint from failed rows, selected %s", a.URL)
		}
		if a.URL != missing {
			t.Errorf("--missing-summary selected unexpected row %s", a.URL)
		}
	}

	// The regular path takes unposted rows created within the window, whatever
	// their summary state; it must never select posted rows.
	regular, err := loadRequeueArticles(db, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("loadRequeueArticles: %v", err)
	}
	for _, a := range regular {
		if a.URL == posteds || a.URL == missing {
			t.Errorf("regular path selected posted row %s", a.URL)
		}
	}
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
