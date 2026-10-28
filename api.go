package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"information-broker/config"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// APIServer provides HTTP endpoints for accessing article data
type APIServer struct {
	db              *sql.DB
	port            int
	metrics         *PrometheusMetrics
	config          *config.Config
	circuitBreakers *CircuitBreakerManager
	scheduler       *SummarizationScheduler
}

// NewAPIServer creates a new API server instance
func NewAPIServer(db *sql.DB, port int, metrics *PrometheusMetrics, cfg *config.Config, circuitBreakers *CircuitBreakerManager, scheduler *SummarizationScheduler) *APIServer {
	return &APIServer{
		db:              db,
		port:            port,
		metrics:         metrics,
		config:          cfg,
		circuitBreakers: circuitBreakers,
		scheduler:       scheduler,
	}
}

// Start starts the HTTP server
func (s *APIServer) Start() {
	mux := http.NewServeMux()

	// Add CORS middleware
	corsHandler := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", s.config.Security.CORSAllowedOrigins)
			w.Header().Set("Access-Control-Allow-Methods", s.config.Security.CORSAllowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", s.config.Security.CORSAllowedHeaders)

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next(w, r)
		}
	}

	// Routes with metrics middleware
	mux.HandleFunc("/articles", corsHandler(s.metrics.HTTPMetricsMiddleware(s.getArticles, "/articles")))
	mux.HandleFunc("/articles/latest", corsHandler(s.metrics.HTTPMetricsMiddleware(s.getLatestArticles, "/articles/latest")))
	mux.HandleFunc("/articles/get", corsHandler(s.metrics.HTTPMetricsMiddleware(s.getArticleByID, "/articles/get")))
	mux.HandleFunc("/articles/digest", corsHandler(s.metrics.HTTPMetricsMiddleware(s.getArticlesDigest, "/articles/digest")))
	mux.HandleFunc("/feeds", corsHandler(s.metrics.HTTPMetricsMiddleware(s.getFeeds, "/feeds")))
	mux.HandleFunc("/stats", corsHandler(s.metrics.HTTPMetricsMiddleware(s.getStats, "/stats")))
	mux.HandleFunc("/summarization/stats", corsHandler(s.metrics.HTTPMetricsMiddleware(s.getSummarizationStats, "/summarization/stats")))
	mux.HandleFunc("/health", corsHandler(s.metrics.HTTPMetricsMiddleware(s.healthCheck, "/health")))

	// Prometheus metrics endpoint
	mux.Handle(s.config.Prometheus.MetricsPath, MetricsHandler())

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Starting API server on %s", addr)

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  s.config.Performance.HTTPReadTimeout,
		WriteTimeout: s.config.Performance.HTTPWriteTimeout,
		IdleTimeout:  s.config.Performance.HTTPIdleTimeout,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("API server failed: %v", err)
	}
}

// ArticleView is the JSON representation of an article returned by the API.
type ArticleView struct {
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Summary *string `json:"summary"`
	Content string  `json:"content"`
	// ContentHTML is the structured article body (original headings, lists,
	// images) when the source page provided one. Pointer + omitempty: absent
	// on list/latest responses (which don't select the column) and on older
	// backends. The frontend sanitizes it with a strict whitelist before
	// rendering; never treat it as trusted.
	ContentHTML    *string       `json:"content_html,omitempty"`
	PublishedAt    time.Time     `json:"published_at"`
	FetchDuration  time.Duration `json:"fetch_duration"`
	FeedURL        string        `json:"feed_url"`
	ContentHash    string        `json:"content_hash"`
	CrossFeedCount int           `json:"cross_feed_count,omitempty"`
	// StoryClusterID is the digest clustering key (the seed article's own id).
	// Pointer + omitempty: NULL for articles ClusteringScheduler hasn't reached
	// yet, and absent entirely from /articles, which doesn't select the column.
	StoryClusterID *int64 `json:"story_cluster_id,omitempty"`
	// SummaryState distinguishes summarization outcomes honestly:
	// "available" (LLM summary stored), "pending" (not yet reached by the
	// summarizer), "failed" (retries exhausted; stored as the literal
	// "summary unavailable" fallback text). See summaryStateOf.
	SummaryState string `json:"summary_state"`
	// FetchTime is when the backend ingested the article. Only /articles/latest
	// selects it; nil (and thus absent from JSON) on the other endpoints.
	FetchTime *time.Time `json:"fetch_time,omitempty"`
}

