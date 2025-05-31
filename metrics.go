package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusMetrics holds all the Prometheus metrics for the application
type PrometheusMetrics struct {
	// RSS fetch metrics
	rssFetchTotal    *prometheus.CounterVec
	rssFetchDuration *prometheus.HistogramVec
	rssFetchErrors   *prometheus.CounterVec

	// Article processing metrics
	articlesProcessed *prometheus.CounterVec
	newArticlesFound  *prometheus.CounterVec

	// Summarization API metrics
	summaryAPILatency *prometheus.HistogramVec
	summaryAPITotal   *prometheus.CounterVec
	summaryAPIErrors  *prometheus.CounterVec

	// Discord webhook metrics
	discordWebhookLatency *prometheus.HistogramVec
	discordWebhookTotal   *prometheus.CounterVec
	discordWebhookErrors  *prometheus.CounterVec

	// HTTP API metrics
	httpRequestDuration *prometheus.HistogramVec
	httpRequestsTotal   *prometheus.CounterVec

	// System metrics
	dbConnections *prometheus.GaugeVec

	// Circuit breaker metrics
	circuitBreakerState *prometheus.GaugeVec
	circuitBreakerTrips *prometheus.CounterVec

	// Summarization scheduler metrics
	summarizationQueueDepth     *prometheus.GaugeVec
	summarizationQueueCapacity  *prometheus.GaugeVec
	summarizationProcessingTime *prometheus.HistogramVec
	summarizationQueueWaitTime  *prometheus.HistogramVec
	summarizationTotalProcessed *prometheus.CounterVec

	// Article date filtering metrics
	articlesFilteredPreCutoff   *prometheus.CounterVec
	articlesProcessedPostCutoff *prometheus.CounterVec
}

