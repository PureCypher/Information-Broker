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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Article represents a fetched article with all required information
type Article struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
	Content     string    `json:"content"`
	// ContentHTML is the extracted article body with its original structure
	// (headings, lists, images) when the source provides HTML. Empty for
	// plain-text sources. Not used for hashing, search, or summarisation —
	// those consume Content.
	ContentHTML   string        `json:"content_html,omitempty"`
	FetchDuration time.Duration `json:"fetch_duration"`
	FeedURL       string        `json:"feed_url"`
	ContentHash   string        `json:"content_hash"`
}

// looksLikeHTML reports whether s carries markup rather than plain text.
// Only opens on tags the frontend whitelist actually renders, so code
// snippets quoting "<p>" in prose (rare but real) don't flip a text body
// into HTML rendering.
var htmlTagRe = regexp.MustCompile(`(?i)<(p|div|section|article|figure|figcaption|img|h[1-6]|ul|ol|li|table|blockquote|pre|strong|b|em|i|br)[ >/]`)

func looksLikeHTML(s string) bool {
	return htmlTagRe.MatchString(s)
}

// stripTags removes markup from s, keeping only text content (entities are
// decoded upstream by gofeed). Used to derive the plain-text form of
// HTML-bearing feed descriptions. The raw-text contents of script/style
// elements are dropped, not kept.
func stripTags(s string) string {
	tok := html.NewTokenizer(strings.NewReader(s))
	var out strings.Builder
	// Depth counter for elements whose text content is never displayed.
	skipDepth := 0
	for {
		tt := tok.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken:
			if name, _ := tok.TagName(); rawTextElements[string(name)] {
				skipDepth++
			}
		case html.EndTagToken:
			if name, _ := tok.TagName(); rawTextElements[string(name)] && skipDepth > 0 {
				skipDepth--
			}
		case html.TextToken:
			if skipDepth == 0 {
				out.Write(tok.Text())
			}
		}
	}
	return strings.TrimSpace(out.String())
}