// maxFeedFilters bounds how many sources one /articles request may combine.
const maxFeedFilters = 25

var errTooManyFeedFilters = fmt.Errorf("too many feed filters (max %d)", maxFeedFilters)

// normalizeFeedFilters cleans the repeatable ?feed= values: trims whitespace,
// drops empties (the legacy "All sources" submit sends feed=), dedupes
// preserving first-seen order, and rejects more than maxFeedFilters distinct
// feeds. The cap applies after dedupe.
func normalizeFeedFilters(values []string) ([]string, error) {
	var out []string
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) > maxFeedFilters {
		return nil, errTooManyFeedFilters
	}
	return out, nil
}

// buildArticlesWhere returns the WHERE clause ("" when no filters) with its
// ordered placeholder args; nextIdx is the next free placeholder number so
// callers can append more parameters without renumbering. feeds must already
// be normalized (normalizeFeedFilters): one feed keeps plain equality, two or
// more match any of them (OR).
func buildArticlesWhere(feeds []string, q string) (string, []interface{}, int) {
	q = strings.TrimSpace(q)
	if len(q) < 2 {
		q = "" // ignore empty/too-short searches to avoid full-table ILIKE scans
	}
	var conds []string
	var args []interface{}
	i := 1
	switch len(feeds) {
	case 0:
	case 1:
		conds = append(conds, fmt.Sprintf("feed_url = $%d", i))
		args = append(args, feeds[0])
		i++
	default:
		conds = append(conds, fmt.Sprintf("feed_url = ANY($%d)", i))
		args = append(args, pq.Array(feeds))
		i++
	}
	if q != "" {
		conds = append(conds, fmt.Sprintf("(title ILIKE $%d OR summary ILIKE $%d OR full_content ILIKE $%d)", i, i+1, i+2))
		like := "%" + q + "%"
		args = append(args, like, like, like)
		i += 3
	}
	if len(conds) == 0 {
		return "", nil, 1
	}
	return strings.Join(conds, " AND "), args, i
}

// buildArticlesQuery constructs the SQL and ordered args for listing articles,
// applying optional feed (one or several) and case-insensitive search (q) filters, with optional sort order.
func buildArticlesQuery(feeds []string, q, sort string, limit, offset int) (string, []interface{}) {
	where, args, i := buildArticlesWhere(feeds, q)
	query := `SELECT id, title, url, summary, full_content, publish_date, fetch_duration_ms, feed_url, content_hash
		FROM articles`
	if where != "" {
		query += " WHERE " + where
	}
	order := "DESC"
	if sort == "oldest" {
		order = "ASC"
	}
	query += fmt.Sprintf(" ORDER BY publish_date %s LIMIT $%d OFFSET $%d", order, i, i+1)
	args = append(args, limit, offset)
	return query, args
}

// summaryStateOf maps a stored summary to the public summary_state. The
// summarizer persists the literal "summary unavailable" when retries are
// exhausted (summarizer.go:246). NULL plus stored article text means
// summarization hasn't reached the article yet — "pending" (the requeue
// catch-up command and the normal pipeline both pick such rows up). NULL with
// no stored text can never be summarized, so it is "unavailable", not
// "pending": the reading page must not promise queued work that doesn't
// exist.
func summaryStateOf(summary *string, content string) string {
	switch {
	case summary == nil:
		if strings.TrimSpace(content) == "" {
			return "unavailable"
		}
		return "pending"
	case strings.EqualFold(strings.TrimSpace(*summary), "summary unavailable"):
		return "failed"
	default:
		return "available"
	}
}