// NewPrometheusMetrics creates and registers all Prometheus metrics
func NewPrometheusMetrics() *PrometheusMetrics {
	metrics := &PrometheusMetrics{
		// RSS fetch metrics
		rssFetchTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rss_fetch_total",
				Help: "Total number of RSS feed fetch attempts",
			},
			[]string{"feed_url", "status"},
		),
		rssFetchDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "rss_fetch_duration_seconds",
				Help:    "Time spent fetching RSS feeds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"feed_url", "status"},
		),
		rssFetchErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rss_fetch_errors_total",
				Help: "Total number of RSS fetch errors",
			},
			[]string{"feed_url", "error_type"},
		),

		// Article processing metrics
		articlesProcessed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "articles_processed_total",
				Help: "Total number of articles processed",
			},
			[]string{"feed_url", "status"},
		),
		newArticlesFound: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "new_articles_found_total",
				Help: "Total number of new articles found",
			},
			[]string{"feed_url"},
		),

		// Summarization API metrics
		summaryAPILatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "summary_api_duration_seconds",
				Help:    "Time spent calling summarization API",
				Buckets: []float64{0.1, 0.5, 1.0, 2.5, 5.0, 10.0, 15.0, 30.0, 60.0},
			},
			[]string{"model", "status"},
		),
		summaryAPITotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "summary_api_requests_total",
				Help: "Total number of summarization API requests",
			},
			[]string{"model", "status"},
		),
		summaryAPIErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "summary_api_errors_total",
				Help: "Total number of summarization API errors",
			},
			[]string{"model", "error_type"},
		),

		// Discord webhook metrics
		discordWebhookLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "discord_webhook_duration_seconds",
				Help:    "Time spent sending Discord webhooks",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.5, 5.0, 10.0},
			},
			[]string{"status"},
		),
		discordWebhookTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "discord_webhook_requests_total",
				Help: "Total number of Discord webhook requests",
			},
			[]string{"status"},
		),
		discordWebhookErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "discord_webhook_errors_total",
				Help: "Total number of Discord webhook errors",
			},
			[]string{"error_type"},
		),

		// HTTP API metrics
		httpRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "Time spent processing HTTP requests",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "endpoint", "status_code"},
		),
		httpRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "endpoint", "status_code"},
		),

		// System metrics
		dbConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "database_connections",
				Help: "Current number of database connections",
			},
			[]string{"state"},
		),

		// Circuit breaker metrics
		circuitBreakerState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "circuit_breaker_state",
				Help: "Current state of circuit breakers (0=closed, 1=half_open, 2=open)",
			},
			[]string{"name", "state"},
		),
		circuitBreakerTrips: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "circuit_breaker_trips_total",
				Help: "Total number of circuit breaker trips",
			},
			[]string{"name"},
		),

		// Summarization scheduler metrics
		summarizationQueueDepth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "summarization_queue_depth",
				Help: "Current number of articles in summarization queue",
			},
			[]string{},
		),
		summarizationQueueCapacity: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "summarization_queue_capacity",
				Help: "Maximum capacity of summarization queue",
			},
			[]string{},
		),
		summarizationProcessingTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "summarization_processing_duration_seconds",
				Help:    "Time spent processing summarization requests end-to-end",
				Buckets: []float64{0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0, 300.0},
			},
			[]string{"model", "status"},
		),
		summarizationQueueWaitTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "summarization_queue_wait_duration_seconds",
				Help:    "Time articles spend waiting in summarization queue",
				Buckets: []float64{0.1, 0.5, 1.0, 5.0, 15.0, 30.0, 60.0, 300.0, 600.0},
			},
			[]string{"model"},
		),
		summarizationTotalProcessed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "summarization_requests_processed_total",
				Help: "Total number of summarization requests processed by the scheduler",
			},
			[]string{"model", "status"},
		),

		// Article date filtering metrics
		articlesFilteredPreCutoff: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "articles_filtered_pre_cutoff_total",
				Help: "Total number of articles filtered out due to publication date before cutoff",
			},
			[]string{"feed_url"},
		),
		articlesProcessedPostCutoff: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "articles_processed_post_cutoff_total",
				Help: "Total number of articles processed after passing cutoff date filter",
			},
			[]string{"feed_url"},
		),
	}

	// Register all metrics
	prometheus.MustRegister(
		metrics.rssFetchTotal,
		metrics.rssFetchDuration,
		metrics.rssFetchErrors,
		metrics.articlesProcessed,
		metrics.newArticlesFound,
		metrics.summaryAPILatency,
		metrics.summaryAPITotal,
		metrics.summaryAPIErrors,
		metrics.discordWebhookLatency,
		metrics.discordWebhookTotal,
		metrics.discordWebhookErrors,
		metrics.httpRequestDuration,
		metrics.httpRequestsTotal,
		metrics.dbConnections,
		metrics.circuitBreakerState,
		metrics.circuitBreakerTrips,
		metrics.summarizationQueueDepth,
		metrics.summarizationQueueCapacity,
		metrics.summarizationProcessingTime,
		metrics.summarizationQueueWaitTime,
		metrics.summarizationTotalProcessed,
		metrics.articlesFilteredPreCutoff,
		metrics.articlesProcessedPostCutoff,
	)

	return metrics
}

// RecordRSSFetch records RSS fetch metrics
func (m *PrometheusMetrics) RecordRSSFetch(feedURL, status string, duration time.Duration) {
	m.rssFetchTotal.WithLabelValues(feedURL, status).Inc()
	m.rssFetchDuration.WithLabelValues(feedURL, status).Observe(duration.Seconds())
}

// RecordRSSFetchError records RSS fetch error metrics
func (m *PrometheusMetrics) RecordRSSFetchError(feedURL, errorType string) {
	m.rssFetchErrors.WithLabelValues(feedURL, errorType).Inc()
}

// RecordArticleProcessed records article processing metrics
func (m *PrometheusMetrics) RecordArticleProcessed(feedURL, status string) {
	m.articlesProcessed.WithLabelValues(feedURL, status).Inc()
}

// RecordNewArticles records new articles found metrics
func (m *PrometheusMetrics) RecordNewArticles(feedURL string, count int) {
	m.newArticlesFound.WithLabelValues(feedURL).Add(float64(count))
}

// RecordSummaryAPI records summarization API metrics
func (m *PrometheusMetrics) RecordSummaryAPI(model, status string, duration time.Duration) {
	m.summaryAPITotal.WithLabelValues(model, status).Inc()
	m.summaryAPILatency.WithLabelValues(model, status).Observe(duration.Seconds())
}

// RecordSummaryAPIError records summarization API error metrics
func (m *PrometheusMetrics) RecordSummaryAPIError(model, errorType string) {
	m.summaryAPIErrors.WithLabelValues(model, errorType).Inc()
}

