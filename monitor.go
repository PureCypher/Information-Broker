package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"information-broker/config"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/mmcdole/gofeed"
)

// Article represents a fetched article with all required information
type Article struct {
	Title         string        `json:"title"`
	URL           string        `json:"url"`
	PublishedAt   time.Time     `json:"published_at"`
	Content       string        `json:"content"`
	FetchDuration time.Duration `json:"fetch_duration"`
	FeedURL       string        `json:"feed_url"`
	ContentHash   string        `json:"content_hash"`
}

// RSSMonitor manages the monitoring of RSS feeds
type RSSMonitor struct {
	db              *sql.DB
	feeds           []string
	seenArticles    map[string]bool // URL -> bool for deduplication
	mutex           sync.RWMutex
	fetchInterval   time.Duration
	httpClient      *http.Client
	parser          *gofeed.Parser
	metrics         *PrometheusMetrics
	config          *config.Config
	circuitBreakers *CircuitBreakerManager
	scheduler       *SummarizationScheduler
}

// NewRSSMonitor creates a new RSS monitor instance
func NewRSSMonitor(db *sql.DB, feeds []string, metrics *PrometheusMetrics, cfg *config.Config, circuitBreakers *CircuitBreakerManager, scheduler *SummarizationScheduler) *RSSMonitor {
	return &RSSMonitor{
		db:            db,
		feeds:         feeds,
		seenArticles:  make(map[string]bool),
		fetchInterval: cfg.App.RSSFetchInterval,
		httpClient: &http.Client{
			Timeout: cfg.API.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 5,
				MaxConnsPerHost:     10,
				IdleConnTimeout:     90 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
		parser:          gofeed.NewParser(),
		metrics:         metrics,
		config:          cfg,
		circuitBreakers: circuitBreakers,
		scheduler:       scheduler,
	}
}

// Start begins monitoring RSS feeds
func (m *RSSMonitor) Start(ctx context.Context) {
	log.Println("Starting RSS monitor")

	// Load existing articles from database to populate seen articles
	if err := m.loadExistingArticles(); err != nil {
		log.Printf("Error loading existing articles: %v", err)
	}

	// Create a ticker for periodic checks
	ticker := time.NewTicker(m.fetchInterval)
	defer ticker.Stop()

	// Initial fetch
	m.fetchAllFeeds(ctx)

	// Periodic fetching
	for {
		select {
		case <-ctx.Done():
			log.Println("RSS monitor stopping...")
			return
		case <-ticker.C:
			m.fetchAllFeeds(ctx)
		}
	}
}

// loadExistingArticles populates the seen articles map from database
func (m *RSSMonitor) loadExistingArticles() error {
	log.Println("Loading existing articles from database...")

	rows, err := m.db.Query("SELECT url FROM articles")
	if err != nil {
		return err
	}
	defer rows.Close()

	// Build a local slice first to avoid holding the write lock during I/O
	var urls []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			log.Printf("Error scanning article URL: %v", err)
			continue
		}
		urls = append(urls, url)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Populate the map under lock in a tight loop — no I/O
	m.mutex.Lock()
	for _, url := range urls {
		m.seenArticles[url] = true
	}
	m.mutex.Unlock()

	log.Printf("Loaded %d existing articles for deduplication", len(urls))
	return nil
}

// fetchAllFeeds fetches all RSS feeds concurrently
func (m *RSSMonitor) fetchAllFeeds(ctx context.Context) {
	log.Printf("Fetching %d RSS feeds...", len(m.feeds))

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, m.config.Performance.MaxConcurrentFeeds) // Limit concurrent fetches

	for _, feedURL := range m.feeds {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
				m.fetchFeed(ctx, url)
			case <-ctx.Done():
				return
			}
		}(feedURL)
	}

	wg.Wait()
	log.Println("Completed fetching all feeds")
}