// rawTextElements: text inside these is data (CSS/JS), never article prose.
var rawTextElements = map[string]bool{"script": true, "style": true, "title": true, "textarea": true}

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
		// Some feeds (e.g. Dark Reading event promos) stamp the future event date
		// as the pubDate. That would let ads outrank real news on a publish_date
		// sort, so anything more than 24h in the future is treated as published now.
		if clamped := clampFuturePublishDate(publishDate, time.Now().UTC()); clamped != publishDate {
			log.Printf("Clamping future-dated publish (%s) to now for: %s", publishDate.Format(time.RFC3339), item.Title)
			publishDate = clamped
		}
	} else {
		// If no publish date is available, skip the article as per requirements
		log.Printf("Skipping article with missing publish date: %s", item.Title)
		m.metrics.RecordArticleProcessed(feedURL, "skipped_no_publish_date")
		return false
	}

	// Skip rejected/reserved CVE entries — cvefeed.io (and similar CVE feeds) publish
	// "Rejected reason: …" items that carry no usable intelligence and flood the feed.
	if isRejectedCVE(item.Title) {
		m.metrics.RecordArticleProcessed(feedURL, "skipped_rejected_cve")
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

	var content string
	var contentHTML string
	var fetchDuration time.Duration

	// cvefeed.io's RSS description already carries the structured vulnerability
	// summary (CVE ID, description, severity); the live page is a Bootstrap
	// admin dashboard whose scraped text is nav/scoring-table chrome. Prefer
	// the feed's own description and skip the page fetch entirely.
	if strings.Contains(item.Link, "cvefeed.io") {
		content = cleanCvefeedDescription(item.Description)
		m.metrics.RecordArticleProcessed(feedURL, "used_feed_description")
	} else {
		// Fetch full content with context for graceful shutdown
		fetchCtx, fetchCancel := context.WithTimeout(context.Background(), m.config.API.Timeout)
		defer fetchCancel()
		startTime := time.Now()
		var err error
		content, contentHTML, err = m.fetchFullContent(fetchCtx, item.Link, item.Title)
		fetchDuration = time.Since(startTime)

		if err != nil {
			log.Printf("Failed to fetch content for %s: %v", item.Link, err)
			// Fallback to the feed description. HTML-bearing descriptions keep
			// their structure (in content_html) with a plain-text twin for
			// search/summarisation; plain ones are stored as text as before.
			if looksLikeHTML(item.Description) {
				contentHTML = item.Description
				content = stripTags(item.Description)
			} else {
				content = item.Description
			}
		}
	}

	// Create article struct
	article := Article{
		Title:         item.Title,
		URL:           item.Link,
		Content:       content,
		ContentHTML:   contentHTML,
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
// norm collapses all whitespace runs to single spaces and trims, so heading
// text can be compared against the RSS title regardless of the publisher's
// line breaks and indentation.
func norm(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// extractMainContent locates the article body of a parsed page and returns
// both a plain-text form and the body's original HTML. Text feeds search,
// previews, hashing, and summarisation; HTML backs structured rendering
// (headings, lists, images) in the frontend.
func extractMainContent(doc *goquery.Document, title string) (text, bodyHTML string) {
	// goquery's .Text() returns the raw source text of <script>/<style>
	// elements too (they're unrendered but still DOM text nodes) — strip
	// them first so CSS rules or JS don't leak into the stored article text.
	// "title" too: infostealers.com articles embed a complete HTML document
	// (its own <head>/<title>/<style>) inside the prose container; the style
	// tag is dropped above but the embedded page <title> would survive as a
	// phantom headline line. A <title> is never reader prose.
	doc.Find("script, style, title").Remove()

	// Strip known non-article widgets before measuring/using any selector's
	// text — e.g. "#ar-widget" is a common "Listen to this article"
	// audio-player plugin embedded inside the real content container, whose
	// own playback/voice-selector controls would otherwise be measured (and
	// stored) as if they were article prose.
	doc.Find("#ar-widget").Remove()

	// securityaffairs.com keeps its "You might also like" recommendations in
	// .recommended-post and its "Recent Articles" grid in .top-article (with
	// .sngl-article teaser cards), and renders a cookie-consent banner
	// (.cli-bar-popup / #cookie-law-info-bar). None are article prose; strip
	// them wherever they appear so a body fallback can't sweep them in.
	// bleepingcomputer.com's .cz-news-story-title-section is its byline/date
	// block — also chrome (the reading page renders byline and date itself).
	// .post-time is securityaffairs.com's byline/date block and .cta-tags its
	// tag cloud — both live inside .article-details-block, so the
	// container-fallback sweep would otherwise store them as article prose
	// (the reading page renders byline/date itself and has no use for a tag
	// link list).
	doc.Find(".recommended-post, .top-article, .cli-bar-popup, " +
		"#cookie-law-info-bar, #cookie_action_close_header, " +
		".cz-news-story-title-section, .post-time, .cta-tags").Remove()

	// theregister.com labels every ad slot with <span class="ad-label">REG AD</span>;
	// strip them so ad-slot labels don't pollute the extracted article text.
	doc.Find(".ad-label").Remove()

	// Future plc sites (techradar.com et al.) tag their page widgets with
	// data-component-name="..."; the article prose sits directly inside
	// <article>. The share/follow rail, video carousel, "more from" link
	// rivers, author bio, comment thread and sponsored promos are all inside
	// the <article> node, so the <article> match otherwise sweeps them in
	// around the real body (2026-10 techradar template).
	doc.Find(`[data-component-name="UtilityBar"], ` +
		`[data-component-name="JwPlayer:Carousel"], ` +
		`[data-component-name="Recirculation:ArticleRiver"], ` +
		`[data-component-name="Slice:AuthorBio"], ` +
		`[data-component-name="Viafora:Comments"], ` +
		`[data-component-name="Slice:PopularBox"], ` +
		`[data-component-name="SponsoredScroll"]`).Remove()

	// The same pages also inject a mid-article "You might also like"
	// recirculation widget straight into the prose: an <a id="elk-you-might-
	// also-..."> anchor, a <h3 id="section-you-might-also-..."> heading and a
	// related-links <ul id="elk-<uuid>">. The elk- prefix is Future plc's
	// server-side injected-block namespace, so this never matches real prose.
	doc.Find(`a[id^="elk-you-might-also"], ` +
		`h3[id^="section-you-might-also"], ` +
		`ul[id^="elk-"]`).Remove()

	// Future plc affiliate cards (hawk framework): the deal-widget root div
	// carries data-merchant-name, and its rendered text is a shopping CTA
	// ("Save up to 68% ... View Deal") — never prose. The inline newsletter
	// capture ("Are you a pro? Subscribe to our newsletter") and the
	// post-article TOPICS tag nav are equally non-prose chrome.
	//
	// Their "Read more" / "Latest in <category>" rivers are <aside
	// data-mrf-recirculation="article-river-stacked"> blocks inside
	// <article>; on short stories they carry ~10x the prose's text, so the
	// "asides are the body" guard below would otherwise keep them.
	doc.Find(`[data-merchant-name], ` +
		`[id^="slice-container-newsletterForm"], ` +
		`.newsletter-inbodyContent-slice, ` +
		`[data-component-name="PostArticleLinks"], ` +
		`aside[data-mrf-recirculation]`).Remove()

	// rapid7.com's blog template puts the tag cloud and share footer INSIDE
	// <article> (data-testid="blog-post-article-tags" / "blog-post-footer").
	// Tag links and a share rail are chrome, never prose.
	doc.Find(`[data-testid="blog-post-article-tags"], ` +
		`[data-testid="blog-post-footer"]`).Remove()

	// elastic.co security-labs pages end with a newsletter promo <section>
	// (its banner uses the publisher's "promo-banner" class family) and a
	// "Report an issue" feedback link row after the prose. Neither is
	// article prose.
	doc.Find("section").Each(func(_ int, s *goquery.Selection) {
		if s.Find(`[class*="promo-banner"]`).Length() > 0 {
			s.Remove()
		}
	})
	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		if norm(s.Text()) == "Report an issue" {
			s.Parent().Remove()
		}
	})

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
		// securityaffairs.com wraps the real body in .article-details-block;
		// without this tier entry, extraction fell back to <body> and swept
		// in its "You might also like" / "Recent Articles" widgets.
		".article-details-block",
		// bleepingcomputer.com wraps the prose in .articleBody; without it the
		// <article> match swept in the leading h1+byline/date block too. Its
		// .cz-news-story-title-section byline block (author h6 + date list)
		// is chrome, never prose — strip it wherever it appears, since the
		// longest-match ranking would otherwise pick the enclosing <article>
		// over .articleBody.
		".articleBody",
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

	// longest returns the candidate selection whose text is the longest —
	// the real article body dwarfs teaser cards and chrome.
	longest := func(selectors []string) *goquery.Selection {
		var best *goquery.Selection
		var bestLen int
		for _, selector := range selectors {
			doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
				if text := s.Text(); len(text) > bestLen {
					best = s
					bestLen = len(text)
				}
			})
		}
		return best
	}

	best := longest(preciseSelectors)
	if best == nil {
		best = longest(fallbackSelectors)
	}
	if best == nil {
		best = doc.Find("main").First()
	}
	if best == nil || best.Length() == 0 {
		best = doc.Find("body").First()
	}

	// If the winning container merely wraps another precise candidate that
	// holds most of its non-<aside> prose, prefer the inner candidate: it is
	// the real prose container, and the outer's remaining extra text is
	// title/byline chrome the reading page renders itself. This settles
	// infostealers.com, whose <article> wraps .entry-content alongside a
	// sidebar <aside> that holds half the container's text — exactly the
	// ratio the aside guard below cannot classify. The inner must not
	// itself live inside an <aside>: techradar's body-in-aside template
	// would otherwise be discarded wholesale.
	noaside := best.Clone()
	noaside.Find("aside").Remove()
	noasideLen := len(noaside.Text())
	if noasideLen > 0 {
		var inner *goquery.Selection
		for _, sel := range preciseSelectors {
			best.Find(sel).Each(func(_ int, s *goquery.Selection) {
				if s.Get(0) == best.Get(0) || (inner != nil && len(s.Text()) <= len(inner.Text())) {
					return
				}
				for p := s.Parent(); p.Length() > 0 && p.Get(0) != best.Get(0); p = p.Parent() {
					if n := p.Get(0); n.Type == html.ElementNode && n.Data == "aside" {
						return // the inner lives inside an <aside>
					}
				}
				if len(s.Text())*2 >= noasideLen {
					inner = s
				}
			})
		}
		if inner != nil {
			best = inner
		}
	}

	stripChrome := func(sel string) {
		before := len(best.Text())
		clone := best.Clone()
		clone.Find(sel).Remove()
		if len(clone.Text())*2 >= before {
			best.Find(sel).Remove()
		}
	}
	best.Find("header, nav, footer, form, button, " +
		"[x-show], [x-cloak], [hidden], [aria-hidden='true'], " +
		"[role='toolbar'], [role='menu'], [role='menubar'], [role='navigation'], " +
		".social-share, .share-buttons, .sharing-links, [class*='sharethis']").Remove()
	stripChrome("aside")

	// CSO Online (IDG) pages end the body with a "SUBSCRIBE TO OUR
	// NEWSLETTER" widget — heading, pitch copy and an email-capture block
	// the prose container sweeps in as a tail (2348-row feed; the widget is
	// JS-rendered, so the form strip above misses it). Truncate the body at
	// the heading: everything from it on is widget chrome.
	newsletter := best.Find("*").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.ToLower(norm(s.Text())) == "subscribe to our newsletter"
	}).Last()
	if newsletter.Length() > 0 {
		// Climb from the heading to best's direct child; drop that child and
		// every sibling after it.
		c := newsletter
		for c.Parent().Length() > 0 && c.Parent().Get(0) != best.Get(0) {
			c = c.Parent()
		}
		c.NextAll().Remove()
		c.Remove()
	}

	// infosecurity-magazine.com embeds related-article links mid-prose as
	// standalone paragraphs: <p><em><a>Read more: <headline></a></em></p>.
	// A block whose entire text opens with "Read more:" is a recirculation
	// line, never prose; strip it. Prose that merely links elsewhere or
	// mentions the phrase has additional text and survives.
	best.Find("p, li").Each(func(_ int, s *goquery.Selection) {
		if strings.HasPrefix(strings.ToLower(norm(s.Text())), "read more:") {
			s.Remove()
		}
	})

	// securityaffairs.com signs every article with a standing footer inside
	// .article-details-block: a "Follow me on Twitter: ..." paragraph, the
	// author's name as a bare link paragraph and the "(SecurityAffairs –
	// <topics>)" line, then a .social-media share row. All chrome — the
	// reading page renders the author itself and the share row's link soup
	// ("facebook / linkedin / twitter") read as article text. Match the
	// signature paragraphs by whole-paragraph text, not class: wp-block-
	// paragraph is generic Gutenberg markup and the site's real prose uses it.
	best.Find("p").Each(func(_ int, s *goquery.Selection) {
		t := strings.ToLower(norm(s.Text()))
		if t == "" {
			return
		}
		if strings.HasPrefix(t, "follow me on twitter") ||
			t == "pierluigi paganini" ||
			strings.HasPrefix(t, "(securityaffairs") {
			s.Remove()
		}
	})
	best.Find(".social-media").Remove()

	// A publisher's leading <h1> that repeats the article title duplicates
	// the headline the reading page already renders (bleepingcomputer puts
	// the h1+byline block in a plain <div class="article_section"> outside
	// any <header>; theregister's .k5a-article embeds its h1.headline
	// similarly). Remove it only on an exact text match so genuine h1
	// section headings survive. The title is unknown for some callers —
	// pass "" to skip this check.
	if norm(title) != "" {
		// securityaffairs.com wraps its duplicate headline in an h2 inside a
		// .common-heading.article-title div (row 90330) or straight after the
		// hero image (row 209: <img>, sharethis widget, <h2>headline +
		// editorial subtitle</h2>) — the first-child and h1-only checks never
		// see those. Scan the container's leading chrome zone: drop any
		// leading child whose entire text is the title, and any leading
		// h1/h2/h3 whose text starts with the title (the h2 carries an
		// editorial subtitle after the headline). The reading page renders
		// the headline itself, so a body that opens by restating it is
		// duplication noise. The prefix rule is heading-only so a genuine
		// lead paragraph that happens to open with the title text survives.
		// The scan stops at the first real content block.
		if tt := norm(title); tt != "" {
			children := best.Children()
			for i := 0; i < children.Length(); i++ {
				c := children.Eq(i)
				if c.Is("img, picture, figure, svg, video, source") || norm(c.Text()) == "" {
					continue // hero/media or empty spacer nodes
				}
				ft := norm(c.Text())
				if ft == tt || (strings.HasPrefix(ft, tt) && c.Is("h1, h2, h3")) {
					c.Remove()
					continue
				}
				break // first real content block reached
			}
		}
		best.Find("h1").First().Each(func(_ int, s *goquery.Selection) {
			if norm(s.Text()) == norm(title) {
				s.Remove()
			}
		})
	}

	text = strings.TrimSpace(best.Text())
	bodyHTML, _ = goquery.OuterHtml(best)
	// Only structural content (more than a bare paragraph or two) is worth
	// keeping as HTML; single-line extractions add nothing over the text.
	if looksLikeHTML(bodyHTML) && len(bodyHTML) > 200 {
		return text, bodyHTML
	}
	return text, ""
}