// RecordDiscordWebhook records Discord webhook metrics
func (m *PrometheusMetrics) RecordDiscordWebhook(status string, duration time.Duration) {
	m.discordWebhookTotal.WithLabelValues(status).Inc()
	m.discordWebhookLatency.WithLabelValues(status).Observe(duration.Seconds())
}

// RecordDiscordWebhookError records Discord webhook error metrics
func (m *PrometheusMetrics) RecordDiscordWebhookError(errorType string) {
	m.discordWebhookErrors.WithLabelValues(errorType).Inc()
}

// RecordHTTPRequest records HTTP request metrics
func (m *PrometheusMetrics) RecordHTTPRequest(method, endpoint, statusCode string, duration time.Duration) {
	m.httpRequestsTotal.WithLabelValues(method, endpoint, statusCode).Inc()
	m.httpRequestDuration.WithLabelValues(method, endpoint, statusCode).Observe(duration.Seconds())
}

// UpdateDBConnections updates database connection metrics
func (m *PrometheusMetrics) UpdateDBConnections(open, inUse, idle int) {
	m.dbConnections.WithLabelValues("open").Set(float64(open))
	m.dbConnections.WithLabelValues("in_use").Set(float64(inUse))
	m.dbConnections.WithLabelValues("idle").Set(float64(idle))
}

// HTTPMetricsMiddleware creates a middleware for recording HTTP metrics
func (m *PrometheusMetrics) HTTPMetricsMiddleware(next http.HandlerFunc, endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer that captures the status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Call the next handler
		next(rw, r)

		// Record metrics
		duration := time.Since(start)
		statusCode := http.StatusText(rw.statusCode)
		m.RecordHTTPRequest(r.Method, endpoint, statusCode, duration)
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// UpdateCircuitBreakerState updates circuit breaker state metrics
func (m *PrometheusMetrics) UpdateCircuitBreakerState(name string, state CircuitBreakerState) {
	// Reset all state gauges for this circuit breaker
	m.circuitBreakerState.WithLabelValues(name, "closed").Set(0)
	m.circuitBreakerState.WithLabelValues(name, "half_open").Set(0)
	m.circuitBreakerState.WithLabelValues(name, "open").Set(0)

	// Set the current state to 1
	m.circuitBreakerState.WithLabelValues(name, string(state)).Set(1)
}

// RecordCircuitBreakerTrip records when a circuit breaker trips to open state
func (m *PrometheusMetrics) RecordCircuitBreakerTrip(name string) {
	m.circuitBreakerTrips.WithLabelValues(name).Inc()
}

// UpdateSummarizationQueueDepth updates the summarization queue depth metric
func (m *PrometheusMetrics) UpdateSummarizationQueueDepth(depth int) {
	m.summarizationQueueDepth.WithLabelValues().Set(float64(depth))
}

// UpdateSummarizationQueueCapacity updates the summarization queue capacity metric
func (m *PrometheusMetrics) UpdateSummarizationQueueCapacity(capacity int) {
	m.summarizationQueueCapacity.WithLabelValues().Set(float64(capacity))
}

// RecordSummarizationProcessing records end-to-end summarization processing metrics
func (m *PrometheusMetrics) RecordSummarizationProcessing(model, status string, duration time.Duration) {
	m.summarizationProcessingTime.WithLabelValues(model, status).Observe(duration.Seconds())
	m.summarizationTotalProcessed.WithLabelValues(model, status).Inc()
}

// RecordSummarizationQueueWait records time spent waiting in queue
func (m *PrometheusMetrics) RecordSummarizationQueueWait(model string, waitTime time.Duration) {
	m.summarizationQueueWaitTime.WithLabelValues(model).Observe(waitTime.Seconds())
}

// RecordArticleFilteredPreCutoff records when an article is filtered due to pre-cutoff date
func (m *PrometheusMetrics) RecordArticleFilteredPreCutoff(feedURL string) {
	m.articlesFilteredPreCutoff.WithLabelValues(feedURL).Inc()
}

// RecordArticleProcessedPostCutoff records when an article passes the cutoff date filter
func (m *PrometheusMetrics) RecordArticleProcessedPostCutoff(feedURL string) {
	m.articlesProcessedPostCutoff.WithLabelValues(feedURL).Inc()
}

// MetricsHandler returns the Prometheus metrics handler
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}
