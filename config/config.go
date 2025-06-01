package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration
type Config struct {
	Database      DatabaseConfig
	App           AppConfig
	API           APIConfig
	OLLAMA        OLLAMAConfig
	Discord       DiscordConfig
	Prometheus    PrometheusConfig
	Security      SecurityConfig
	Performance   PerformanceConfig
	Content       ContentConfig
	Summarization SummarizationConfig
}

// DatabaseConfig holds database-related configuration
type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
}

// AppConfig holds general application configuration
type AppConfig struct {
	Port              int
	RSSFetchInterval  time.Duration
	RSSFeedsFile      string
	LogLevel          string
	InitiationDate    time.Time
	ArticleCutoffDate time.Time
}

// APIConfig holds API-related configuration
type APIConfig struct {
	Timeout   time.Duration
	UserAgent string
}

// OLLAMAConfig holds OLLAMA AI service configuration
type OLLAMAConfig struct {
	URL        string
	Model      string
	Timeout    time.Duration
	MaxRetries int
}

// DiscordConfig holds Discord webhook configuration
type DiscordConfig struct {
	WebhookURL  string   // Deprecated: Use WebhookURLs for multiple webhooks
	WebhookURLs []string // Multiple webhook URLs for multi-cast notifications
	MaxRetries  int
	Timeout     time.Duration
}

// PrometheusConfig holds Prometheus metrics configuration
type PrometheusConfig struct {
	MetricsPath string
}

// SecurityConfig holds security-related configuration
type SecurityConfig struct {
	CORSAllowedOrigins string
	CORSAllowedMethods string
	CORSAllowedHeaders string
}

// PerformanceConfig holds performance-related configuration
type PerformanceConfig struct {
	MaxConcurrentFeeds      int
	MaxArticleContentLength int
	HTTPReadTimeout         time.Duration
	HTTPWriteTimeout        time.Duration
	HTTPIdleTimeout         time.Duration
}

// ContentConfig holds content processing configuration
type ContentConfig struct {
	MaxSummaryLength     int
	ContentHashAlgorithm string
}

// SummarizationConfig holds summarization scheduler configuration
type SummarizationConfig struct {
	MaxQueueSize      int
	WorkerTimeout     time.Duration
	MaxRetries        int
	RetryBackoffBase  time.Duration
	MetricsInterval   time.Duration
	QueuePurgeTimeout time.Duration
}

// Load loads configuration from environment variables
func Load() *Config {
	return &Config{
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			Name:     getEnv("DB_NAME", "information_broker"),
		},
		App: AppConfig{
			Port:              getEnvInt("APP_PORT", 8080),
			RSSFetchInterval:  getEnvDuration("RSS_FETCH_INTERVAL", 5*time.Minute),
			RSSFeedsFile:      getEnv("RSS_FEEDS_FILE", "/app/feeds.txt"),
			LogLevel:          getEnv("LOG_LEVEL", "info"),
			InitiationDate:    getEnvTime("APP_INITIATION_DATE", time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC)),
			ArticleCutoffDate: getEnvTime("ARTICLE_CUTOFF_DATE", time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC)),
		},
		API: APIConfig{
			Timeout:   getEnvDuration("API_TIMEOUT", 30*time.Second),
			UserAgent: getEnv("API_USER_AGENT", "Information-Broker/1.0"),
		},
		OLLAMA: OLLAMAConfig{
			URL:        getEnv("OLLAMA_URL", "http://localhost:11434"),
			Model:      getEnv("OLLAMA_MODEL", "llama2"),
			Timeout:    getEnvDuration("OLLAMA_TIMEOUT", 60*time.Second),
			MaxRetries: getEnvInt("OLLAMA_MAX_RETRIES", 3),
		},
		Discord: DiscordConfig{
			WebhookURL:  getEnv("DISCORD_WEBHOOK_URL", ""),
			WebhookURLs: getEnvStringSlice("DISCORD_WEBHOOK_URLS", []string{}),
			MaxRetries:  getEnvInt("DISCORD_MAX_RETRIES", 2),
			Timeout:     getEnvDuration("DISCORD_TIMEOUT", 30*time.Second),
		},
		Prometheus: PrometheusConfig{
			MetricsPath: getEnv("PROMETHEUS_METRICS_PATH", "/metrics"),
		},
		Security: SecurityConfig{
			CORSAllowedOrigins: getEnv("CORS_ALLOWED_ORIGINS", "*"),
			CORSAllowedMethods: getEnv("CORS_ALLOWED_METHODS", "GET,POST,PUT,DELETE,OPTIONS"),
			CORSAllowedHeaders: getEnv("CORS_ALLOWED_HEADERS", "Content-Type,Authorization"),
		},
		Performance: PerformanceConfig{
			MaxConcurrentFeeds:      getEnvInt("MAX_CONCURRENT_FEEDS", 10),
			MaxArticleContentLength: getEnvInt("MAX_ARTICLE_CONTENT_LENGTH", 10000),
			HTTPReadTimeout:         getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
			HTTPWriteTimeout:        getEnvDuration("HTTP_WRITE_TIMEOUT", 15*time.Second),
			HTTPIdleTimeout:         getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		},
		Content: ContentConfig{
			MaxSummaryLength:     getEnvInt("MAX_SUMMARY_LENGTH", 200),
			ContentHashAlgorithm: getEnv("CONTENT_HASH_ALGORITHM", "sha256"),
		},
		Summarization: SummarizationConfig{
			MaxQueueSize:      getEnvInt("SUMMARIZATION_MAX_QUEUE_SIZE", 100),
			WorkerTimeout:     getEnvDuration("SUMMARIZATION_WORKER_TIMEOUT", 120*time.Second),
			MaxRetries:        getEnvInt("SUMMARIZATION_MAX_RETRIES", 3),
			RetryBackoffBase:  getEnvDuration("SUMMARIZATION_RETRY_BACKOFF_BASE", 1*time.Second),
			MetricsInterval:   getEnvDuration("SUMMARIZATION_METRICS_INTERVAL", 10*time.Second),
			QueuePurgeTimeout: getEnvDuration("SUMMARIZATION_QUEUE_PURGE_TIMEOUT", 1*time.Hour),
		},
	}
}

// Helper functions for environment variable parsing
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvStringSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		// Split by comma and trim whitespace
		parts := strings.Split(value, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return defaultValue
}

func getEnvTime(key string, defaultValue time.Time) time.Time {
	if value := os.Getenv(key); value != "" {
		// Try parsing in RFC3339 format first (2006-01-02T15:04:05Z07:00)
		if parsedTime, err := time.Parse(time.RFC3339, value); err == nil {
			return parsedTime
		}
		// Try parsing in date-only format (2006-01-02)
		if parsedTime, err := time.Parse("2006-01-02", value); err == nil {
			return parsedTime
		}
		// Try parsing in date-time format without timezone (2006-01-02 15:04:05)
		if parsedTime, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
			return parsedTime
		}
	}
	return defaultValue
}

// GetWebhookURLs returns all configured webhook URLs, supporting both single and multiple webhook configurations
func (d *DiscordConfig) GetWebhookURLs() []string {
	// If multiple webhooks are configured, use them
	if len(d.WebhookURLs) > 0 {
		return d.WebhookURLs
	}

	// Fall back to single webhook URL for backward compatibility
	if d.WebhookURL != "" {
		return []string{d.WebhookURL}
	}

	// No webhooks configured
	return []string{}
}

// GetConnectionString returns the database connection string
func (c *Config) GetConnectionString() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		c.Database.Host, c.Database.Port, c.Database.User, c.Database.Password, c.Database.Name)
}
