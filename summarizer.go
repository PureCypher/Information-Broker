package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"information-broker/config"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// SummaryRequest represents the request payload for OLLAMA API
type SummaryRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// SummaryResponse represents the response from OLLAMA API
type SummaryResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

// SummaryLog represents the logging structure for summary operations
type SummaryLog struct {
	ArticleURL   string        `json:"article_url"`
	Model        string        `json:"model"`
	Status       string        `json:"status"`
	Summary      string        `json:"summary"`
	ErrorMessage string        `json:"error_message,omitempty"`
	Duration     time.Duration `json:"duration"`
	RetryAttempt int           `json:"retry_attempt"`
	CreatedAt    time.Time     `json:"created_at"`
}

// ArticleSummarizer handles AI-powered article summarization
type ArticleSummarizer struct {
	db         *sql.DB
	httpClient *http.Client
	config     *config.Config
	metrics    *PrometheusMetrics
}

// NewArticleSummarizer creates a new article summarizer instance with centralized configuration
func NewArticleSummarizer(db *sql.DB, cfg *config.Config, metrics *PrometheusMetrics) *ArticleSummarizer {
	return &ArticleSummarizer{
		db: db,
		httpClient: &http.Client{
			Timeout: cfg.OLLAMA.Timeout,
		},
		config:  cfg,
		metrics: metrics,
	}
}

// SummarizeArticle generates a concise summary of the article text using OLLAMA
// It handles retries with exponential backoff and logs all operations to PostgreSQL
func (s *ArticleSummarizer) SummarizeArticle(ctx context.Context, articleText, articleURL, model string) (string, error) {
	startTime := time.Now()

	// Validate inputs
	if strings.TrimSpace(articleText) == "" {
		return s.handleSummaryFailure(articleURL, model, "empty article text", 0, startTime)
	}

	if strings.TrimSpace(model) == "" {
		model = s.config.OLLAMA.Model // Use configured default model
	}

	// Create the prompt for summarization
	prompt := s.createSummaryPrompt(articleText)

	var lastErr error

	// Retry logic with exponential backoff
	for attempt := 1; attempt <= s.config.OLLAMA.MaxRetries; attempt++ {
		attemptStart := time.Now()

		summary, err := s.callOllamaAPI(ctx, prompt, model)
		attemptDuration := time.Since(attemptStart)

		if err == nil {
			// Success - log and return
			s.logSummaryOperation(SummaryLog{
				ArticleURL:   articleURL,
				Model:        model,
				Status:       "success",
				Summary:      summary,
				Duration:     attemptDuration,
				RetryAttempt: attempt,
				CreatedAt:    time.Now(),
			})

			// Record successful metrics
			s.metrics.RecordSummaryAPI(model, "success", attemptDuration)

			log.Printf("Successfully summarized article %s with model %s (attempt %d/%d)",
				articleURL, model, attempt, s.config.OLLAMA.MaxRetries)
			return summary, nil
		}

		lastErr = err

		// Log failed attempt
		s.logSummaryOperation(SummaryLog{
			ArticleURL:   articleURL,
			Model:        model,
			Status:       "retry_failed",
			ErrorMessage: err.Error(),
			Duration:     attemptDuration,
			RetryAttempt: attempt,
			CreatedAt:    time.Now(),
		})

		// Record failed attempt metrics
		s.metrics.RecordSummaryAPI(model, "error", attemptDuration)
		s.metrics.RecordSummaryAPIError(model, "api_call_failed")

		log.Printf("Summary attempt %d/%d failed for %s: %v", attempt, s.config.OLLAMA.MaxRetries, articleURL, err)

		// Don't wait after the last attempt
		if attempt < s.config.OLLAMA.MaxRetries {
			// Exponential backoff: 1s, 2s, 4s, 8s, etc.
			backoffDuration := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second

			select {
			case <-ctx.Done():
				s.metrics.RecordSummaryAPIError(model, "context_cancelled")
				return s.handleSummaryFailure(articleURL, model, "context cancelled", attempt, startTime)
			case <-time.After(backoffDuration):
				// Continue to next attempt
			}
		}
	}

	// All retries failed
	return s.handleSummaryFailure(articleURL, model, lastErr.Error(), s.config.OLLAMA.MaxRetries, startTime)
}

