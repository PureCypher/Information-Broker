package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"information-broker/config"
	"log"
	"net/http"
	"strconv"
	"time"
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

// getArticles returns paginated articles
func (s *APIServer) getArticles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

	feedURL := r.URL.Query().Get("feed")

	// Build query
	var query string
	var args []interface{}

	if feedURL != "" {
		query = `
			SELECT title, url, content, published_at, fetched_at, fetch_duration_ms, feed_url, content_hash
			FROM articles 
			WHERE feed_url = $1
			ORDER BY published_at DESC 
			LIMIT $2 OFFSET $3`
		args = []interface{}{feedURL, limit, offset}
	} else {
		query = `
			SELECT title, url, content, published_at, fetched_at, fetch_duration_ms, feed_url, content_hash
			FROM articles 
			ORDER BY published_at DESC 
			LIMIT $1 OFFSET $2`
		args = []interface{}{limit, offset}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		log.Printf("Database query error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var article Article
		var fetchDurationMs int64
		var fetchedAt time.Time

		err := rows.Scan(
			&article.Title,
			&article.URL,
			&article.Content,
			&article.PublishedAt,
			&fetchedAt,
			&fetchDurationMs,
			&article.FeedURL,
			&article.ContentHash,
		)
		if err != nil {
			log.Printf("Row scan error: %v", err)
			continue
		}

		article.FetchDuration = time.Duration(fetchDurationMs) * time.Millisecond
		articles = append(articles, article)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"articles": articles,
		"count":    len(articles),
		"limit":    limit,
		"offset":   offset,
	})
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
		SELECT title, url, content, published_at, fetched_at, fetch_duration_ms, feed_url, content_hash
		FROM articles 
		ORDER BY fetched_at DESC 
		LIMIT $1`

	rows, err := s.db.Query(query, limit)
	if err != nil {
		log.Printf("Database query error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		var article Article
		var fetchDurationMs int64
		var fetchedAt time.Time

		err := rows.Scan(
			&article.Title,
			&article.URL,
			&article.Content,
			&article.PublishedAt,
			&fetchedAt,
			&fetchDurationMs,
			&article.FeedURL,
			&article.ContentHash,
		)
		if err != nil {
			log.Printf("Row scan error: %v", err)
			continue
		}

		article.FetchDuration = time.Duration(fetchDurationMs) * time.Millisecond
		articles = append(articles, article)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"latest_articles": articles,
		"count":           len(articles),
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
			MAX(published_at) as latest_article,
			MIN(published_at) as oldest_article,
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
