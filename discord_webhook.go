package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

// DiscordEmbed represents a Discord embed structure
type DiscordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	URL         string              `json:"url,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
	Footer      *DiscordEmbedFooter `json:"footer,omitempty"`
	Author      *DiscordEmbedAuthor `json:"author,omitempty"`
	Fields      []DiscordEmbedField `json:"fields,omitempty"`
}

// DiscordEmbedFooter represents the footer of a Discord embed
type DiscordEmbedFooter struct {
	Text    string `json:"text,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

// DiscordEmbedAuthor represents the author of a Discord embed
type DiscordEmbedAuthor struct {
	Name    string `json:"name,omitempty"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

// DiscordEmbedField represents a field in a Discord embed
type DiscordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// DiscordWebhookMessage represents the complete webhook message
type DiscordWebhookMessage struct {
	Content   string         `json:"content,omitempty"`
	Username  string         `json:"username,omitempty"`
	AvatarURL string         `json:"avatar_url,omitempty"`
	Embeds    []DiscordEmbed `json:"embeds,omitempty"`
}

// ArticleMessage represents an article to be sent to Discord
type ArticleMessage struct {
	Title       string
	URL         string
	Summary     string
	PublishDate time.Time
	FeedTitle   string
}

// DiscordWebhookSender handles sending messages to Discord webhooks
type DiscordWebhookSender struct {
	db         *sql.DB
	httpClient *http.Client
	maxRetries int
	metrics    *PrometheusMetrics
}

// DiscordErrorLog represents logging structure for Discord webhook errors
type DiscordErrorLog struct {
	WebhookURL   string        `json:"webhook_url"`
	ArticleURL   string        `json:"article_url"`
	ErrorMessage string        `json:"error_message"`
	StatusCode   int           `json:"status_code"`
	RetryAttempt int           `json:"retry_attempt"`
	Duration     time.Duration `json:"duration"`
	CreatedAt    time.Time     `json:"created_at"`
}

// NewDiscordWebhookSender creates a new Discord webhook sender instance
func NewDiscordWebhookSender(db *sql.DB, metrics *PrometheusMetrics) *DiscordWebhookSender {
	return &DiscordWebhookSender{
		db: db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxRetries: 2, // Retry twice as specified
		metrics:    metrics,
	}
}

// SendArticleToDiscord sends a formatted article message to Discord webhook with embeds
func (d *DiscordWebhookSender) SendArticleToDiscord(ctx context.Context, webhookURL string, article ArticleMessage) error {
	startTime := time.Now()

	// Validate inputs
	if strings.TrimSpace(webhookURL) == "" {
		return fmt.Errorf("webhook URL cannot be empty")
	}

	if strings.TrimSpace(article.Title) == "" {
		return fmt.Errorf("article title cannot be empty")
	}

	if strings.TrimSpace(article.URL) == "" {
		return fmt.Errorf("article URL cannot be empty")
	}

	// Create the Discord message with embed
	message := d.createDiscordMessage(article)

	var lastErr error

	// Retry logic - retry twice if Discord returns an error
	for attempt := 1; attempt <= d.maxRetries+1; attempt++ { // +1 for initial attempt
		attemptStart := time.Now()

		err := d.sendWebhookMessage(ctx, webhookURL, message)
		attemptDuration := time.Since(attemptStart)

		if err == nil {
			// Success - record metrics
			d.metrics.RecordDiscordWebhook("success", attemptDuration)
			log.Printf("Successfully sent article to Discord: %s (attempt %d)", article.Title, attempt)
			return nil
		}

		lastErr = err

		// Record failed attempt metrics
		d.metrics.RecordDiscordWebhook("error", attemptDuration)

		// Determine error type for metrics
		errorType := "unknown"
		if discordErr, ok := err.(*DiscordAPIError); ok {
			if discordErr.StatusCode >= 400 && discordErr.StatusCode < 500 {
				errorType = "client_error"
			} else if discordErr.StatusCode >= 500 {
				errorType = "server_error"
			}
		} else {
			errorType = "network_error"
		}
		d.metrics.RecordDiscordWebhookError(errorType)

		// Log the error to PostgreSQL
		d.logDiscordError(DiscordErrorLog{
			WebhookURL:   d.sanitizeWebhookURL(webhookURL),
			ArticleURL:   article.URL,
			ErrorMessage: err.Error(),
			StatusCode:   d.extractStatusCode(err),
			RetryAttempt: attempt,
			Duration:     attemptDuration,
			CreatedAt:    time.Now(),
		})

		log.Printf("Discord webhook attempt %d failed for article %s: %v", attempt, article.Title, err)

		// Don't wait after the last attempt
		if attempt <= d.maxRetries {
			// Exponential backoff: 1s, 2s, 4s
			backoffDuration := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second

			select {
			case <-ctx.Done():
				d.metrics.RecordDiscordWebhookError("context_cancelled")
				return fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			case <-time.After(backoffDuration):
				// Continue to next attempt
			}
		}
	}

	// All attempts failed
	totalDuration := time.Since(startTime)
	d.metrics.RecordDiscordWebhookError("max_retries_exceeded")
	log.Printf("Failed to send article to Discord after %d attempts (took %v): %s",
		d.maxRetries+1, totalDuration, article.Title)

	return fmt.Errorf("failed to send to Discord after %d attempts: %w", d.maxRetries+1, lastErr)
}

// createDiscordMessage creates a properly formatted Discord message with embed
func (d *DiscordWebhookSender) createDiscordMessage(article ArticleMessage) DiscordWebhookMessage {
	// Truncate title to Discord's 256 character limit
	title := d.truncateString(article.Title, 256)

	// Ensure the entire message content stays within Discord's 2000 character limit
	// Reserve space for title, formatting, timestamp, and other embed elements
	maxSummaryLength := 300 // Conservative limit to ensure total message < 2000 chars
	summary := d.truncateString(article.Summary, maxSummaryLength)

	// Format timestamp to ISO 8601 format
	timestamp := article.PublishDate.Format(time.RFC3339)

	// Create embed
	embed := DiscordEmbed{
		Title:       title,
		URL:         article.URL,
		Description: summary,
		Color:       0x5865F2, // Discord's blurple color
		Timestamp:   timestamp,
		Footer: &DiscordEmbedFooter{
			Text: "Information Broker",
		},
	}

	// Add feed title as author if available
	if strings.TrimSpace(article.FeedTitle) != "" {
		embed.Author = &DiscordEmbedAuthor{
			Name: d.truncateString(article.FeedTitle, 256),
		}
	}

	// Create the webhook message
	message := DiscordWebhookMessage{
		Username:  "Information Broker",
		AvatarURL: "https://vignette.wikia.nocookie.net/es.starwars/images/e/e5/Information_broker_TotG.jpg", // Default Discord avatar
		Embeds:    []DiscordEmbed{embed},
	}

	return message
}

// sendWebhookMessage sends the actual HTTP request to Discord
func (d *DiscordWebhookSender) sendWebhookMessage(ctx context.Context, webhookURL string, message DiscordWebhookMessage) error {
	// Marshal the message to JSON
	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal Discord message: %w", err)
	}

	// Verify total message size doesn't exceed Discord's limits
	if len(jsonData) > 2000 {
		return fmt.Errorf("message too large: %d characters (Discord limit: 2000)", len(jsonData))
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Information-Broker-Discord/1.0")

	// Send the request
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for error details
	body, _ := io.ReadAll(resp.Body)

	// Check for Discord API errors
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &DiscordAPIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	return nil
}

// DiscordAPIError represents an error from Discord's API
type DiscordAPIError struct {
	StatusCode int
	Message    string
}

func (e *DiscordAPIError) Error() string {
	return fmt.Sprintf("Discord API error (status %d): %s", e.StatusCode, e.Message)
}

// truncateString safely truncates a string to the specified length
func (d *DiscordWebhookSender) truncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}

	// Try to truncate at word boundary if possible
	if maxLength > 3 {
		truncated := s[:maxLength-3]
		lastSpace := strings.LastIndex(truncated, " ")
		if lastSpace > maxLength/2 { // Only use word boundary if it's not too short
			return truncated[:lastSpace] + "..."
		}
	}

	return s[:maxLength-3] + "..."
}

// extractStatusCode extracts HTTP status code from error if it's a DiscordAPIError
func (d *DiscordWebhookSender) extractStatusCode(err error) int {
	if discordErr, ok := err.(*DiscordAPIError); ok {
		return discordErr.StatusCode
	}
	return 0
}

// sanitizeWebhookURL removes sensitive parts of webhook URL for logging
func (d *DiscordWebhookSender) sanitizeWebhookURL(webhookURL string) string {
	// Replace the token part with asterisks for security
	parts := strings.Split(webhookURL, "/")
	if len(parts) >= 7 { // Discord webhook URLs have at least 7 parts
		// Replace the last part (token) with hidden placeholder
		parts[len(parts)-1] = "***TOKEN_HIDDEN***"
	}
	return strings.Join(parts, "/")
}

// logDiscordError logs Discord webhook errors to PostgreSQL
func (d *DiscordWebhookSender) logDiscordError(errorLog DiscordErrorLog) {
	query := `
		INSERT INTO discord_error_logs (
			webhook_url, article_url, error_message, status_code,
			retry_attempt, duration_ms, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := d.db.Exec(query,
		errorLog.WebhookURL,
		errorLog.ArticleURL,
		errorLog.ErrorMessage,
		errorLog.StatusCode,
		errorLog.RetryAttempt,
		errorLog.Duration.Milliseconds(),
		errorLog.CreatedAt,
	)

	if err != nil {
		log.Printf("Failed to log Discord error to database: %v", err)
	}
}

// InitializeDiscordTables creates the necessary database tables for Discord error logging
func InitializeDiscordTables(db *sql.DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS discord_error_logs (
			id SERIAL PRIMARY KEY,
			webhook_url TEXT NOT NULL,
			article_url TEXT NOT NULL,
			error_message TEXT NOT NULL,
			status_code INTEGER,
			retry_attempt INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL
		)`

	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to create discord_error_logs table: %w", err)
	}

	// Create indexes for better query performance
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_discord_error_logs_webhook_url ON discord_error_logs(webhook_url)`,
		`CREATE INDEX IF NOT EXISTS idx_discord_error_logs_article_url ON discord_error_logs(article_url)`,
		`CREATE INDEX IF NOT EXISTS idx_discord_error_logs_created_at ON discord_error_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_discord_error_logs_status_code ON discord_error_logs(status_code)`,
	}

	for _, indexQuery := range indexes {
		if _, err := db.Exec(indexQuery); err != nil {
			return fmt.Errorf("failed to create Discord error log index: %w", err)
		}
	}

	return nil
}

// SendArticleWithRetry is a convenience function that sends an article to Discord with proper retry logic
// Usage example:
//
//	sender := NewDiscordWebhookSender(db)
//	article := ArticleMessage{
//		Title:       "Breaking News: Important Update",
//		URL:         "https://example.com/article",
//		Summary:     "This is a summary of the important news article...",
//		PublishDate: time.Now(),
//		FeedTitle:   "Example News Feed",
//	}
//	err := sender.SendArticleWithRetry(ctx, "https://discord.com/api/webhooks/...", article)
func (d *DiscordWebhookSender) SendArticleWithRetry(ctx context.Context, webhookURL string, article ArticleMessage) error {
	return d.SendArticleToDiscord(ctx, webhookURL, article)
}