// getArticles returns paginated articles
func (s *APIServer) getArticles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate the feed filters first: a bad request must be rejected before
	// any database access.
	feedURLs, err := normalizeFeedFilters(r.URL.Query()["feed"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse query parameters
	limit := 50 // default
	offset := 0 // default

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	searchQ := r.URL.Query().Get("q")

	query, args := buildArticlesQuery(feedURLs, searchQ, r.URL.Query().Get("sort"), limit, offset)

	// Total matching rows for honest pagination ("PAGE 2 OF 7"); the count
	// mirrors the listing's WHERE clause exactly. Non-fatal on error — the
	// listing still works, the total is just absent from the response.
	where, countArgs, _ := buildArticlesWhere(feedURLs, searchQ)
	countQuery := "SELECT COUNT(*) FROM articles"
	if where != "" {
		countQuery += " WHERE " + where
	}
	var total int64
	countOK := true
	if err := s.db.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		log.Printf("Count query error: %v", err)
		countOK = false
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		log.Printf("Database query error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	articles := []ArticleView{}
	for rows.Next() {
		var article ArticleView
		var fetchDurationMs int64

		err := rows.Scan(
			&article.ID,
			&article.Title,
			&article.URL,
			&article.Summary,
			&article.Content,
			&article.PublishedAt,
			&fetchDurationMs,
			&article.FeedURL,
			&article.ContentHash,
		)
		if err != nil {
			log.Printf("Row scan error: %v", err)
			continue
		}

		article.FetchDuration = time.Duration(fetchDurationMs) * time.Millisecond
		article.SummaryState = summaryStateOf(article.Summary, article.Content)
		articles = append(articles, article)
	}

	w.Header().Set("Content-Type", "application/json")
	// Struct response so a failed count query can omit `total` entirely
	// (omitempty) rather than sending 0, which the frontend would read as
	// "no results" instead of "total unknown".
	json.NewEncoder(w).Encode(struct {
		Articles []ArticleView `json:"articles"`
		Count    int           `json:"count"`
		Total    int64         `json:"total,omitempty"`
		Limit    int           `json:"limit"`
		Offset   int           `json:"offset"`
	}{
		Articles: articles,
		Count:    len(articles),
		Total: func() int64 {
			if countOK {
				return total
			}
			return 0
		}(),
		Limit:  limit,
		Offset: offset,
	})
}

// parseArticleID validates and parses an article id query value.
func parseArticleID(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("missing id")
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id: %q", s)
	}
	return id, nil
}

// getArticleByID returns a single article (incl. summary + full content) by id.
func (s *APIServer) getArticleByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, err := parseArticleID(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	query := `SELECT id, title, url, summary, full_content, content_html, publish_date, fetch_duration_ms, feed_url, content_hash
		FROM articles WHERE id = $1`

	var article ArticleView
	var fetchDurationMs int64
	err = s.db.QueryRow(query, id).Scan(
		&article.ID,
		&article.Title,
		&article.URL,
		&article.Summary,
		&article.Content,
		&article.ContentHTML,
		&article.PublishedAt,
		&fetchDurationMs,
		&article.FeedURL,
		&article.ContentHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("Database query error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	article.FetchDuration = time.Duration(fetchDurationMs) * time.Millisecond
	article.SummaryState = summaryStateOf(article.Summary, article.Content)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(article)
}

// getLatestArticles returns the most recent articles across all feeds
func (s *APIServer) getLatestArticles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := 20 // default for latest
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	query := `
		SELECT id, title, url, summary, full_content, publish_date, fetch_time, fetch_duration_ms, feed_url, content_hash
		FROM articles
		ORDER BY fetch_time DESC
		LIMIT $1`

	rows, err := s.db.Query(query, limit)
	if err != nil {
		log.Printf("Database query error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	articles := []ArticleView{}
	for rows.Next() {
		var article ArticleView
		var fetchDurationMs int64

		err := rows.Scan(
			&article.ID,
			&article.Title,
			&article.URL,
			&article.Summary,
			&article.Content,
			&article.PublishedAt,
			&article.FetchTime,
			&fetchDurationMs,
			&article.FeedURL,
			&article.ContentHash,
		)
		if err != nil {
			log.Printf("Row scan error: %v", err)
			continue
		}

		article.FetchDuration = time.Duration(fetchDurationMs) * time.Millisecond
		article.SummaryState = summaryStateOf(article.Summary, article.Content)
		articles = append(articles, article)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"articles": articles,
		"count":    len(articles),
	})
}

// getFeeds returns statistics about each RSS feed
func (s *APIServer) getFeeds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := `
		SELECT 
			feed_url,
			COUNT(*) as article_count,
			MAX(publish_date) as latest_article,
			MIN(publish_date) as oldest_article,
			AVG(fetch_duration_ms) as avg_fetch_duration_ms
		FROM articles 
		GROUP BY feed_url 
		ORDER BY article_count DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		log.Printf("Database query error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type FeedStats struct {
		FeedURL            string     `json:"feed_url"`
		ArticleCount       int        `json:"article_count"`
		LatestArticle      *time.Time `json:"latest_article"`
		OldestArticle      *time.Time `json:"oldest_article"`
		AvgFetchDurationMs *float64   `json:"avg_fetch_duration_ms"`
	}

	var feeds []FeedStats
	for rows.Next() {
		var feed FeedStats
		err := rows.Scan(
			&feed.FeedURL,
			&feed.ArticleCount,
			&feed.LatestArticle,
			&feed.OldestArticle,
			&feed.AvgFetchDurationMs,
		)
		if err != nil {
			log.Printf("Row scan error: %v", err)
			continue
		}
		feeds = append(feeds, feed)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"feeds": feeds,
		"count": len(feeds),
	})
}

// getStats returns overall system statistics
func (s *APIServer) getStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type Stats struct {
		TotalArticles     int        `json:"total_articles"`
		TotalFeeds        int        `json:"total_feeds"`
		LastFetch         *time.Time `json:"last_fetch"`
		SuccessfulFetches int        `json:"successful_fetches_24h"`
		FailedFetches     int        `json:"failed_fetches_24h"`
		AvgFetchTime      *float64   `json:"avg_fetch_time_ms"`
		ArticlesToday     int        `json:"articles_today"`
		ArticlesThisWeek  int        `json:"articles_this_week"`
		ArticlesThisMonth int        `json:"articles_this_month"`
	}

	var stats Stats

	// Get total articles
	err := s.db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&stats.TotalArticles)
	if err != nil {
		log.Printf("Error getting total articles: %v", err)
	}

	// Get total feeds
	err = s.db.QueryRow("SELECT COUNT(DISTINCT feed_url) FROM articles").Scan(&stats.TotalFeeds)
	if err != nil {
		log.Printf("Error getting total feeds: %v", err)
	}

	// Get last fetch time
	err = s.db.QueryRow("SELECT MAX(created_at) FROM fetch_logs").Scan(&stats.LastFetch)
	if err != nil {
		log.Printf("Error getting last fetch time: %v", err)
	}

	// Get 24h fetch statistics
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM fetch_logs 
		WHERE status = 'success' AND created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&stats.SuccessfulFetches)
	if err != nil {
		log.Printf("Error getting successful fetches: %v", err)
	}

	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM fetch_logs 
		WHERE status = 'error' AND created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&stats.FailedFetches)
	if err != nil {
		log.Printf("Error getting failed fetches: %v", err)
	}

	// Get average fetch time
	err = s.db.QueryRow(`
		SELECT AVG(duration_ms) FROM fetch_logs
		WHERE status = 'success' AND created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&stats.AvgFetchTime)
	if err != nil {
		log.Printf("Error getting average fetch time: %v", err)
	}

	// Get articles collected today/this week/this month, by fetch_time (when
	// we ingested it, not the source's own publish_date).
	err = s.db.QueryRow(`SELECT COUNT(*) FROM articles WHERE fetch_time >= NOW() - INTERVAL '24 hours'`).Scan(&stats.ArticlesToday)
	if err != nil {
		log.Printf("Error getting articles today: %v", err)
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM articles WHERE fetch_time >= NOW() - INTERVAL '7 days'`).Scan(&stats.ArticlesThisWeek)
	if err != nil {
		log.Printf("Error getting articles this week: %v", err)
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM articles WHERE fetch_time >= NOW() - INTERVAL '30 days'`).Scan(&stats.ArticlesThisMonth)
	if err != nil {
		log.Printf("Error getting articles this month: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HealthStatus represents the overall health status
type HealthStatus struct {
	Status          string                          `json:"status"`
	Timestamp       string                          `json:"timestamp"`
	Version         string                          `json:"version"`
	Database        DatabaseHealth                  `json:"database"`
	CircuitBreakers map[string]CircuitBreakerStatus `json:"circuit_breakers"`
	SystemMetrics   SystemMetrics                   `json:"system_metrics"`
	Services        map[string]ServiceHealth        `json:"services"`
}

// DatabaseHealth represents database health information
type DatabaseHealth struct {
	Status      string `json:"status"`
	Connections struct {
		Open  int `json:"open"`
		InUse int `json:"in_use"`
		Idle  int `json:"idle"`
	} `json:"connections"`
	LastError string `json:"last_error,omitempty"`
}

// SystemMetrics represents basic system metrics
type SystemMetrics struct {
	UptimeSeconds int64 `json:"uptime_seconds"`
	GoRoutines    int   `json:"goroutines"`
	MemoryMB      int   `json:"memory_mb"`
}

// ServiceHealth represents individual service health
type ServiceHealth struct {
	Status       string    `json:"status"`
	LastCheck    time.Time `json:"last_check"`
	ResponseTime string    `json:"response_time,omitempty"`
	Error        string    `json:"error,omitempty"`
}

var startTime = time.Now()

// healthCheck returns the comprehensive health status of the service
func (s *APIServer) healthCheck(w http.ResponseWriter, r *http.Request) {
	health := HealthStatus{
		Timestamp:       time.Now().Format(time.RFC3339),
		Version:         "1.0.0",
		CircuitBreakers: s.circuitBreakers.GetStatus(),
		Services:        make(map[string]ServiceHealth),
	}

	// Check database health
	dbHealth := DatabaseHealth{
		Status: "healthy",
	}

	if err := s.db.Ping(); err != nil {
		dbHealth.Status = "unhealthy"
		dbHealth.LastError = err.Error()
		health.Status = "degraded"
	}

	// Get database connection stats
	stats := s.db.Stats()
	dbHealth.Connections.Open = stats.OpenConnections
	dbHealth.Connections.InUse = stats.InUse
	dbHealth.Connections.Idle = stats.Idle
	health.Database = dbHealth

	// Check circuit breaker states
	overallHealthy := true
	for _, cb := range health.CircuitBreakers {
		if cb.State == StateOpen {
			overallHealthy = false
			break
		}
	}

	// System metrics
	health.SystemMetrics = SystemMetrics{
		UptimeSeconds: int64(time.Since(startTime).Seconds()),
		GoRoutines:    0, // runtime.NumGoroutine() if needed
		MemoryMB:      0, // Can add memory stats if needed
	}

	// Overall health status
	if health.Status == "" {
		if overallHealthy && dbHealth.Status == "healthy" {
			health.Status = "healthy"
		} else {
			health.Status = "degraded"
		}
	}

	// Set HTTP status code based on health
	statusCode := http.StatusOK
	if health.Status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	} else if health.Status == "degraded" {
		statusCode = http.StatusPartialContent
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(health)
}

// getSummarizationStats returns summarization scheduler statistics
func (s *APIServer) getSummarizationStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get scheduler statistics
	stats := s.scheduler.GetStats()

	// Add database-based summarization statistics
	summaryStats := make(map[string]interface{})

	// Get total summaries processed
	var totalSummaries int
	err := s.db.QueryRow("SELECT COUNT(*) FROM summary_logs WHERE status = 'success'").Scan(&totalSummaries)
	if err != nil {
		log.Printf("Error getting total summaries: %v", err)
	} else {
		summaryStats["total_successful_summaries"] = totalSummaries
	}

	// Get failed summaries in last 24h
	var failedSummaries24h int
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM summary_logs
		WHERE status = 'failed' AND created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&failedSummaries24h)
	if err != nil {
		log.Printf("Error getting failed summaries: %v", err)
	} else {
		summaryStats["failed_summaries_24h"] = failedSummaries24h
	}

	// Get average processing time
	var avgProcessingTime *float64
	err = s.db.QueryRow(`
		SELECT AVG(duration_ms) FROM summary_logs
		WHERE status = 'success' AND created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&avgProcessingTime)
	if err != nil {
		log.Printf("Error getting average processing time: %v", err)
	} else {
		summaryStats["avg_processing_time_ms_24h"] = avgProcessingTime
	}

	// Get most recent error
	var recentError *string
	var recentErrorTime *time.Time
	err = s.db.QueryRow(`
		SELECT error_message, created_at FROM summary_logs
		WHERE status = 'failed' AND error_message IS NOT NULL
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&recentError, &recentErrorTime)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("Error getting recent error: %v", err)
	} else if recentError != nil {
		summaryStats["recent_error"] = map[string]interface{}{
			"message": *recentError,
			"time":    *recentErrorTime,
		}
	}

	// Combine scheduler stats with database stats
	response := map[string]interface{}{
		"scheduler": stats,
		"database":  summaryStats,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