// fetchFullContent attempts to fetch the full content of an article. It
// returns the plain-text form and, when the page has a real article body,
// its original HTML for structured rendering. title is the RSS title, used
// to drop a body-embedded duplicate headline.
func (m *RSSMonitor) fetchFullContent(ctx context.Context, url, title string) (content, contentHTML string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}

	req.Header.Set("User-Agent", m.config.API.UserAgent)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Decode per the page's declared charset (goquery alone assumes UTF-8;
	// ISO-8859-1/GBK pages would otherwise yield mojibake or invalid UTF-8).
	// charset.NewReader consults the Content-Type header, the meta tag, and
	// the BOM, then strips a BOM as a side effect.
	rdr, err := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
	if err != nil {
		return "", "", err
	}

	// Parse HTML and extract text content
	doc, err := goquery.NewDocumentFromReader(rdr)
	if err != nil {
		return "", "", err
	}

	text, bodyHTML := extractMainContent(doc, title)
	if len(text) > m.config.Performance.MaxArticleContentLength { // Limit content length
		// Truncate on a rune boundary; byte-slicing can split a multi-byte
		// character and leave invalid UTF-8 that PostgreSQL rejects on save.
		text = safeTruncate(text, m.config.Performance.MaxArticleContentLength) + "..."
	}
	if len(bodyHTML) > m.config.Performance.MaxArticleHTMLLength {
		// A byte cut may split a tag or attribute; the frontend's parser
		// tolerates truncated markup (unbalanced tags are auto-closed).
		bodyHTML = safeTruncate(bodyHTML, m.config.Performance.MaxArticleHTMLLength)
	}

	return text, bodyHTML, nil
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
		INSERT INTO articles (title, url, full_content, content_html, publish_date, fetch_duration_ms, feed_url, content_hash, fetch_time, posted_to_discord)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), FALSE)
		ON CONFLICT (url) DO NOTHING`

	// Strip any invalid UTF-8 before insert: a single bad byte makes PostgreSQL
	// reject the whole row ("invalid byte sequence for encoding UTF8"), silently
	// dropping the article. Covers both truncation- and source-induced bad bytes.
	_, err := m.db.Exec(query,
		sanitizeUTF8(article.Title),
		sanitizeUTF8(article.URL),
		sanitizeUTF8(article.Content),
		sanitizeUTF8(article.ContentHTML),
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
		Model:        m.config.LLM.Model,
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

// clampFuturePublishDate treats anything more than 24h in the future as
// published now: some feeds stamp the future event date as the pubDate, which
// would let event promos outrank real news on a publish_date sort.
func clampFuturePublishDate(publishDate, now time.Time) time.Time {
	if publishDate.After(now.Add(24 * time.Hour)) {
		return now
	}
	return publishDate
}

// isRejectedCVE reports whether an item title marks a rejected/reserved CVE
// record (e.g. cvefeed.io items titled "CVE-… - Rejected reason: …"). These
// entries describe the CVE identifier's status, not a vulnerability, and are
// not worth storing or summarizing.
func isRejectedCVE(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	if !strings.HasPrefix(lower, "cve-") {
		return false
	}
	return strings.Contains(lower, "rejected reason") || strings.Contains(lower, "reserved")
}

// cleanCvefeedDescription converts cvefeed.io's structured RSS description
// (HTML-escaped <strong>label</strong> text with <br /> separators) into
// plain, readable text: labels become "Label:" prefixes, breaks become
// newlines, and any residual HTML tags are stripped.
func cleanCvefeedDescription(raw string) string {
	if raw == "" {
		return ""
	}
	s := raw
	// gofeed already decodes entities; tolerate both decoded and still-encoded forms.
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	// <br /> variants → newline
	for _, br := range []string{"<br />", "<br/>", "<br>"} {
		s = strings.ReplaceAll(s, br, "\n")
	}
	// Drop block-level tags; keep strong/i/em text inline.
	for _, tag := range []string{"</strong>", "<strong>", "</b>", "<b>", "</i>", "<i>", "</em>", "<em>", "</p>", "<p>"} {
		s = strings.ReplaceAll(s, tag, "")
	}
	// Drop empty lines entirely (indentation between <br /> tags produces
	// stray blanks; keep only content lines separated by single newlines).
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