// fetchFeed fetches and processes a single RSS feed with circuit breaker protection
func (m *RSSMonitor) fetchFeed(ctx context.Context, feedURL string) {
	startTime := time.Now()

	log.Printf("Fetching feed: %s", feedURL)

	// Get or create circuit breaker for this feed
	cb := m.circuitBreakers.GetOrCreateBreaker("rss_feed_"+feedURL, &CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          time.Minute * 2,
		ResetTimeout:     time.Minute * 5,
	})

	// Execute feed fetch with circuit breaker protection
	err := cb.Execute(func() error {
		return m.doFetchFeed(ctx, feedURL, startTime)
	}, m.metrics)

	if err != nil {
		if err == ErrCircuitBreakerOpen {
			duration := time.Since(startTime)
			m.logFetch(feedURL, "error", "Circuit breaker is open", duration, 0, 0)
			m.metrics.RecordRSSFetch(feedURL, "circuit_breaker_open", duration)
			m.metrics.RecordRSSFetchError(feedURL, "circuit_breaker_open")
		}
		// Other errors are already handled in doFetchFeed
	}
}

// doFetchFeed performs the actual feed fetching logic
func (m *RSSMonitor) doFetchFeed(ctx context.Context, feedURL string, startTime time.Time) error {
	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		duration := time.Since(startTime)
		m.logFetch(feedURL, "error", fmt.Sprintf("Failed to create request: %v", err), duration, 0, 0)
		m.metrics.RecordRSSFetch(feedURL, "error", duration)
		m.metrics.RecordRSSFetchError(feedURL, "request_creation_failed")
		return err
	}

	// Set user agent
	req.Header.Set("User-Agent", m.config.API.UserAgent)

	// Fetch the feed
	resp, err := m.httpClient.Do(req)
	if err != nil {
		// Cloudflare and similar WAFs frequently block non-browser clients at the
		// transport layer (TLS handshake, HTTP/2 RST_STREAM, connection reset)
		// instead of returning 403, so the status-code check below never runs.
		// Retry through the challenge solver before giving up, unless we are
		// shutting down (context cancelled/expired), in which case a retry is
		// pointless.
		if m.config.FlareSolverr.URL != "" && ctx.Err() == nil {
			return m.fetchViaFlareSolverr(ctx, feedURL, startTime)
		}
		duration := time.Since(startTime)
		m.logFetch(feedURL, "error", fmt.Sprintf("Failed to fetch feed: %v", err), duration, 0, 0)
		m.metrics.RecordRSSFetch(feedURL, "error", duration)
		m.metrics.RecordRSSFetchError(feedURL, "http_request_failed")
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Cloudflare and similar WAFs answer with a challenge status (403, and
		// sometimes 429/503 or Cloudflare's 520-527 origin codes) plus a JS/TLS
		// challenge that a plain Go HTTP client cannot pass. If a challenge solver
		// is configured, retry the fetch through it before giving up.
		if m.config.FlareSolverr.URL != "" && isChallengeStatus(resp.StatusCode) {
			return m.fetchViaFlareSolverr(ctx, feedURL, startTime)
		}
		duration := time.Since(startTime)
		err := fmt.Errorf("HTTP %d", resp.StatusCode)
		m.logFetch(feedURL, "error", err.Error(), duration, 0, 0)
		m.metrics.RecordRSSFetch(feedURL, "error", duration)
		m.metrics.RecordRSSFetchError(feedURL, "http_error")
		return err
	}

	// Parse the feed
	feed, err := m.parser.Parse(resp.Body)
	if err != nil {
		duration := time.Since(startTime)
		m.logFetch(feedURL, "error", fmt.Sprintf("Failed to parse feed: %v", err), duration, 0, 0)
		m.metrics.RecordRSSFetch(feedURL, "error", duration)
		m.metrics.RecordRSSFetchError(feedURL, "parse_failed")
		return err
	}

	return m.processFeedItems(ctx, feedURL, feed, startTime)
}

// isChallengeStatus reports whether an HTTP status code likely indicates a
// WAF/CDN challenge (Cloudflare et al.) that a headless-browser solver such as
// FlareSolverr can bypass, as opposed to a genuine client/server error.
func isChallengeStatus(code int) bool {
	switch code {
	case http.StatusForbidden, // 403
		http.StatusTooManyRequests,    // 429
		http.StatusServiceUnavailable: // 503
		return true
	}
	// Cloudflare's non-standard origin-unreachable / challenge codes.
	return code >= 520 && code <= 527
}

