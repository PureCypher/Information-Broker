package main

import (
	"context"
	"database/sql"
	"fmt"
	"information-broker/config"
	"log"
	"math"
	"strings"
	"sync"
	"time"
)

// SummarizationRequest represents a request for article summarization
type SummarizationRequest struct {
	ArticleURL   string
	ArticleTitle string
	Content      string
	Model        string
	Priority     int // Higher values = higher priority
	EnqueuedAt   time.Time
	ResponseChan chan SummarizationResponse // Optional channel for response
}

// SummarizationResponse represents the response from summarization
type SummarizationResponse struct {
	Summary   string
	Error     error
	Duration  time.Duration
	Attempts  int
	Timestamp time.Time
}

// SummarizationScheduler manages a centralized queue for Ollama API calls
type SummarizationScheduler struct {
	// Core components
	queue         chan SummarizationRequest
	summarizer    *ArticleSummarizer
	db            *sql.DB
	config        *config.Config
	metrics       *PrometheusMetrics
	discordSender *DiscordWebhookSender

	// Control channels
	shutdown chan struct{}
	done     chan struct{}

	// State tracking
	mu             sync.RWMutex
	queueDepth     int
	totalProcessed int64
	totalErrors    int64
	isRunning      bool

	// Worker state
	currentRequest   *SummarizationRequest
	requestStartTime time.Time
}

// SummarizationSchedulerConfig holds configuration for the scheduler
type SummarizationSchedulerConfig struct {
	MaxQueueSize      int           `env:"SUMMARIZATION_MAX_QUEUE_SIZE" default:"100"`
	WorkerTimeout     time.Duration `env:"SUMMARIZATION_WORKER_TIMEOUT" default:"120s"`
	MaxRetries        int           `env:"SUMMARIZATION_MAX_RETRIES" default:"3"`
	RetryBackoffBase  time.Duration `env:"SUMMARIZATION_RETRY_BACKOFF_BASE" default:"1s"`
	MetricsInterval   time.Duration `env:"SUMMARIZATION_METRICS_INTERVAL" default:"10s"`
	QueuePurgeTimeout time.Duration `env:"SUMMARIZATION_QUEUE_PURGE_TIMEOUT" default:"1h"`
}

// NewSummarizationScheduler creates a new centralized summarization scheduler
func NewSummarizationScheduler(db *sql.DB, cfg *config.Config, metrics *PrometheusMetrics) *SummarizationScheduler {
	// Load scheduler-specific config
	schedulerConfig := loadSchedulerConfig(cfg)

	// Create the buffered channel for queuing requests
	queue := make(chan SummarizationRequest, schedulerConfig.MaxQueueSize)

	// Create summarizer instance
	summarizer := NewArticleSummarizer(db, cfg, metrics)

	// Create Discord webhook sender
	discordSender := NewDiscordWebhookSender(db, metrics)

	scheduler := &SummarizationScheduler{
		queue:         queue,
		summarizer:    summarizer,
		db:            db,
		config:        cfg,
		metrics:       metrics,
		discordSender: discordSender,
		shutdown:      make(chan struct{}),
		done:          make(chan struct{}),
		queueDepth:    0,
	}

	// Initialize metrics with queue capacity
	metrics.UpdateSummarizationQueueCapacity(schedulerConfig.MaxQueueSize)

	return scheduler
}

// loadSchedulerConfig loads scheduler configuration from the main config
func loadSchedulerConfig(cfg *config.Config) SummarizationSchedulerConfig {
	return SummarizationSchedulerConfig{
		MaxQueueSize:      cfg.Summarization.MaxQueueSize,
		WorkerTimeout:     cfg.Summarization.WorkerTimeout,
		MaxRetries:        cfg.Summarization.MaxRetries,
		RetryBackoffBase:  cfg.Summarization.RetryBackoffBase,
		MetricsInterval:   cfg.Summarization.MetricsInterval,
		QueuePurgeTimeout: cfg.Summarization.QueuePurgeTimeout,
	}
}

// Start begins the scheduler worker goroutine
func (s *SummarizationScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is already running")
	}
	s.isRunning = true
	s.mu.Unlock()

	log.Println("Starting summarization scheduler with single worker")

	// Start the single worker goroutine
	go s.worker(ctx)

	// Start metrics collection goroutine
	go s.metricsCollector(ctx)

	return nil
}