// createSummaryPrompt creates a well-structured prompt for article summarization
func (s *ArticleSummarizer) createSummaryPrompt(articleText string) string {
	// Truncate article if it's too long to avoid token limits
	maxChars := s.config.Performance.MaxArticleContentLength
	if len(articleText) > maxChars {
		articleText = articleText[:maxChars] + "..."
	}

	maxSummaryLength := s.config.Content.MaxSummaryLength

	return fmt.Sprintf(`Please provide a concise summary of the following article in exactly %d words or less. The summary should be:
- Written in clear, simple language that non-technical users can understand
- Focused on the main points and key takeaways
- Objective and factual
- Complete sentences with proper grammar

Article text:
%s

Summary:`, maxSummaryLength, articleText)
}

// callOllamaAPI makes the actual API call to OLLAMA
func (s *ArticleSummarizer) callOllamaAPI(ctx context.Context, prompt, model string) (string, error) {
	// Prepare request payload
	reqPayload := SummaryRequest{
		Model:  model,
		Prompt: prompt,
		Stream: false, // We want the complete response, not streaming
	}

	jsonData, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, "POST", s.config.OLLAMA.URL+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.config.API.UserAgent)

	// Make the API call
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OLLAMA API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var summaryResp SummaryResponse
	if err := json.Unmarshal(body, &summaryResp); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %w", err)
	}

	// Check for API errors
	if summaryResp.Error != "" {
		return "", fmt.Errorf("OLLAMA API error: %s", summaryResp.Error)
	}

	// Validate response
	summary := strings.TrimSpace(summaryResp.Response)
	if summary == "" {
		return "", fmt.Errorf("received empty summary from OLLAMA")
	}

	// Clean the summary by removing thinking tags and other unwanted content
	summary = cleanSummaryContent(summary)

	// Ensure summary is within configured word limit (approximately)
	words := strings.Fields(summary)
	maxWords := s.config.Content.MaxSummaryLength
	if len(words) > maxWords+20 { // Slightly more than configured to account for variations
		summary = strings.Join(words[:maxWords], " ") + "..."
	}

	return summary, nil
}

// handleSummaryFailure handles the case when all retry attempts fail
func (s *ArticleSummarizer) handleSummaryFailure(articleURL, model, errorMsg string, attempts int, startTime time.Time) (string, error) {
	const fallbackSummary = "summary unavailable"

	duration := time.Since(startTime)

	// Log final failure
	s.logSummaryOperation(SummaryLog{
		ArticleURL:   articleURL,
		Model:        model,
		Status:       "failed",
		Summary:      fallbackSummary,
		ErrorMessage: errorMsg,
		Duration:     duration,
		RetryAttempt: attempts,
		CreatedAt:    time.Now(),
	})

	log.Printf("Failed to summarize article %s after %d attempts: %s", articleURL, attempts, errorMsg)

	// Return the placeholder summary as requested
	return fallbackSummary, fmt.Errorf("summarization failed after %d attempts: %s", attempts, errorMsg)
}