// processFeedItems sorts and processes a parsed feed's items and records success
// metrics. It is shared by the direct fetch path and the FlareSolverr fallback.
func (m *RSSMonitor) processFeedItems(ctx context.Context, feedURL string, feed *gofeed.Feed, startTime time.Time) error {
	// Process articles
	newArticles := 0
	totalArticles := len(feed.Items)

	// Sort articles by publication date (oldest first) to maintain chronological order
	sortedItems := make([]*gofeed.Item, len(feed.Items))
	copy(sortedItems, feed.Items)

	sort.Slice(sortedItems, func(i, j int) bool {
		// Handle cases where PublishedParsed might be nil
		timeI := time.Time{}
		timeJ := time.Time{}

		if sortedItems[i].PublishedParsed != nil {
			timeI = *sortedItems[i].PublishedParsed
		}
		if sortedItems[j].PublishedParsed != nil {
			timeJ = *sortedItems[j].PublishedParsed
		}

		// If both times are zero (nil), maintain original order
		if timeI.IsZero() && timeJ.IsZero() {
			return i < j
		}
		// If one is zero, put the non-zero one first
		if timeI.IsZero() {
			return false
		}
		if timeJ.IsZero() {
			return true
		}

		// Both have valid times, sort chronologically (oldest first)
		return timeI.Before(timeJ)
	})

	for _, item := range sortedItems {
		if ctx.Err() != nil {
			return ctx.Err() // Context cancelled
		}

		if m.processArticle(item, feedURL) {
			newArticles++
		}
	}

	duration := time.Since(startTime)
	m.logFetch(feedURL, "success", "", duration, totalArticles, newArticles)

	// Record metrics
	m.metrics.RecordRSSFetch(feedURL, "success", duration)
	m.metrics.RecordNewArticles(feedURL, newArticles)

	if newArticles > 0 {
		log.Printf("Feed %s: Found %d new articles out of %d total", feedURL, newArticles, totalArticles)
	}

	return nil
}

// flareSolverrResponse models the subset of the FlareSolverr v1 API response we use.
type flareSolverrResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		Status   int    `json:"status"`
		Response string `json:"response"`
	} `json:"solution"`
}