// Stop gracefully stops the scheduler
func (s *SummarizationScheduler) Stop() error {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is not running")
	}
	s.mu.Unlock()

	log.Println("Stopping summarization scheduler...")

	// Signal shutdown
	close(s.shutdown)

	// Wait for worker to finish
	select {
	case <-s.done:
		log.Println("Summarization scheduler stopped gracefully")
	case <-time.After(30 * time.Second):
		log.Println("Summarization scheduler shutdown timeout")
	}

	s.mu.Lock()
	s.isRunning = false
	s.mu.Unlock()

	return nil
}

// EnqueueSummarization adds a new summarization request to the queue
func (s *SummarizationScheduler) EnqueueSummarization(request SummarizationRequest) error {
	// Set enqueue timestamp
	request.EnqueuedAt = time.Now()

	// Set default model if not specified
	if request.Model == "" {
		request.Model = s.config.OLLAMA.Model
	}

	// Attempt to enqueue with timeout to prevent blocking
	select {
	case s.queue <- request:
		s.mu.Lock()
		s.queueDepth++
		newDepth := s.queueDepth
		s.mu.Unlock()

		// Update metrics immediately
		s.metrics.UpdateSummarizationQueueDepth(newDepth)

		log.Printf("Enqueued summarization request for article: %s (queue depth: %d)",
			request.ArticleTitle, newDepth)
		return nil

	default:
		// Queue is full - apply backpressure
		s.mu.Lock()
		s.totalErrors++
		s.mu.Unlock()

		err := fmt.Errorf("summarization queue is full (max size: %d)", cap(s.queue))
		log.Printf("Failed to enqueue summarization request for %s: %v", request.ArticleTitle, err)

		// Record metrics for queue full condition
		s.metrics.RecordSummaryAPIError(request.Model, "queue_full")

		return err
	}
}

// EnqueueSummarizationSync enqueues and waits for the summarization to complete
func (s *SummarizationScheduler) EnqueueSummarizationSync(request SummarizationRequest, timeout time.Duration) (*SummarizationResponse, error) {
	// Create response channel
	responseChan := make(chan SummarizationResponse, 1)
	request.ResponseChan = responseChan

	// Enqueue the request
	if err := s.EnqueueSummarization(request); err != nil {
		return nil, err
	}

	// Wait for response with timeout
	select {
	case response := <-responseChan:
		return &response, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("summarization request timed out after %v", timeout)
	}
}

// worker is the single worker goroutine that processes requests sequentially
func (s *SummarizationScheduler) worker(ctx context.Context) {
	defer close(s.done)

	config := loadSchedulerConfig(s.config)
	log.Printf("Summarization worker started with timeout: %v", config.WorkerTimeout)

	for {
		select {
		case <-ctx.Done():
			log.Println("Summarization worker stopping due to context cancellation")
			return

		case <-s.shutdown:
			log.Println("Summarization worker stopping due to shutdown signal")
			return

		case request := <-s.queue:
			s.mu.Lock()
			s.queueDepth--
			s.currentRequest = &request
			s.requestStartTime = time.Now()
			s.mu.Unlock()

			// Process the request with timeout
			response := s.processRequest(ctx, request, config)

			// Calculate wait time and record metrics
			waitTime := s.requestStartTime.Sub(request.EnqueuedAt)
			s.metrics.RecordSummarizationQueueWait(request.Model, waitTime)

			// Record processing metrics
			status := "success"
			if response.Error != nil {
				status = "error"
			}
			s.metrics.RecordSummarizationProcessing(request.Model, status, response.Duration)

			// Update statistics
			s.mu.Lock()
			s.totalProcessed++
			if response.Error != nil {
				s.totalErrors++
			}
			s.currentRequest = nil
			s.mu.Unlock()

			// Send response if channel is provided
			if request.ResponseChan != nil {
				select {
				case request.ResponseChan <- response:
				default:
					log.Printf("Failed to send response to channel for article: %s", request.ArticleTitle)
				}
			}

			// Save summary to database regardless of how it was requested
			if err := s.updateArticleSummary(request.ArticleURL, response.Summary); err != nil {
				log.Printf("Failed to save summary to database for %s: %v", request.ArticleURL, err)
			}

			// Send Discord notification if summarization was successful and webhooks are configured
			if response.Error == nil {
				webhookURLs := s.config.Discord.GetWebhookURLs()
				if len(webhookURLs) > 0 {
					go s.sendDiscordNotification(request, response.Summary)
				}
			}
		}
	}
}