// logSummaryOperation logs summary operations to PostgreSQL
func (s *ArticleSummarizer) logSummaryOperation(logEntry SummaryLog) {
	query := `
		INSERT INTO summary_logs (
			article_url, model, status, summary, error_message, 
			duration_ms, retry_attempt, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := s.db.Exec(query,
		logEntry.ArticleURL,
		logEntry.Model,
		logEntry.Status,
		logEntry.Summary,
		logEntry.ErrorMessage,
		logEntry.Duration.Milliseconds(),
		logEntry.RetryAttempt,
		logEntry.CreatedAt,
	)

	if err != nil {
		log.Printf("Failed to log summary operation to database: %v", err)
	}
}

// InitializeSummaryTables creates the necessary database tables for summary logging
func InitializeSummaryTables(db *sql.DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS summary_logs (
			id SERIAL PRIMARY KEY,
			article_url TEXT NOT NULL,
			model TEXT NOT NULL,
			status TEXT NOT NULL,
			summary TEXT,
			error_message TEXT,
			duration_ms INTEGER NOT NULL,
			retry_attempt INTEGER NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL
		)`

	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to create summary_logs table: %w", err)
	}

	// Create indexes for better query performance
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_summary_logs_article_url ON summary_logs(article_url)`,
		`CREATE INDEX IF NOT EXISTS idx_summary_logs_status ON summary_logs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_summary_logs_created_at ON summary_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_summary_logs_model ON summary_logs(model)`,
	}

	for _, indexQuery := range indexes {
		if _, err := db.Exec(indexQuery); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// SummarizeArticleWithModel is a convenience function that wraps the main functionality
// Usage example:
//
//	summarizer := NewArticleSummarizer(db, "http://localhost:11434")
//	summary, err := summarizer.SummarizeArticleWithModel(ctx, articleText, articleURL, "llama2")
func (s *ArticleSummarizer) SummarizeArticleWithModel(ctx context.Context, articleText, articleURL, model string) (string, error) {
	return s.SummarizeArticle(ctx, articleText, articleURL, model)
}

// SummarizeWithOllama is a standalone function that takes raw article text as input
// and calls a local Ollama API endpoint using environment variables for configuration.
// It implements retry logic with exponential backoff and logs results to PostgreSQL.
func SummarizeWithOllama(ctx context.Context, articleText string, db *sql.DB) string {
	startTime := time.Now()

	// Get configuration from environment variables
	ollamaHost := getEnvWithDefault("OLLAMA_HOST", "http://ollama:11434")
	ollamaPort := getEnvWithDefault("OLLAMA_PORT", "")
	model := getEnvWithDefault("OLLAMA_MODEL", "llama3")

	// Construct full URL
	ollamaURL := ollamaHost
	if ollamaPort != "" {
		ollamaURL = fmt.Sprintf("http://%s:%s",
			strings.TrimPrefix(strings.TrimPrefix(ollamaHost, "http://"), "https://"),
			ollamaPort)
	}

	// Validate input
	if strings.TrimSpace(articleText) == "" {
		logSummarizeWithOllamaOperation(db, "", model, "failed", "summary unavailable", "empty article text", 0, time.Since(startTime))
		return "summary unavailable"
	}

	// Create summarization prompt with 200-word limit
	prompt := createStandaloneSummaryPrompt(articleText)

	// HTTP client with timeout
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	var lastErr error
	maxRetries := 3

	// Retry logic with exponential backoff
	for attempt := 1; attempt <= maxRetries; attempt++ {
		attemptStart := time.Now()

		summary, err := callOllamaAPIStandalone(ctx, client, ollamaURL, prompt, model)
		attemptDuration := time.Since(attemptStart)

		if err == nil {
			// Success - log and return
			logSummarizeWithOllamaOperation(db, "", model, "success", summary, "", attempt, attemptDuration)
			log.Printf("Successfully summarized article with model %s (attempt %d/%d)", model, attempt, maxRetries)
			return summary
		}

		lastErr = err

		// Log failed attempt
		logSummarizeWithOllamaOperation(db, "", model, "retry_failed", "summary unavailable", err.Error(), attempt, attemptDuration)
		log.Printf("Summary attempt %d/%d failed: %v", attempt, maxRetries, err)

		// Don't wait after the last attempt
		if attempt < maxRetries {
			// Exponential backoff: 1s, 2s, 4s
			backoffDuration := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second

			select {
			case <-ctx.Done():
				logSummarizeWithOllamaOperation(db, "", model, "failed", "summary unavailable", "context cancelled", attempt, time.Since(startTime))
				return "summary unavailable"
			case <-time.After(backoffDuration):
				// Continue to next attempt
			}
		}
	}

	// All retries failed
	logSummarizeWithOllamaOperation(db, "", model, "failed", "summary unavailable", lastErr.Error(), maxRetries, time.Since(startTime))
	log.Printf("Failed to summarize article after %d attempts: %s", maxRetries, lastErr.Error())
	return "summary unavailable"
}

// createStandaloneSummaryPrompt creates a prompt for article summarization with 100-word limit
func createStandaloneSummaryPrompt(articleText string) string {
	// Truncate article if it's too long (10000 chars max)
	maxChars := 10000
	if len(articleText) > maxChars {
		articleText = articleText[:maxChars] + "..."
	}

	return fmt.Sprintf(`Please provide a concise summary of the following article in exactly 100 words or less. The summary should be:
