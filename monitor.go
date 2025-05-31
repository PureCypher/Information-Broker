package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"information-broker/config"
	"log"
	"net/http"
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

	count := 0
	m.mutex.Lock()
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			log.Printf("Error scanning article URL: %v", err)
			continue
		}
		m.seenArticles[url] = true
		count++
	}
	m.mutex.Unlock()

	log.Printf("Loaded %d existing articles for deduplication", count)
	return rows.Err()
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
		duration := time.Since(startTime)
		m.logFetch(feedURL, "error", fmt.Sprintf("Failed to fetch feed: %v", err), duration, 0, 0)
		m.metrics.RecordRSSFetch(feedURL, "error", duration)
		m.metrics.RecordRSSFetchError(feedURL, "http_request_failed")
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
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

	// Process articles
	newArticles := 0
	totalArticles := len(feed.Items)

	for _, item := range feed.Items {
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

// processArticle processes a single article from an RSS feed
func (m *RSSMonitor) processArticle(item *gofeed.Item, feedURL string) bool {
	if item.Link == "" {
		m.metrics.RecordArticleProcessed(feedURL, "skipped_no_link")
		return false
	}

	// Check if we've already seen this article
	m.mutex.RLock()
	seen := m.seenArticles[item.Link]
	m.mutex.RUnlock()

	if seen {
		m.metrics.RecordArticleProcessed(feedURL, "skipped_duplicate")
		return false // Already processed
	}

	// Fetch full content
	startTime := time.Now()
	content, err := m.fetchFullContent(item.Link)
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

	// Set published time
	if item.PublishedParsed != nil {
		article.PublishedAt = *item.PublishedParsed
	} else {
		article.PublishedAt = time.Now()
	}

	// Generate content hash for deduplication
	article.ContentHash = m.generateContentHash(article.Title, article.URL, article.Content)

	// Save to database
	if err := m.saveArticle(article); err != nil {
		log.Printf("Failed to save article %s: %v", article.URL, err)
		m.metrics.RecordArticleProcessed(feedURL, "save_failed")
		return false
	}

	// Mark as seen
	m.mutex.Lock()
	m.seenArticles[item.Link] = true
	m.mutex.Unlock()

	// Record successful processing
	m.metrics.RecordArticleProcessed(feedURL, "processed")

	log.Printf("New article saved: %s", article.Title)

	// Try to generate summary for the new article
	go m.generateSummaryAsync(article)

	return true
}

// fetchFullContent attempts to fetch the full content of an article
func (m *RSSMonitor) fetchFullContent(url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
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

	// Try to find main content areas (common selectors)
	contentSelectors := []string{
		"article",
		".post-content",
		".entry-content",
		".content",
		"main",
		".article-body",
		".post-body",
	}

	var content string
	for _, selector := range contentSelectors {
		if text := doc.Find(selector).First().Text(); text != "" {
			content = text
			break
		}
	}

	// Fallback to body text if no specific content area found
	if content == "" {
		content = doc.Find("body").Text()
	}

	// Clean up the content
	content = strings.TrimSpace(content)
	if len(content) > m.config.Performance.MaxArticleContentLength { // Limit content length
		content = content[:m.config.Performance.MaxArticleContentLength] + "..."
	}

	return content, nil
}

// generateContentHash creates a unique hash for content deduplication
func (m *RSSMonitor) generateContentHash(title, url, content string) string {
	hasher := sha256.New()
	hasher.Write([]byte(title + url + content))
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

// saveArticle saves an article to the database
func (m *RSSMonitor) saveArticle(article Article) error {
	query := `
		INSERT INTO articles (title, url, full_content, publish_date, fetch_duration_ms, feed_url, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (url) DO NOTHING`

	_, err := m.db.Exec(query,
		article.Title,
		article.URL,
		article.Content,
		article.PublishedAt,
		article.FetchDuration.Milliseconds(),
		article.FeedURL,
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