// processRequest processes a single summarization request with retries and exponential backoff
func (s *SummarizationScheduler) processRequest(ctx context.Context, request SummarizationRequest, config SummarizationSchedulerConfig) SummarizationResponse {
	startTime := time.Now()

	log.Printf("Processing summarization request for: %s (model: %s)", request.ArticleTitle, request.Model)

	var lastErr error

	// Create a timeout context for this specific request
	requestCtx, cancel := context.WithTimeout(ctx, config.WorkerTimeout)
	defer cancel()

	// Retry logic with exponential backoff
	for attempt := 1; attempt <= config.MaxRetries; attempt++ {
		attemptStart := time.Now()

		// Call the summarizer (this is the ONLY place Ollama is called)
		summary, err := s.summarizer.SummarizeArticleWithModel(requestCtx, request.Content, request.ArticleURL, request.Model)
		attemptDuration := time.Since(attemptStart)

		if err == nil {
			// Success!
			totalDuration := time.Since(startTime)
			log.Printf("Successfully summarized article '%s' in %v (attempt %d/%d)",
				request.ArticleTitle, totalDuration, attempt, config.MaxRetries)

			return SummarizationResponse{
				Summary:   summary,
				Error:     nil,
				Duration:  totalDuration,
				Attempts:  attempt,
				Timestamp: time.Now(),
			}
		}

		lastErr = err
		log.Printf("Summarization attempt %d/%d failed for '%s': %v (took %v)",
			attempt, config.MaxRetries, request.ArticleTitle, err, attemptDuration)

		// Don't wait after the last attempt
		if attempt < config.MaxRetries {
			// Exponential backoff
			backoffDuration := time.Duration(math.Pow(2, float64(attempt-1))) * config.RetryBackoffBase

			select {
			case <-requestCtx.Done():
				// Context cancelled/timed out
				totalDuration := time.Since(startTime)
				log.Printf("Summarization context cancelled for '%s' after %v", request.ArticleTitle, totalDuration)

				return SummarizationResponse{
					Summary:   "summary unavailable",
					Error:     fmt.Errorf("context cancelled after %d attempts: %v", attempt, requestCtx.Err()),
					Duration:  totalDuration,
					Attempts:  attempt,
					Timestamp: time.Now(),
				}

			case <-time.After(backoffDuration):
				// Continue to next attempt
				log.Printf("Retrying summarization for '%s' after %v backoff", request.ArticleTitle, backoffDuration)
			}
		}
	}

	// All retries failed
	totalDuration := time.Since(startTime)
	log.Printf("All summarization attempts failed for '%s' after %v", request.ArticleTitle, totalDuration)

	return SummarizationResponse{
		Summary:   "summary unavailable",
		Error:     fmt.Errorf("summarization failed after %d attempts: %v", config.MaxRetries, lastErr),
		Duration:  totalDuration,
		Attempts:  config.MaxRetries,
		Timestamp: time.Now(),
	}
}

// metricsCollector periodically updates Prometheus metrics
func (s *SummarizationScheduler) metricsCollector(ctx context.Context) {
	config := loadSchedulerConfig(s.config)
	ticker := time.NewTicker(config.MetricsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdown:
			return
		case <-ticker.C:
			s.updateMetrics()
		}
	}
}

// updateMetrics updates Prometheus metrics with current scheduler state
func (s *SummarizationScheduler) updateMetrics() {
	s.mu.RLock()
	queueDepth := s.queueDepth
	currentRequest := s.currentRequest
	requestStartTime := s.requestStartTime
	s.mu.RUnlock()

	// Update queue depth metric
	s.metrics.UpdateSummarizationQueueDepth(queueDepth)

	// Log current state for debugging
	log.Printf("Summarization scheduler metrics - Queue depth: %d, Processing: %v",
		queueDepth, currentRequest != nil)

	if currentRequest != nil {
		processingDuration := time.Since(requestStartTime)
		log.Printf("Current request processing time: %v for article: %s",
			processingDuration, currentRequest.ArticleTitle)
	}
}