// fetchViaFlareSolverr retries a blocked feed through a FlareSolverr instance,
// which uses a headless browser to pass Cloudflare/WAF challenges. The browser
// returns a rendered DOM, so the raw feed XML is extracted before parsing.
func (m *RSSMonitor) fetchViaFlareSolverr(ctx context.Context, feedURL string, startTime time.Time) error {
	log.Printf("Feed %s: HTTP 403, retrying via FlareSolverr", feedURL)

	payload, err := json.Marshal(map[string]interface{}{
		"cmd":        "request.get",
		"url":        feedURL,
		"maxTimeout": int(m.config.FlareSolverr.Timeout / time.Millisecond),
	})
	if err != nil {
		return m.flareError(feedURL, startTime, fmt.Sprintf("marshal request: %v", err))
	}

	// Allow the solver its full maxTimeout plus headroom for browser startup.
	reqCtx, cancel := context.WithTimeout(ctx, m.config.FlareSolverr.Timeout+30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", m.config.FlareSolverr.URL, bytes.NewReader(payload))
	if err != nil {
		return m.flareError(feedURL, startTime, fmt.Sprintf("create request: %v", err))
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: m.config.FlareSolverr.Timeout + 30*time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return m.flareError(feedURL, startTime, fmt.Sprintf("request failed: %v", err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return m.flareError(feedURL, startTime, fmt.Sprintf("read response: %v", err))
	}

	var fsResp flareSolverrResponse
	if err := json.Unmarshal(raw, &fsResp); err != nil {
		return m.flareError(feedURL, startTime, fmt.Sprintf("decode response: %v", err))
	}
	if fsResp.Status != "ok" {
		return m.flareError(feedURL, startTime, fmt.Sprintf("solver status %q: %s", fsResp.Status, fsResp.Message))
	}
	if fsResp.Solution.Status != http.StatusOK {
		return m.flareError(feedURL, startTime, fmt.Sprintf("solved with HTTP %d", fsResp.Solution.Status))
	}

	feed, err := m.parser.ParseString(extractFeedXML(fsResp.Solution.Response))
	if err != nil {
		return m.flareError(feedURL, startTime, fmt.Sprintf("parse solved feed: %v", err))
	}

	log.Printf("Feed %s: solved via FlareSolverr (%d items)", feedURL, len(feed.Items))
	return m.processFeedItems(ctx, feedURL, feed, startTime)
}

// flareError centralises error logging and metrics for the FlareSolverr path.
func (m *RSSMonitor) flareError(feedURL string, startTime time.Time, msg string) error {
	full := "FlareSolverr: " + msg
	duration := time.Since(startTime)
	m.logFetch(feedURL, "error", full, duration, 0, 0)
	m.metrics.RecordRSSFetch(feedURL, "error", duration)
	m.metrics.RecordRSSFetchError(feedURL, "flaresolverr_failed")
	return fmt.Errorf("%s", full)
}

// extractFeedXML pulls raw feed XML out of the HTML document returned by
// FlareSolverr's headless browser. Chrome renders raw XML inside a <pre>
// element with the markup HTML-escaped (&lt;rss&gt;...), so the entities must
// be decoded before the feed can be parsed.
func extractFeedXML(s string) string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "<?xml") || strings.HasPrefix(trimmed, "<rss") || strings.HasPrefix(trimmed, "<feed") {
		return trimmed
	}

	if doc, derr := goquery.NewDocumentFromReader(strings.NewReader(s)); derr == nil {
		// Older Chrome kept an unrendered copy of the source here.
		if sel := doc.Find("#webkit-xml-viewer-source-xml"); sel.Length() > 0 {
			if inner, herr := sel.Html(); herr == nil && strings.Contains(inner, "<") {
				return strings.TrimSpace(inner)
			}
		}
		// Modern Chrome shows the escaped source in a <pre>; Text() decodes it.
		if pre := doc.Find("pre"); pre.Length() > 0 {
			if text := strings.TrimSpace(pre.First().Text()); text != "" {
				return text
			}
		}
		// Last resort: the decoded text of the whole body.
		if body := strings.TrimSpace(doc.Find("body").Text()); body != "" {
			return body
		}
	}

	// Fallback: slice from the first feed root element to its matching close tag.
	for _, tag := range [][2]string{{"<rss", "</rss>"}, {"<feed", "</feed>"}} {
		if start := strings.Index(s, tag[0]); start != -1 {
			if end := strings.LastIndex(s, tag[1]); end != -1 && end > start {
				return s[start : end+len(tag[1])]
			}
		}
	}
	return trimmed
}

// processArticle processes a single article from an RSS feed
func (m *RSSMonitor) processArticle(item *gofeed.Item, feedURL string) bool {
	if item.Link == "" {
		m.metrics.RecordArticleProcessed(feedURL, "skipped_no_link")
		return false
	}

	// Parse and normalize the publish date to UTC
	var publishDate time.Time
	if item.PublishedParsed != nil {
		publishDate = item.PublishedParsed.UTC()
	} else {
		// If no publish date is available, skip the article as per requirements
		log.Printf("Skipping article with missing publish date: %s", item.Title)
		m.metrics.RecordArticleProcessed(feedURL, "skipped_no_publish_date")
		return false
	}

	// Check publication date against the cutoff date — skip silently (metrics track these)
	cutoffDate := m.config.App.ArticleCutoffDate.UTC()
	if publishDate.Before(cutoffDate) {
		m.metrics.RecordArticleFilteredPreCutoff(feedURL)
		m.metrics.RecordArticleProcessed(feedURL, "skipped_before_cutoff")
		return false
	}

	// Check publication date against initiation date
	if publishDate.Before(m.config.App.InitiationDate) {
		m.metrics.RecordArticleProcessed(feedURL, "skipped_before_initiation")
		return false
	}

	// Article passed the cutoff date filter
	m.metrics.RecordArticleProcessedPostCutoff(feedURL)

	// Check-and-set under write lock to prevent concurrent goroutines
	// from processing the same URL simultaneously
	m.mutex.Lock()
	if m.seenArticles[item.Link] {
		m.mutex.Unlock()
		m.metrics.RecordArticleProcessed(feedURL, "skipped_duplicate")
		return false // Already processed
	}
	// Mark as seen immediately to prevent duplicate processing by concurrent goroutines
	m.seenArticles[item.Link] = true
	m.mutex.Unlock()

	// Fetch full content with context for graceful shutdown
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), m.config.API.Timeout)
	defer fetchCancel()
	startTime := time.Now()
	content, err := m.fetchFullContent(fetchCtx, item.Link)
	fetchDuration := time.Since(startTime)

	if err != nil {
		log.Printf("Failed to fetch content for %s: %v", item.Link, err)
		content = item.Description // Fallback to description
	}

	// Create article struct
	article := Article{
		Title:         item.Title,
		URL:           item.Link,
		Content:       content,
		FetchDuration: fetchDuration,
		FeedURL:       feedURL,
	}

	// Set published time (we already validated it exists above)
	article.PublishedAt = publishDate

	// Generate content hash for deduplication
	article.ContentHash = m.generateContentHash(article.Title, article.URL, article.Content)

	// Save to database
	if err := m.saveArticle(article); err != nil {
		log.Printf("Failed to save article %s: %v", article.URL, err)
		m.metrics.RecordArticleProcessed(feedURL, "save_failed")
		m.metrics.RecordArticleProcessedTotal("failed")
		// Unmark on failure so it can be retried next cycle
		m.mutex.Lock()
		delete(m.seenArticles, item.Link)
		m.mutex.Unlock()
		return false
	}

	// Record successful processing
	m.metrics.RecordArticleProcessed(feedURL, "processed")
	m.metrics.RecordArticleProcessedTotal("success")

	log.Printf("New article saved: %s", article.Title)

	// Try to generate summary for the new article
	go m.generateSummaryAsync(article)

	return true
}

