-- Enhanced PostgreSQL schema for Information Broker
-- This schema includes optimized tables for articles and webhook logs with proper indexing

-- Drop existing tables if recreating (uncomment if needed)
-- DROP TABLE IF EXISTS webhook_logs CASCADE;
-- DROP TABLE IF EXISTS articles CASCADE;

-- Articles table with enhanced schema
CREATE TABLE IF NOT EXISTS articles (
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
);

-- Webhook logs table for tracking Discord webhook attempts
CREATE TABLE IF NOT EXISTS webhook_logs (
    id BIGSERIAL PRIMARY KEY,
    article_id BIGINT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL DEFAULT 1,
    response_code INTEGER,
    response_body TEXT,
    latency_ms INTEGER,
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Performance indexes for articles table
CREATE INDEX IF NOT EXISTS idx_articles_url ON articles(url);
CREATE INDEX IF NOT EXISTS idx_articles_content_hash ON articles(content_hash);
CREATE INDEX IF NOT EXISTS idx_articles_publish_date ON articles(publish_date DESC);
CREATE INDEX IF NOT EXISTS idx_articles_fetch_time ON articles(fetch_time DESC);
CREATE INDEX IF NOT EXISTS idx_articles_posted_to_discord ON articles(posted_to_discord);
CREATE INDEX IF NOT EXISTS idx_articles_feed_url ON articles(feed_url);
CREATE INDEX IF NOT EXISTS idx_articles_created_at ON articles(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_updated_at ON articles(updated_at DESC);

-- Performance indexes for webhook_logs table
CREATE INDEX IF NOT EXISTS idx_webhook_logs_article_id ON webhook_logs(article_id);
CREATE INDEX IF NOT EXISTS idx_webhook_logs_created_at ON webhook_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_logs_response_code ON webhook_logs(response_code);
CREATE INDEX IF NOT EXISTS idx_webhook_logs_attempt ON webhook_logs(attempt);

-- Composite indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_articles_feed_publish ON articles(feed_url, publish_date DESC);
CREATE INDEX IF NOT EXISTS idx_articles_discord_fetch ON articles(posted_to_discord, fetch_time DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_logs_article_attempt ON webhook_logs(article_id, attempt DESC);

-- Function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Trigger to automatically update updated_at on articles
DROP TRIGGER IF EXISTS update_articles_updated_at ON articles;
CREATE TRIGGER update_articles_updated_at
    BEFORE UPDATE ON articles
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- View for article statistics
CREATE OR REPLACE VIEW article_stats AS
SELECT 
    COUNT(*) as total_articles,
    COUNT(*) FILTER (WHERE posted_to_discord = true) as posted_to_discord,
    COUNT(*) FILTER (WHERE posted_to_discord = false) as pending_discord,
    COUNT(DISTINCT feed_url) as unique_feeds,
    MIN(publish_date) as earliest_article,
    MAX(publish_date) as latest_article
FROM articles;

-- View for webhook success rates
CREATE OR REPLACE VIEW webhook_stats AS
SELECT 
    a.feed_url,
    COUNT(wl.id) as total_attempts,
    COUNT(wl.id) FILTER (WHERE wl.response_code BETWEEN 200 AND 299) as successful_attempts,
    COUNT(wl.id) FILTER (WHERE wl.response_code NOT BETWEEN 200 AND 299 OR wl.response_code IS NULL) as failed_attempts,
    AVG(wl.latency_ms) as avg_latency_ms,
    MAX(wl.created_at) as last_attempt
FROM articles a
LEFT JOIN webhook_logs wl ON a.id = wl.article_id
GROUP BY a.feed_url;