// updateArticleSummary updates the summary in the database
func (s *SummarizationScheduler) updateArticleSummary(articleURL, summary string) error {
	query := `UPDATE articles SET summary = $1, updated_at = NOW() WHERE url = $2`
	_, err := s.db.Exec(query, summary, articleURL)
	return err
}

// sendDiscordNotification sends Discord notifications to all configured webhooks for a successfully summarized article
func (s *SummarizationScheduler) sendDiscordNotification(request SummarizationRequest, summary string) {
	// Get all configured webhook URLs
	webhookURLs := s.config.Discord.GetWebhookURLs()
	if len(webhookURLs) == 0 {
		return
	}

	// Get article details from database
	feedTitle, publishDate := s.getArticleDetails(request.ArticleURL)

	// Check if article was published before the initiation date
	if publishDate.Before(s.config.App.InitiationDate) {
		log.Printf("Skipping Discord notification for article published before initiation date: %s (published: %s, initiation: %s)",
			request.ArticleTitle, publishDate.Format("2025-05-31"), s.config.App.InitiationDate.Format("2025-05-31"))
		return
	}

	// Create ArticleMessage for Discord
	articleMessage := ArticleMessage{
		Title:       request.ArticleTitle,
		URL:         request.ArticleURL,
		Summary:     summary,
		PublishDate: publishDate,
		FeedTitle:   feedTitle,
	}

	log.Printf("Sending Discord notifications to %d webhook(s) for article: %s", len(webhookURLs), request.ArticleTitle)

	// Send to all webhooks concurrently
	var wg sync.WaitGroup
	for i, webhookURL := range webhookURLs {
		wg.Add(1)
		go func(url string, webhookIndex int) {
			defer wg.Done()

			// Create context with timeout for each webhook call
			ctx, cancel := context.WithTimeout(context.Background(), s.config.Discord.Timeout)
			defer cancel()

			if err := s.discordSender.SendArticleToDiscord(ctx, url, articleMessage); err != nil {
				log.Printf("Failed to send Discord notification to webhook %d for article %s: %v",
					webhookIndex+1, request.ArticleTitle, err)
			} else {
				log.Printf("Successfully sent Discord notification to webhook %d for article: %s",
					webhookIndex+1, request.ArticleTitle)
			}
		}(webhookURL, i)
	}

	// Wait for all webhook calls to complete
	wg.Wait()
	log.Printf("Completed sending Discord notifications to %d webhook(s) for article: %s", len(webhookURLs), request.ArticleTitle)
}

// getArticleDetails retrieves the feed title and publish date for an article URL from the database
func (s *SummarizationScheduler) getArticleDetails(articleURL string) (string, time.Time) {
	var feedURL string
	var publishDate time.Time
	query := `SELECT feed_url, publish_date FROM articles WHERE url = $1 LIMIT 1`

	if err := s.db.QueryRow(query, articleURL).Scan(&feedURL, &publishDate); err != nil {
		log.Printf("Failed to get article details for %s: %v", articleURL, err)
		return "Unknown Feed", time.Now()
	}

	// Extract domain name from feed URL as a simple feed title
	feedTitle := feedURL
	if idx := strings.Index(feedURL, "://"); idx != -1 {
		feedTitle = feedURL[idx+3:]
	}
	if idx := strings.Index(feedTitle, "/"); idx != -1 {
		feedTitle = feedTitle[:idx]
	}

	return feedTitle, publishDate
}

// GetQueueDepth returns the current queue depth (thread-safe)
func (s *SummarizationScheduler) getQueueDepth() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queueDepth
}

// GetStats returns scheduler statistics
func (s *SummarizationScheduler) GetStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]interface{}{
		"queue_depth":     s.queueDepth,
		"queue_capacity":  cap(s.queue),
		"total_processed": s.totalProcessed,
		"total_errors":    s.totalErrors,
		"is_running":      s.isRunning,
		"current_request": s.currentRequest != nil,
	}

	if s.currentRequest != nil {
		stats["current_request_article"] = s.currentRequest.ArticleTitle
		stats["current_request_duration"] = time.Since(s.requestStartTime).String()
	}

	return stats
}

// Helper functions for environment variable parsing (reusing existing pattern)
// These functions reference the existing helper functions from the codebase
