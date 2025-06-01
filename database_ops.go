package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// DatabaseArticle represents an article as stored in the enhanced database schema
// This is separate from the existing Article struct to avoid conflicts
type DatabaseArticle struct {
	ID              int64      `json:"id"`
	Title           string     `json:"title"`
	URL             string     `json:"url"`
	PublishDate     *time.Time `json:"publish_date,omitempty"`
	Summary         *string    `json:"summary,omitempty"`
	FullContent     *string    `json:"full_content,omitempty"`
	FetchTime       time.Time  `json:"fetch_time"`
	PostedToDiscord bool       `json:"posted_to_discord"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FeedURL         *string    `json:"feed_url,omitempty"`
	ContentHash     *string    `json:"content_hash,omitempty"`
	FetchDurationMs *int       `json:"fetch_duration_ms,omitempty"`
}

// WebhookLog represents a webhook attempt log in the database
type WebhookLog struct {
	ID           int64     `json:"id"`
	ArticleID    int64     `json:"article_id"`
	Attempt      int       `json:"attempt"`
	ResponseCode *int      `json:"response_code,omitempty"`
	ResponseBody *string   `json:"response_body,omitempty"`
	LatencyMs    *int      `json:"latency_ms,omitempty"`
	ErrorMessage *string   `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// DatabaseOperations provides high-performance database operations
type DatabaseOperations struct {
	db *sql.DB
}

// NewDatabaseOperations creates a new database operations instance
func NewDatabaseOperations(db *sql.DB) *DatabaseOperations {
	return &DatabaseOperations{db: db}
}

// ConvertArticleToDatabase converts the existing Article struct to DatabaseArticle
func ConvertArticleToDatabase(article Article) *DatabaseArticle {
	dbArticle := &DatabaseArticle{
		Title:     article.Title,
		URL:       article.URL,
		FetchTime: time.Now(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Convert PublishedAt to PublishDate
	if !article.PublishedAt.IsZero() {
		dbArticle.PublishDate = &article.PublishedAt
	}

	// Convert Content to FullContent
	if article.Content != "" {
		content := article.Content
		dbArticle.FullContent = &content
	}

	// Convert FeedURL
	if article.FeedURL != "" {
		feedURL := article.FeedURL
		dbArticle.FeedURL = &feedURL
	}

	// Convert ContentHash
	if article.ContentHash != "" {
		contentHash := article.ContentHash
		dbArticle.ContentHash = &contentHash
	}

	// Convert FetchDuration
	if article.FetchDuration > 0 {
		ms := int(article.FetchDuration.Milliseconds())
		dbArticle.FetchDurationMs = &ms
	}

	return dbArticle
}

// UpsertArticle performs an atomic upsert operation on the articles table
// This function is idempotent - calling it multiple times with the same data won't create duplicates
func (ops *DatabaseOperations) UpsertArticle(article *DatabaseArticle) (*DatabaseArticle, error) {
	tx, err := ops.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO articles (
			title, url, publish_date, summary, full_content, 
			fetch_time, posted_to_discord, feed_url, content_hash, fetch_duration_ms
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
		ON CONFLICT (url) DO UPDATE SET
			title = EXCLUDED.title,
			publish_date = COALESCE(EXCLUDED.publish_date, articles.publish_date),
			summary = COALESCE(EXCLUDED.summary, articles.summary),
			full_content = COALESCE(EXCLUDED.full_content, articles.full_content),
			fetch_time = EXCLUDED.fetch_time,
			posted_to_discord = EXCLUDED.posted_to_discord,
			feed_url = COALESCE(EXCLUDED.feed_url, articles.feed_url),
			content_hash = COALESCE(EXCLUDED.content_hash, articles.content_hash),
			fetch_duration_ms = COALESCE(EXCLUDED.fetch_duration_ms, articles.fetch_duration_ms),
			updated_at = NOW()
		RETURNING id, title, url, publish_date, summary, full_content, 
				  fetch_time, posted_to_discord, created_at, updated_at,
				  feed_url, content_hash, fetch_duration_ms`

	var result DatabaseArticle
	err = tx.QueryRow(
		query,
		article.Title,
		article.URL,
		article.PublishDate,
		article.Summary,
		article.FullContent,
		article.FetchTime,
		article.PostedToDiscord,
		article.FeedURL,
		article.ContentHash,
		article.FetchDurationMs,
	).Scan(
		&result.ID,
		&result.Title,
		&result.URL,
		&result.PublishDate,
		&result.Summary,
		&result.FullContent,
		&result.FetchTime,
		&result.PostedToDiscord,
		&result.CreatedAt,
		&result.UpdatedAt,
		&result.FeedURL,
		&result.ContentHash,
		&result.FetchDurationMs,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to upsert article: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &result, nil
}

// UpsertArticleFromExisting converts and upserts an existing Article struct
func (ops *DatabaseOperations) UpsertArticleFromExisting(article Article) (*DatabaseArticle, error) {
	dbArticle := ConvertArticleToDatabase(article)
	return ops.UpsertArticle(dbArticle)
}

// BatchUpsertArticles performs atomic batch upsert of multiple articles
// Highly performant for bulk operations, all inserts are atomic
func (ops *DatabaseOperations) BatchUpsertArticles(articles []*DatabaseArticle) ([]*DatabaseArticle, error) {
	if len(articles) == 0 {
		return []*DatabaseArticle{}, nil
	}

	tx, err := ops.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare the statement for better performance
	stmt, err := tx.Prepare(`
		INSERT INTO articles (
			title, url, publish_date, summary, full_content, 
			fetch_time, posted_to_discord, feed_url, content_hash, fetch_duration_ms
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
		ON CONFLICT (url) DO UPDATE SET
			title = EXCLUDED.title,
			publish_date = COALESCE(EXCLUDED.publish_date, articles.publish_date),
			summary = COALESCE(EXCLUDED.summary, articles.summary),
			full_content = COALESCE(EXCLUDED.full_content, articles.full_content),
			fetch_time = EXCLUDED.fetch_time,
			posted_to_discord = EXCLUDED.posted_to_discord,
			feed_url = COALESCE(EXCLUDED.feed_url, articles.feed_url),
			content_hash = COALESCE(EXCLUDED.content_hash, articles.content_hash),
			fetch_duration_ms = COALESCE(EXCLUDED.fetch_duration_ms, articles.fetch_duration_ms),
			updated_at = NOW()
		RETURNING id, title, url, publish_date, summary, full_content, 
				  fetch_time, posted_to_discord, created_at, updated_at,
				  feed_url, content_hash, fetch_duration_ms`)

	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	results := make([]*DatabaseArticle, len(articles))

	for i, article := range articles {
		var result DatabaseArticle
		err = stmt.QueryRow(
			article.Title,
			article.URL,
			article.PublishDate,
			article.Summary,
			article.FullContent,
			article.FetchTime,
			article.PostedToDiscord,
			article.FeedURL,
			article.ContentHash,
			article.FetchDurationMs,
		).Scan(
			&result.ID,
			&result.Title,
			&result.URL,
			&result.PublishDate,
			&result.Summary,
			&result.FullContent,
			&result.FetchTime,
			&result.PostedToDiscord,
			&result.CreatedAt,
			&result.UpdatedAt,
			&result.FeedURL,
			&result.ContentHash,
			&result.FetchDurationMs,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to upsert article %d: %w", i, err)
		}

		results[i] = &result
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return results, nil
}

// UpdateArticleDiscordStatus atomically updates the posted_to_discord status
func (ops *DatabaseOperations) UpdateArticleDiscordStatus(articleID int64, posted bool) error {
	query := `UPDATE articles SET posted_to_discord = $1, updated_at = NOW() WHERE id = $2`

	result, err := ops.db.Exec(query, posted, articleID)
	if err != nil {
		return fmt.Errorf("failed to update discord status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("article with ID %d not found", articleID)
	}

	return nil
}

// UpdateArticleDiscordStatusByURL atomically updates the posted_to_discord status by URL
func (ops *DatabaseOperations) UpdateArticleDiscordStatusByURL(url string, posted bool) error {
	query := `UPDATE articles SET posted_to_discord = $1, updated_at = NOW() WHERE url = $2`

	result, err := ops.db.Exec(query, posted, url)
	if err != nil {
		return fmt.Errorf("failed to update discord status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("article with URL %s not found", url)
	}

	return nil
}

// InsertWebhookLog inserts a new webhook log entry atomically
func (ops *DatabaseOperations) InsertWebhookLog(log *WebhookLog) (*WebhookLog, error) {
	tx, err := ops.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO webhook_logs (
			article_id, attempt, response_code, response_body, latency_ms, error_message
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)
		RETURNING id, article_id, attempt, response_code, response_body, 
				  latency_ms, error_message, created_at`

	var result WebhookLog
	err = tx.QueryRow(
		query,
		log.ArticleID,
		log.Attempt,
		log.ResponseCode,
		log.ResponseBody,
		log.LatencyMs,
		log.ErrorMessage,
	).Scan(
		&result.ID,
		&result.ArticleID,
		&result.Attempt,
		&result.ResponseCode,
		&result.ResponseBody,
		&result.LatencyMs,
		&result.ErrorMessage,
		&result.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to insert webhook log: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &result, nil
}

// LogWebhookAttempt creates a webhook log for an article by URL
func (ops *DatabaseOperations) LogWebhookAttempt(url string, responseCode *int, responseBody *string, latencyMs *int, errorMessage *string) error {
	// First get the article ID and next attempt number
	var articleID int64
	var nextAttempt int

	tx, err := ops.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Get article ID
	err = tx.QueryRow("SELECT id FROM articles WHERE url = $1", url).Scan(&articleID)
	if err != nil {
		return fmt.Errorf("failed to find article with URL %s: %w", url, err)
	}

	// Get next attempt number
	err = tx.QueryRow(`
		SELECT COALESCE(MAX(attempt), 0) + 1 
		FROM webhook_logs 
		WHERE article_id = $1`, articleID).Scan(&nextAttempt)
	if err != nil {
		return fmt.Errorf("failed to get next attempt number: %w", err)
	}

	// Insert webhook log
	_, err = tx.Exec(`
		INSERT INTO webhook_logs (article_id, attempt, response_code, response_body, latency_ms, error_message)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		articleID, nextAttempt, responseCode, responseBody, latencyMs, errorMessage)
	if err != nil {
		return fmt.Errorf("failed to insert webhook log: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetNextWebhookAttempt atomically gets the next attempt number for an article
func (ops *DatabaseOperations) GetNextWebhookAttempt(articleID int64) (int, error) {
	query := `
		SELECT COALESCE(MAX(attempt), 0) + 1 
		FROM webhook_logs 
		WHERE article_id = $1`

	var nextAttempt int
	err := ops.db.QueryRow(query, articleID).Scan(&nextAttempt)
	if err != nil {
		return 0, fmt.Errorf("failed to get next attempt number: %w", err)
	}

	return nextAttempt, nil
}

// GetArticlesByDiscordStatus gets articles by their Discord posting status with pagination
func (ops *DatabaseOperations) GetArticlesByDiscordStatus(posted bool, limit, offset int) ([]*DatabaseArticle, error) {
	query := `
		SELECT id, title, url, publish_date, summary, full_content, 
			   fetch_time, posted_to_discord, created_at, updated_at,
			   feed_url, content_hash, fetch_duration_ms
		FROM articles 
		WHERE posted_to_discord = $1 
		ORDER BY fetch_time DESC 
		LIMIT $2 OFFSET $3`

	rows, err := ops.db.Query(query, posted, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query articles: %w", err)
	}
	defer rows.Close()

	var articles []*DatabaseArticle
	for rows.Next() {
		var article DatabaseArticle
		err := rows.Scan(
			&article.ID,
			&article.Title,
			&article.URL,
			&article.PublishDate,
			&article.Summary,
			&article.FullContent,
			&article.FetchTime,
			&article.PostedToDiscord,
			&article.CreatedAt,
			&article.UpdatedAt,
			&article.FeedURL,
			&article.ContentHash,
			&article.FetchDurationMs,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan article: %w", err)
		}
		articles = append(articles, &article)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return articles, nil
}

// GetWebhookLogsByArticle gets all webhook logs for a specific article
func (ops *DatabaseOperations) GetWebhookLogsByArticle(articleID int64) ([]*WebhookLog, error) {
	query := `
		SELECT id, article_id, attempt, response_code, response_body, 
			   latency_ms, error_message, created_at
		FROM webhook_logs 
		WHERE article_id = $1 
		ORDER BY attempt DESC`

	rows, err := ops.db.Query(query, articleID)
	if err != nil {
		return nil, fmt.Errorf("failed to query webhook logs: %w", err)
	}
	defer rows.Close()

	var logs []*WebhookLog
	for rows.Next() {
		var log WebhookLog
		err := rows.Scan(
			&log.ID,
			&log.ArticleID,
			&log.Attempt,
			&log.ResponseCode,
			&log.ResponseBody,
			&log.LatencyMs,
			&log.ErrorMessage,
			&log.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan webhook log: %w", err)
		}
		logs = append(logs, &log)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return logs, nil
}

// GetArticleByURL gets an article by its URL
func (ops *DatabaseOperations) GetArticleByURL(url string) (*DatabaseArticle, error) {
	query := `
		SELECT id, title, url, publish_date, summary, full_content, 
			   fetch_time, posted_to_discord, created_at, updated_at,
			   feed_url, content_hash, fetch_duration_ms
		FROM articles 
		WHERE url = $1`

	var article DatabaseArticle
	err := ops.db.QueryRow(query, url).Scan(
		&article.ID,
		&article.Title,
		&article.URL,
		&article.PublishDate,
		&article.Summary,
		&article.FullContent,
		&article.FetchTime,
		&article.PostedToDiscord,
		&article.CreatedAt,
		&article.UpdatedAt,
		&article.FeedURL,
		&article.ContentHash,
		&article.FetchDurationMs,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("article with URL %s not found", url)
		}
		return nil, fmt.Errorf("failed to get article: %w", err)
	}

	return &article, nil
}

// GetArticleCount returns the total number of articles in the database
func (ops *DatabaseOperations) GetArticleCount() (int64, error) {
	query := `SELECT COUNT(*) FROM articles`

	var count int64
	err := ops.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get article count: %w", err)
	}

	return count, nil
}