- Written in clear, simple language suitable for Discord posting
- Focused on the main points and key takeaways
- Objective and factual
- Complete sentences with proper grammar
- Easy to read and understand

Article text:
%s

Summary:`, articleText)
}

// callOllamaAPIStandalone makes the actual API call to Ollama with the specified payload format
func callOllamaAPIStandalone(ctx context.Context, client *http.Client, ollamaURL, prompt, model string) (string, error) {
	// Prepare request payload exactly as specified
	reqPayload := map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}

	jsonData, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with context
	apiURL := ollamaURL + "/api/generate"
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Information-Broker/1.0")

	// Make the API call
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var summaryResp SummaryResponse
	if err := json.Unmarshal(body, &summaryResp); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %w", err)
	}

	// Check for API errors
	if summaryResp.Error != "" {
		return "", fmt.Errorf("Ollama API error: %s", summaryResp.Error)
	}

	// Validate response
	summary := strings.TrimSpace(summaryResp.Response)
	if summary == "" {
		return "", fmt.Errorf("received empty summary from Ollama")
	}

	// Clean the summary by removing thinking tags and other unwanted content
	summary = cleanSummaryContent(summary)

	// Ensure summary is within 100 words for Discord posting
	words := strings.Fields(summary)
	if len(words) > 100 {
		summary = strings.Join(words[:100], " ") + "..."
	}

	return summary, nil
}

// logSummarizeWithOllamaOperation logs operations to PostgreSQL for the standalone function
func logSummarizeWithOllamaOperation(db *sql.DB, articleURL, model, status, summary, errorMessage string, retryAttempt int, duration time.Duration) {
	if db == nil {
		log.Printf("Database connection is nil, skipping log operation")
		return
	}

	query := `
		INSERT INTO summary_logs (
			article_url, model, status, summary, error_message,
			duration_ms, retry_attempt, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := db.Exec(query,
		articleURL,
		model,
		status,
		summary,
		errorMessage,
		duration.Milliseconds(),
		retryAttempt,
		time.Now(),
	)

	if err != nil {
		log.Printf("Failed to log SummarizeWithOllama operation to database: %v", err)
	}
}

// getEnvWithDefault gets an environment variable with a default fallback
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// cleanSummaryContent removes thinking tags and other unwanted content from AI model responses
func cleanSummaryContent(summary string) string {
	// Remove <think> </think> tags and their content (case-insensitive, handles newlines)
	thinkRegex := `(?is)<think\s*>.*?</think\s*>`
	summary = regexp.MustCompile(thinkRegex).ReplaceAllString(summary, "")

	// Remove any standalone thinking tags that might be left
	summary = regexp.MustCompile(`(?i)</?think\s*>`).ReplaceAllString(summary, "")

	// Remove other common AI reasoning patterns
	summary = regexp.MustCompile(`(?is)<thinking\s*>.*?</thinking\s*>`).ReplaceAllString(summary, "")
	summary = regexp.MustCompile(`(?i)</?thinking\s*>`).ReplaceAllString(summary, "")

	// Remove reasoning patterns with different formats
	summary = regexp.MustCompile(`(?is)<reason\s*>.*?</reason\s*>`).ReplaceAllString(summary, "")
	summary = regexp.MustCompile(`(?i)</?reason\s*>`).ReplaceAllString(summary, "")

	// Remove analysis patterns
	summary = regexp.MustCompile(`(?is)<analysis\s*>.*?</analysis\s*>`).ReplaceAllString(summary, "")
	summary = regexp.MustCompile(`(?i)</?analysis\s*>`).ReplaceAllString(summary, "")

	// Clean up multiple whitespace and newlines
	summary = regexp.MustCompile(`\s+`).ReplaceAllString(summary, " ")

	// Trim any remaining whitespace
	summary = strings.TrimSpace(summary)

	// If the summary is empty after cleaning, return a fallback message
	if summary == "" {
		return "Summary content was filtered out - please check model configuration"
	}

	return summary
}
