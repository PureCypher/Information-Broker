package main

import (
	"context"
	"database/sql"
	"fmt"
	"information-broker/config"
	"log"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

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
			Timeout: cfg.LLM.Timeout,
		},
		config:  cfg,
		metrics: metrics,
	}
}

// SummarizeArticle generates a concise summary of the article text using llama.cpp
// It handles retries with exponential backoff and logs all operations to PostgreSQL
func (s *ArticleSummarizer) SummarizeArticle(ctx context.Context, articleText, articleURL, model string) (string, error) {
	startTime := time.Now()

	// Validate inputs
	if strings.TrimSpace(articleText) == "" {
		return s.handleSummaryFailure(articleURL, model, "empty article text", 0, startTime)
	}

	if strings.TrimSpace(model) == "" {
		model = s.config.LLM.Model // Use configured default model
	}

	// Create the prompt for summarization
	prompt := s.createSummaryPrompt(articleText)

	var lastErr error

	// Retry logic with exponential backoff
	for attempt := 1; attempt <= s.config.LLM.MaxRetries; attempt++ {
		attemptStart := time.Now()

		summary, err := s.callLlamaAPI(ctx, prompt)
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
				articleURL, model, attempt, s.config.LLM.MaxRetries)
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

		log.Printf("Summary attempt %d/%d failed for %s: %v", attempt, s.config.LLM.MaxRetries, articleURL, err)

		// Don't wait after the last attempt
		if attempt < s.config.LLM.MaxRetries {
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
	return s.handleSummaryFailure(articleURL, model, lastErr.Error(), s.config.LLM.MaxRetries, startTime)
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

// callLlamaAPI asks the llama.cpp server for a summary of the prompt and tidies the result.
func (s *ArticleSummarizer) callLlamaAPI(ctx context.Context, prompt string) (string, error) {
	summary, err := llamaChat(ctx, s.httpClient, s.config.LLM.URL, s.config.API.UserAgent, prompt, s.config.LLM.MaxTokens)
	if err != nil {
		return "", err
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
