package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"information-broker/config"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Setup logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Information Broker RSS Monitor")

	// Initialize Prometheus metrics
	metrics := NewPrometheusMetrics()
	log.Println("Prometheus metrics initialized")

	// Initialize database
	db, err := initDatabase(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Create database operations instance for metrics
	dbOps := NewDatabaseOperations(db)

	// Load RSS feeds
	feeds, err := loadFeeds(cfg.App.RSSFeedsFile)
	if err != nil {
		log.Fatalf("Failed to load feeds: %v", err)
	}
	log.Printf("Loaded %d RSS feeds", len(feeds))

	// Create circuit breaker manager
	circuitBreakers := NewCircuitBreakerManager()
	circuitBreakers.SetMetrics(metrics)

	// Create summarization scheduler
	summarizationScheduler := NewSummarizationScheduler(db, cfg, metrics)

	// Create monitor with metrics and circuit breakers
	monitor := NewRSSMonitor(db, feeds, metrics, cfg, circuitBreakers, summarizationScheduler)

	// Create API server with metrics and circuit breakers
	apiServer := NewAPIServer(db, cfg.App.Port, metrics, cfg, circuitBreakers, summarizationScheduler)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start monitoring in goroutine
	var wg sync.WaitGroup
	wg.Add(3)

	// Start summarization scheduler
	go func() {
		defer wg.Done()
		if err := summarizationScheduler.Start(ctx); err != nil {
			log.Printf("Failed to start summarization scheduler: %v", err)
		}
	}()

	// Start RSS monitor
	go func() {
		defer wg.Done()
		monitor.Start(ctx)
	}()

	// Start API server
	go func() {
		defer wg.Done()
		apiServer.Start()
	}()

	// Start database metrics updater
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := db.Stats()
				metrics.UpdateDBConnections(stats.OpenConnections, stats.InUse, stats.Idle)
			}
		}
	}()

	// Start article count metrics updater
	go func() {
		ticker := time.NewTicker(5 * time.Minute) // Update every 5 minutes
		defer ticker.Stop()

		// Initial update
		if count, err := dbOps.GetArticleCount(); err != nil {
			log.Printf("Error getting initial article count: %v", err)
		} else {
			metrics.UpdateArticlesInDatabase(count)
			log.Printf("Initial article count in database: %d", count)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if count, err := dbOps.GetArticleCount(); err != nil {
					log.Printf("Error updating article count metric: %v", err)
				} else {
					metrics.UpdateArticlesInDatabase(count)
				}
			}
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received, stopping services...")

	// Stop scheduler gracefully first
	if err := summarizationScheduler.Stop(); err != nil {
		log.Printf("Error stopping summarization scheduler: %v", err)
	}

	cancel()
	wg.Wait()
	log.Println("All services stopped successfully")
}

func loadFeeds(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var feeds []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			feeds = append(feeds, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return feeds, nil
}

func initDatabase(cfg *config.Config) (*sql.DB, error) {
	connStr := cfg.GetConnectionString()

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %v", err)
	}

	// Create tables if they don't exist
	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	// Initialize summary tables
	if err := InitializeSummaryTables(db); err != nil {
		return nil, fmt.Errorf("failed to create summary tables: %v", err)
	}

	// Initialize Discord tables
	if err := InitializeDiscordTables(db); err != nil {
		return nil, fmt.Errorf("failed to create Discord tables: %v", err)
	}

	log.Println("Database connection established")
	return db, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func createTables(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS articles (
			id BIGSERIAL PRIMARY KEY,
			title TEXT NOT NULL,
			url TEXT UNIQUE NOT NULL,
			publish_date TIMESTAMP WITH TIME ZONE,
			summary TEXT,
			full_content TEXT,
			fetch_time TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			posted_to_discord BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			
			-- Additional fields for RSS monitoring compatibility
			feed_url TEXT,
			content_hash TEXT UNIQUE,
			fetch_duration_ms INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_url ON articles(url)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_content_hash ON articles(content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_publish_date ON articles(publish_date DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_fetch_time ON articles(fetch_time DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_posted_to_discord ON articles(posted_to_discord)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_feed_url ON articles(feed_url)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_created_at ON articles(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_updated_at ON articles(updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS fetch_logs (
			id SERIAL PRIMARY KEY,
			feed_url TEXT NOT NULL,
			status TEXT NOT NULL,
			message TEXT,
			duration_ms INTEGER,
			articles_found INTEGER DEFAULT 0,
			new_articles INTEGER DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fetch_logs_feed_url ON fetch_logs(feed_url)`,
		`CREATE INDEX IF NOT EXISTS idx_fetch_logs_created_at ON fetch_logs(created_at)`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %s, error: %v", query, err)
		}
	}

	return nil
}