// extractMainContent picks the best-matching element's text from a page.
// Pages that include "related posts"/"latest articles" widgets often have
// several elements matching a content-area selector (e.g. multiple <article>
// teaser cards) before — or instead of — the actual post body, which may
// itself match a different, later selector (e.g. a bare .entry-content div
// with no <article> wrapper at all). Stopping at the first selector with any
// non-empty match can silently grab an unrelated teaser. Instead, this
// compares every match across the whole tier of specific content selectors
// and keeps the single longest, since a real article body is virtually
// always far longer than a related-post teaser card; only if nothing in
// that tier matched does it fall back to the broader "main" landmark, then
// finally the whole "body". .page-content/.main-content cover Bootstrap
// admin-dashboard-style themes (found live on cvefeed.io, the largest single
// feed in the corpus) that have no <article>/.entry-content wrapper and no
// <main> tag at all — without a matching selector, the body fallback pulled
// in header/nav chrome (a search-box placeholder, a "Pricing" nav link)
// ahead of the real per-page content.
func extractMainContent(doc *goquery.Document) string {
	// goquery's .Text() returns the raw source text of <script>/<style>
	// elements too (they're unrendered but still DOM text nodes) — strip
	// them first so CSS rules or JS don't leak into the stored article text.
	doc.Find("script, style").Remove()

	// Strip known non-article widgets before measuring/using any selector's
	// text — e.g. "#ar-widget" is a common "Listen to this article"
	// audio-player plugin embedded inside the real content container, whose
	// own playback/voice-selector controls would otherwise be measured (and
	// stored) as if they were article prose.
	doc.Find("#ar-widget").Remove()

	// theregister.com labels every ad slot with <span class="ad-label">REG AD</span>;
	// strip them so ad-slot labels don't pollute the extracted article text.
	doc.Find(".ad-label").Remove()

	// Precise, high-confidence article-body selectors. Within this tier the
	// longest match wins (a real post body dwarfs a related-post teaser card;
	// this is the hackread.com fix). ".k5a-article" is theregister.com's
	// <section class="... k5a-article"> article body.
	preciseSelectors := []string{
		"article",
		".post-content",
		".entry-content",
		".article-body",
		".post-body",
		".k5a-article",
	}
	// Broad page wrappers, used ONLY when no precise selector matched — some
	// sites (Bootstrap admin themes like cvefeed.io) have no article/
	// .entry-content wrapper at all. These must never override a precise match:
	// on theregister.com .content/.page-content span the whole page (nav, ads,
	// "more from" grids) and are far longer than the real story, so treating
	// them as equals to precise selectors picked chrome over the article.
	fallbackSelectors := []string{
		".content",
		".page-content",
		".main-content",
	}

	longest := func(selectors []string) string {
		var content string
		for _, selector := range selectors {
			doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
				if text := s.Text(); len(text) > len(content) {
					content = text
				}
			})
		}
		return content
	}

	content := longest(preciseSelectors)
	if content == "" {
		content = longest(fallbackSelectors)
	}
	if content == "" {
		content = doc.Find("main").First().Text()
	}
	if content == "" {
		content = doc.Find("body").Text()
	}
	return content
}

// fetchFullContent attempts to fetch the full content of an article
func (m *RSSMonitor) fetchFullContent(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", m.config.API.UserAgent)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Parse HTML and extract text content
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	content := strings.TrimSpace(extractMainContent(doc))
	if len(content) > m.config.Performance.MaxArticleContentLength { // Limit content length
		// Truncate on a rune boundary; byte-slicing can split a multi-byte
		// character and leave invalid UTF-8 that PostgreSQL rejects on save.
		content = safeTruncate(content, m.config.Performance.MaxArticleContentLength) + "..."
	}

	return content, nil
}

// generateContentHash creates a unique hash for content deduplication
func (m *RSSMonitor) generateContentHash(title, url, content string) string {
	hasher := sha256.New()
	hasher.Write([]byte(title))
	hasher.Write([]byte(url))
	hasher.Write([]byte(content))
	return hex.EncodeToString(hasher.Sum(nil))
}

// saveArticle saves an article to the database
func (m *RSSMonitor) saveArticle(article Article) error {
	query := `
		INSERT INTO articles (title, url, full_content, publish_date, fetch_duration_ms, feed_url, content_hash, fetch_time, posted_to_discord)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), FALSE)
		ON CONFLICT (url) DO NOTHING`

	// Strip any invalid UTF-8 before insert: a single bad byte makes PostgreSQL
	// reject the whole row ("invalid byte sequence for encoding UTF8"), silently
	// dropping the article. Covers both truncation- and source-induced bad bytes.
	_, err := m.db.Exec(query,
		sanitizeUTF8(article.Title),
		sanitizeUTF8(article.URL),
		sanitizeUTF8(article.Content),
		article.PublishedAt,
		article.FetchDuration.Milliseconds(),
		sanitizeUTF8(article.FeedURL),
		article.ContentHash,
	)

	return err
}

// logFetch logs fetch operations to database and stdout
func (m *RSSMonitor) logFetch(feedURL, status, message string, duration time.Duration, articlesFound, newArticles int) {
	// Log to stdout
	logMsg := fmt.Sprintf("Feed: %s | Status: %s | Duration: %v | Articles: %d | New: %d",
		feedURL, status, duration, articlesFound, newArticles)

	if message != "" {
		logMsg += fmt.Sprintf(" | Message: %s", message)
	}

	log.Println(logMsg)

	// Log to database
	query := `
		INSERT INTO fetch_logs (feed_url, status, message, duration_ms, articles_found, new_articles)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := m.db.Exec(query, feedURL, status, message, duration.Milliseconds(), articlesFound, newArticles)
	if err != nil {
		log.Printf("Failed to log fetch to database: %v", err)
	}
}

// generateSummaryAsync generates a summary for an article by enqueuing it to the scheduler
func (m *RSSMonitor) generateSummaryAsync(article Article) {
	// Check if article has content worth summarizing
	if strings.TrimSpace(article.Content) == "" {
		log.Printf("Skipping summarization for article %s: no content", article.URL)
		return
	}

	// Create summarization request
	request := SummarizationRequest{
		ArticleURL:   article.URL,
		ArticleTitle: article.Title,
		Content:      article.Content,
		Model:        m.config.OLLAMA.Model,
		Priority:     1, // Normal priority for RSS articles
		EnqueuedAt:   time.Now(),
		ResponseChan: nil, // No response channel needed for async processing
	}

	// Enqueue to the centralized scheduler
	if err := m.scheduler.EnqueueSummarization(request); err != nil {
		log.Printf("Failed to enqueue summarization for article %s: %v", article.URL, err)

		// Fallback: save a placeholder summary to the database
		if err := m.updateArticleSummary(article.URL, "summary unavailable"); err != nil {
			log.Printf("Failed to save fallback summary for article %s: %v", article.URL, err)
		}
	} else {
		log.Printf("Successfully enqueued summarization for article: %s", article.Title)
	}
}

// updateArticleSummary updates the summary field for an article in the database
func (m *RSSMonitor) updateArticleSummary(articleURL, summary string) error {
	query := `UPDATE articles SET summary = $1, updated_at = NOW() WHERE url = $2`
	_, err := m.db.Exec(query, summary, articleURL)
	return err
}
