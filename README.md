# Information Broker

An end-to-end RSS article summarization and publishing system that continuously monitors RSS feeds, intelligently summarizes articles using AI, and publishes summaries to Discord channels with comprehensive monitoring and observability.

## Project Overview

Information Broker solves the challenge of staying informed in today's information-heavy world by automatically processing multiple RSS feeds, generating concise AI-powered summaries, and delivering them directly to your team's Discord channels. The system guarantees reliable, sequential processing with built-in error handling, metrics collection, and real-time monitoring to ensure no important content is missed while preventing API rate limiting and system overload.

## System Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   RSS Feeds     │    │  RSS Monitor     │    │ Summarization   │
│   (External)    │───▶│  - Feed Scraper  │───▶│ Scheduler       │
│                 │    │  - Deduplication │    │ - Single Worker │
└─────────────────┘    │  - Content Hash  │    │ - Thread-Safe   │
                       └──────────────────┘    │ - Queue Manager │
                                               └─────────────────┘
                                                        │
┌─────────────────┐    ┌──────────────────┐             │
│   Discord       │    │   AI Summary     │             │
│   Webhook       │◀───│   (Ollama LLM)   │◀────────────┘
│                 │    │   - Local API    │
└─────────────────┘    │                  │
                       └──────────────────┘
                                │
┌─────────────────┐    ┌──────────────────┐
│  PostgreSQL     │    │   Prometheus     │
│  Database       │    │   Metrics        │
│  - Articles     │    │   - Pipeline     │
│  - Summaries    │◀───│   - Latency      │
│  - Logs         │    │   - Errors       │
│  - Webhooks     │    │   - Queue Depth  │
└─────────────────┘    └──────────────────┘
                                │
                       ┌──────────────────┐
                       │   Grafana        │
                       │   Dashboards     │
                       │   - Health       │
                       │   - Performance  │
                       │   - Alerts       │
                       └──────────────────┘
```

### Key Components

- **RSS Monitor**: Continuously scrapes configured RSS feeds, performs content deduplication using SHA-256 hashing
- **Summarization Scheduler**: Thread-safe single-worker queue ensuring only one AI request is in-flight at any time
- **Ollama Integration**: Local LLM API for article summarization with configurable models and retry logic
- **Discord Publisher**: Webhook-based notification system with formatted summaries and article links
- **PostgreSQL Database**: Persistent storage for articles, summaries, logs, and operational metrics
- **Prometheus Metrics**: Real-time monitoring of all pipeline stages with custom metrics
- **Grafana Dashboards**: Pre-configured visualizations for system health and performance analysis

## Key Features

- **Sequential Summarization**: Single-worker architecture prevents API rate limiting and ensures predictable load
- **Centralized Queueing**: Thread-safe queue management with priority handling and backpressure protection
- **Robust Error Handling**: Circuit breakers, exponential backoff, and comprehensive retry logic
- **Real-time Metrics**: Prometheus integration tracking feed processing, summarization success/failure, queue depth, and latency
- **Complete Observability**: Pre-built Grafana dashboards for monitoring errors, throughput, and system health
- **Zero-Configuration Setup**: Fully containerized with Docker Compose and environment-based configuration
- **Content Deduplication**: SHA-256 content hashing prevents duplicate article processing
- **Health Monitoring**: Built-in health checks for all services with dependency validation
- **Graceful Shutdown**: Proper signal handling and resource cleanup on termination
- **Article Filtering**: Configurable initiation date to ignore articles published before system start
- **Chronological Processing**: Articles are processed in chronological order to maintain temporal consistency

## Getting Started

### Prerequisites

- **Docker**: Version 20.10+ with Compose V2 support
- **Docker Compose**: V2.0+ (usually bundled with Docker)
- **Git**: For cloning the repository
- **Ollama Server**: Local or remote instance with desired model (e.g., llama3)

### Step-by-Step Setup

1. **Clone the Repository**
   ```bash
   git clone https://github.com/PureCypher/Information-Broker
   cd information-broker
   ```

2. **Generate Secure Passwords**
   ```bash
   # Generate secure passwords for database and Grafana
   ./scripts/generate-passwords.sh
   ```
   
   This script will:
   - Create a backup of your existing `.env` file
   - Generate secure random passwords for database and Grafana
   - Create a 32-character secret key for Grafana
   - Update your `.env` file with the new credentials
   - Display the generated credentials for your records

3. **Essential Configuration**
   After password generation, update these critical values in `.env`:
   ```bash
   # Set your Ollama server URL and model
   OLLAMA_URL=http://your-ollama-server:11434
   OLLAMA_MODEL=llama3
   
   # Configure Discord webhook (optional but recommended)
   DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/your/webhook/url
   
   # Passwords are already generated - no manual entry needed!
   # DB_PASSWORD=<automatically_generated>
   # GRAFANA_ADMIN_PASSWORD=<automatically_generated>
   # GRAFANA_SECRET_KEY=<automatically_generated>
   ```

4. **Start the System**
   ```bash
   # Start all services
   docker compose up -d
   
   # Or use the Makefile for convenience
   make docker-run
   ```

5. **Verify Deployment**
   ```bash
   # Check service health
   make status
   
   # View logs
   make logs
   ```

### Default Service URLs

After successful deployment, access these services:

| Service | URL | Purpose |
|---------|-----|---------|
| **Application API** | http://localhost:8080 | Health checks, statistics, manual triggers |
| **Health Check** | http://localhost:8080/health | Service health status |
| **Prometheus** | http://localhost:9090 | Metrics collection and querying |
| **Grafana** | http://localhost:3001 | Dashboards and visualization (admin/admin) |
| **PostgreSQL** | localhost:5432 | Database access (postgres/postgres) |

## Security Setup

### Password Generation

Information Broker includes automated scripts for generating secure passwords and secret keys:

#### Initial Setup
```bash
# Generate all required passwords and keys
./scripts/generate-passwords.sh
```

This script generates:
- **Database Password**: 32-character secure password for PostgreSQL
- **Grafana Admin Password**: 24-character password for Grafana admin login
- **Grafana Secret Key**: 32-character alphanumeric key for internal encryption

#### Manual Password Management
If you prefer to set passwords manually, edit the `.env` file:
```bash
# Database credentials
DB_PASSWORD=your_secure_32_character_password_here

# Grafana credentials
GRAFANA_ADMIN_PASSWORD=your_secure_grafana_password
GRAFANA_SECRET_KEY=your_32_character_secret_key_here
```

**Security Best Practices**:
- Use the automated scripts for maximum security
- Store generated passwords in a secure password manager
- Never commit `.env` files to version control
- Regenerate passwords periodically in production
- Use different passwords for each environment (dev/staging/prod)

## Configuration

### Core Environment Variables

#### Database Configuration
```bash
DB_HOST=postgres                    # Database hostname
DB_PORT=5432                       # Database port
DB_USER=postgres                   # Database username
DB_PASSWORD=secure_password        # Database password
DB_NAME=information_broker         # Database name
```

#### Application Settings
```bash
APP_PORT=8080                      # API server port
RSS_FETCH_INTERVAL=5m              # Feed polling interval
RSS_FEEDS_FILE=/app/feeds.txt      # RSS feeds configuration file
LOG_LEVEL=info                     # Logging level (debug/info/warn/error)
APP_INITIATION_DATE=2020-01-01     # Articles published before this date will be ignored
                                   # Format: YYYY-MM-DD, YYYY-MM-DDTHH:MM:SSZ, or YYYY-MM-DD HH:MM:SS
ARTICLE_CUTOFF_DATE=2025-05-31T00:00:00Z  # Only articles published on/after this date are processed
                                          # Timezone-agnostic UTC comparison
```

#### Ollama AI Configuration
```bash
OLLAMA_URL=http://ollama:11434      # Ollama server endpoint
OLLAMA_MODEL=llama3                # Model for summarization
OLLAMA_TIMEOUT=60s                 # Request timeout
OLLAMA_MAX_RETRIES=3               # Maximum retry attempts
```

#### Discord Integration
```bash
# Option 1: Single webhook URL (backward compatibility)
DISCORD_WEBHOOK_URL=               # Single Discord webhook URL (optional)

# Option 2: Multiple webhook URLs for multi-cast notifications
DISCORD_WEBHOOK_URLS=              # Comma-separated webhook URLs (optional)
                                   # Example: https://discord.com/api/webhooks/1/token1,https://discord.com/api/webhooks/2/token2

DISCORD_MAX_RETRIES=2              # Discord publish retry attempts
DISCORD_TIMEOUT=30s                # Discord request timeout
```

#### Performance Tuning
```bash
MAX_CONCURRENT_FEEDS=10            # Concurrent feed processing limit
MAX_ARTICLE_CONTENT_LENGTH=10000   # Content length limit (characters)
MAX_SUMMARY_LENGTH=200             # Summary length limit (characters)
HTTP_READ_TIMEOUT=15s              # HTTP client read timeout
HTTP_WRITE_TIMEOUT=15s             # HTTP client write timeout
```

#### Monitoring Configuration
```bash
PROMETHEUS_PORT=9090               # Prometheus server port
PROMETHEUS_RETENTION=90d           # Metrics retention period
GRAFANA_PORT=3001                  # Grafana dashboard port
GRAFANA_ADMIN_PASSWORD=admin       # Grafana admin password
```

## Usage

### Adding RSS Feeds

Edit the [`feeds.txt`](feeds.txt) file to add or remove RSS feeds:

```bash
# Edit the feeds file
nano feeds.txt

# Add new feed URLs (one per line)
https://your-new-feed.com/rss
https://another-feed.com/feed.xml

# Restart to apply changes
docker compose restart rss-monitor
```

The system includes 47 pre-configured cybersecurity and technology feeds covering major news sources, security publications, and threat intelligence feeds.

### Article Filtering and Chronological Processing

The system includes intelligent article filtering with two complementary mechanisms to control which articles are processed:

#### Article Cutoff Date (Primary Filter)
The primary filtering mechanism uses a strict UTC-based cutoff date to ensure only articles published on or after a specific date and time are processed:

```bash
# Set the article cutoff date in .env file
ARTICLE_CUTOFF_DATE=2025-05-31T00:00:00Z     # Only articles from this date/time forward

# Supported date formats:
ARTICLE_CUTOFF_DATE=2025-05-31                    # Date only (assumes 00:00:00Z)
ARTICLE_CUTOFF_DATE=2025-05-31T00:00:00Z          # Full ISO 8601 UTC format (recommended)
ARTICLE_CUTOFF_DATE=2025-05-31T10:30:00+05:00     # With timezone (converted to UTC)
```

**Key Features:**
- **Thread-Safe**: Efficient date parsing with normalized UTC comparison
- **Timezone-Agnostic**: All dates are converted to UTC for consistent comparison
- **RSS Format Compatible**: Supports all common RSS date formats automatically
- **No Missing Dates**: Articles without publish dates are skipped (safer default)
- **Prometheus Metrics**: Tracks filtered vs processed articles separately

#### Initiation Date Configuration (Legacy Filter)
```bash
# Set the initiation date in .env file (for backward compatibility)
APP_INITIATION_DATE=2024-01-01     # Articles before this date will be ignored

# Supported date formats:
APP_INITIATION_DATE=2024-01-01                    # Date only
APP_INITIATION_DATE=2024-01-01T10:30:00Z          # ISO 8601 with timezone
APP_INITIATION_DATE=2024-01-01 10:30:00           # Date and time
```

#### How the Dual Filtering Works
1. **Date Validation**: Articles without publish dates are immediately rejected
2. **Cutoff Date Check**: Articles before `ARTICLE_CUTOFF_DATE` are filtered (tracked as `articles_filtered_pre_cutoff_total`)
3. **Initiation Date Check**: Articles before `APP_INITIATION_DATE` are filtered (tracked as `skipped_before_initiation`)
4. **Processing**: Only articles passing both filters are processed (tracked as `articles_processed_post_cutoff_total`)
5. **Chronological Processing**: Approved articles are sorted by publication date and processed in chronological order (oldest first)

#### Database and Storage Behavior
- **No Database Storage**: Articles filtered by cutoff date are never inserted into the database
- **No Processing**: Filtered articles bypass all summarization and Discord notification steps
- **Clean Logs**: Filtered articles are logged but don't clutter processing metrics
- **Resource Efficient**: Filtering happens early in the pipeline to minimize resource usage

#### Prometheus Metrics for Monitoring
```bash
# New metrics for cutoff date filtering
articles_filtered_pre_cutoff_total{feed_url}      # Articles filtered before cutoff date
articles_processed_post_cutoff_total{feed_url}    # Articles that passed cutoff filter

# Example queries for Grafana
rate(articles_filtered_pre_cutoff_total[5m])      # Rate of filtered articles
rate(articles_processed_post_cutoff_total[5m])    # Rate of processed articles
```

#### Use Cases and Configuration Examples

**Production Deployment (Recommended)**
```bash
# Process only articles from May 31, 2025 onward
ARTICLE_CUTOFF_DATE=2025-05-31T00:00:00Z
APP_INITIATION_DATE=2020-01-01  # Keep as fallback
```

**Testing Environment**
```bash
# Process only today's articles for testing
ARTICLE_CUTOFF_DATE=2025-05-31T00:00:00Z
APP_INITIATION_DATE=2025-05-31
```

**Migration from Old System**
```bash
# Resume processing from last known good date
ARTICLE_CUTOFF_DATE=2025-05-30T15:30:00Z  # Exact time when old system stopped
APP_INITIATION_DATE=2025-01-01             # Broader fallback
```

**Development with Timezone Handling**
```bash
# Handle articles from different timezones correctly
ARTICLE_CUTOFF_DATE=2025-05-31T00:00:00Z  # 12AM UTC = 8PM EST previous day

# This ensures consistent filtering regardless of article's original timezone
```

#### Timezone Handling Examples
The system automatically normalizes all dates to UTC for comparison:

```bash
# Article published at 8PM EST on May 30, 2025
# Converts to: 2025-05-31T01:00:00Z (passes cutoff)

# Article published at 6PM PST on May 30, 2025
# Converts to: 2025-05-31T02:00:00Z (passes cutoff)

# Article published at 11PM UTC on May 30, 2025
# Stays as: 2025-05-30T23:00:00Z (filtered out)
```

#### Testing and Validation
```bash
# Test the filtering logic
go test -v -run TestArticleDateFiltering

# Test timezone handling specifically
go test -v -run TestTimezoneHandling

# Run demonstration script
go run test_cutoff_date.go
```

### Monitoring Pipeline Health

#### API Endpoints
```bash
# System health check
curl http://localhost:8080/health

# Processing statistics
curl http://localhost:8080/stats

# Feed status
curl http://localhost:8080/feeds

# Summarization queue status
curl http://localhost:8080/summarization/stats
```

#### Using the Makefile
```bash
# Check overall system status
make status

# Test all API endpoints
make api-test

# View application health
make health
```

### Grafana Dashboard Access

1. **Access Grafana**: Navigate to http://localhost:3001
2. **Login**: Use `admin` / `admin` (or your configured password)
3. **View Dashboards**: Pre-configured dashboards are available in the "Information Broker" folder

### Graceful Shutdown

```bash
# Graceful shutdown with data preservation
docker compose down

# Or using Makefile
make docker-clean  # Includes volume cleanup
```

The application handles SIGTERM and SIGINT signals gracefully, ensuring:
- In-flight summarization requests complete
- Database connections close properly
- Temporary files are cleaned up
- Queue state is preserved

## Monitoring & Observability

### Content Volume Observability

The system provides comprehensive real-time and historical visibility into content volume through:

#### Key Metrics
- **Total Articles in Database**: Real-time count of all articles stored in the database
- **Daily Article Ingestion Rate**: Number of articles successfully processed each day over the past 30 days
- **Article Processing Success/Failure**: Breakdown of processing status for troubleshooting

#### Dashboard Panels
1. **Total Articles in Database** - Single stat panel showing current article count with configurable thresholds
2. **Daily Article Ingestion Rate** - Bar chart displaying daily processing trends for content volume analysis
3. **Real-time Processing Rate** - Time series showing articles processed per minute/hour

#### Alerting
- **Low Daily Article Count**: Fires when daily article processing falls below 5 articles (configurable threshold)
- **No Articles Processed**: Critical alert when no articles are processed for 24 hours
- **Database Growth Stalled**: Alert when no new articles are added for 2 days

### Grafana Dashboards

The system includes five pre-configured dashboards:

1. **Information Broker Overview** (`information-broker-dashboard.json`)
   - System health indicators
   - Processing rates and queue status
   - Error rate monitoring
   - **NEW: Total Articles in Database panel**
   - **NEW: Daily Article Ingestion Rate chart**

2. **Feed Processing Rates** (`feed-processing-rates-dashboard.json`)
   - Per-feed processing statistics
   - Success/failure rates
   - Processing time analysis

3. **Pipeline Latency** (`pipeline-latency-dashboard.json`)
   - End-to-end processing times
   - Queue wait times
   - API response latencies

4. **Summarization Success/Failure** (`summarization-success-failure-dashboard.json`)
   - AI summarization metrics
   - Model performance tracking
   - Error categorization

5. **Discord Latency & Errors** (`discord-latency-errors-dashboard.json`)
   - Webhook delivery performance
   - Discord API error tracking
   - Message formatting metrics

### Key Prometheus Metrics

#### Feed Processing Metrics
- `rss_feeds_processed_total`: Total feeds processed with status labels
- `rss_articles_found_total`: Articles discovered per feed
- `rss_new_articles_total`: New articles added to database
- `rss_fetch_duration_seconds`: Feed fetching latency

#### Content Volume Metrics
- `articles_processed_total`: Counter incremented each time an article is processed and written to the database
- `articles_in_database`: Gauge reflecting the current total article count in the database (updated every 5 minutes)

#### Article Filtering Metrics
- `articles_filtered_pre_cutoff_total`: Articles filtered due to publication before cutoff date
- `articles_processed_post_cutoff_total`: Articles that passed cutoff date filter and were processed

#### Summarization Metrics
- `summarization_requests_total`: Summarization requests by status
- `summarization_queue_depth`: Current queue size
- `summarization_duration_seconds`: Time to generate summaries
- `ollama_api_requests_total`: Ollama API call statistics

#### Discord Publishing Metrics
- `discord_webhook_requests_total`: Webhook delivery attempts
- `discord_publish_duration_seconds`: Discord API latency
- `discord_errors_total`: Publishing error counts

#### System Health Metrics
- `database_connections_active`: PostgreSQL connection pool status
- `http_requests_total`: API endpoint usage
- `application_uptime_seconds`: Service availability

### Content Volume Monitoring Queries

#### Grafana Dashboard Queries
```promql
# Current total articles in database
articles_in_database

# Daily article ingestion rate (last 30 days)
increase(articles_processed_total{status="success"}[1d])

# Article processing success rate
rate(articles_processed_total{status="success"}[5m]) / rate(articles_processed_total[5m]) * 100

# Weekly content volume trend
increase(articles_processed_total{status="success"}[7d])
```

#### Alerting Rules
```promql
# Daily article processing below threshold
increase(articles_processed_total{status="success"}[1d]) < 5

# No articles processed in 24 hours
increase(articles_processed_total{status="success"}[1d]) == 0

# Database growth stalled (no new articles in 2 days)
increase(articles_processed_total{status="success"}[2d]) == 0
```

### Health Check Endpoints

- **Application Health**: `GET /health`
  ```json
  {
    "status": "healthy",
    "timestamp": "2025-01-15T10:30:00Z",
    "database": "connected",
    "ollama": "available",
    "queue_depth": 5
  }
  ```

- **Prometheus Metrics**: `GET /metrics`
  - Standard Prometheus exposition format
  - All custom application metrics
  - Go runtime metrics

## Testing

### Integration Tests

The system includes comprehensive testing through the Makefile:

```bash
# Run all API endpoint tests
make api-test

# Performance testing
make perf-test

# Load testing (requires 'hey' tool)
make load-test

# Health verification
make health
```

### Database Operations

```bash
# Clear database for fresh start
make clear-db

# Force clear without confirmation
make clear-db-force

# Reset database and restart application
make reset-db

# Backup database
make backup-db
```

### Development Testing

```bash
# Start development environment
make setup-dev

# Build and run locally
make dev

# Run with development database
make dev-db
make run
make dev-db-stop
```

## Troubleshooting

### Common Issues and Solutions

#### 1. Ollama Connection Failures
**Symptoms**: Summarization requests failing, queue building up
```bash
# Check Ollama connectivity
curl http://your-ollama-server:11434/api/tags

# Verify model availability
curl http://your-ollama-server:11434/api/show -d '{"name": "llama3"}'

# Check logs for specific errors
make logs | grep -i ollama
```

**Solutions**:
- Verify `OLLAMA_URL` in `.env` file
- Ensure Ollama server is running and accessible
- Check if the specified model is pulled: `ollama pull llama3`
- Increase `OLLAMA_TIMEOUT` for slower responses

#### 2. Database Connection Issues
**Symptoms**: Application failing to start, health checks failing
```bash
# Check database status
docker compose exec postgres pg_isready

# View database logs
docker compose logs postgres

# Test connection manually
docker compose exec postgres psql -U postgres -d information_broker -c "\dt"
```

**Solutions**:
- Verify database credentials in `.env`
- Ensure PostgreSQL container is healthy
- Check for port conflicts on 5432
- Restart database: `docker compose restart postgres`

#### 3. Queue Saturation
**Symptoms**: Growing queue depth, delayed processing
```bash
# Check queue status
curl http://localhost:8080/summarization/stats

# Monitor queue metrics in Grafana
# Check summarization processing times
```

**Solutions**:
- Reduce `RSS_FETCH_INTERVAL` to slow article intake
- Increase `OLLAMA_TIMEOUT` if requests are timing out
- Check Ollama server performance and scaling
- Consider reducing `MAX_CONCURRENT_FEEDS`

#### 4. Discord Publishing Failures
**Symptoms**: Summaries not appearing in Discord, webhook errors
```bash
# Test webhook manually
curl -X POST "YOUR_DISCORD_WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -d '{"content": "Test message"}'

# Check Discord error metrics
curl http://localhost:8080/metrics | grep discord
```

**Solutions**:
- Verify `DISCORD_WEBHOOK_URL` is correct and active
- Check webhook permissions in Discord server
- Increase `DISCORD_TIMEOUT` for slow connections
- Review Discord rate limiting documentation

#### 5. High Memory Usage
**Symptoms**: Out of memory errors, container restarts
```bash
# Monitor resource usage
docker stats information-broker-app

# Check for memory leaks in metrics
curl http://localhost:8080/metrics | grep go_memstats
```

**Solutions**:
- Reduce `MAX_ARTICLE_CONTENT_LENGTH`
- Lower `MAX_CONCURRENT_FEEDS`
- Increase Docker container memory limits
- Review feed content for unusually large articles

### Log Analysis

```bash
# View real-time logs
make logs

# Filter for specific components
docker compose logs rss-monitor | grep "ERROR"
docker compose logs rss-monitor | grep "summarization"

# Export logs for analysis
docker compose logs --since 24h rss-monitor > debug.log
```

### Performance Optimization

```bash
# Monitor performance metrics
make api-test
curl http://localhost:8080/metrics | grep duration

# Database performance
docker compose exec postgres psql -U postgres -d information_broker -c "
  SELECT schemaname,tablename,attname,avg_width,n_distinct,correlation 
  FROM pg_stats 
  WHERE schemaname='public';"
```

## Extending

### Adding New Features

#### Custom Summarization Models
1. Update `OLLAMA_MODEL` in `.env`
2. Modify summarization prompts in [`summarizer.go`](summarizer.go)
3. Test with new model before production deployment

#### Additional RSS Feeds
1. Add URLs to [`feeds.txt`](feeds.txt)
2. Restart application: `docker compose restart rss-monitor`
3. Monitor feed health in Grafana dashboards

#### Custom Metrics
1. Add new metrics in [`metrics.go`](metrics.go)
2. Instrument code with metric updates
3. Create custom Grafana dashboard panels

#### Webhook Integrations

**Multi-Discord Webhook Support**: The system now supports sending notifications to multiple Discord webhooks simultaneously:

```bash
# Configure multiple Discord webhooks for multi-cast notifications
DISCORD_WEBHOOK_URLS=https://discord.com/api/webhooks/123/token1,https://discord.com/api/webhooks/456/token2

# Or use single webhook for backward compatibility
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/123/token1
```

**Features**:
- **Concurrent Delivery**: All webhooks receive notifications simultaneously
- **Individual Error Handling**: Failed webhooks don't affect others
- **Backward Compatibility**: Single webhook configuration still works
- **Automatic Fallback**: Single webhook URL automatically converts to multi-webhook format

**Extending to Other Platforms**:
1. Extend webhook functionality in [`discord_webhook.go`](discord_webhook.go)
2. Add new webhook targets (Slack, Teams, etc.)
3. Configure routing based on content type or source

### Scaling Considerations

#### Multi-Worker Summarization
The current single-worker design can be extended to multi-worker:

1. **Modify Scheduler**: Update [`summarization_scheduler.go`](summarization_scheduler.go) to support worker pools
2. **Add Worker Management**: Implement worker lifecycle and load balancing
3. **Update Metrics**: Track per-worker performance
4. **Configuration**: Add `SUMMARIZATION_WORKERS` environment variable

**Trade-offs**: Higher throughput vs. increased API load and complexity

#### Horizontal Scaling
For high-volume deployments:

1. **Load Balancer**: Add reverse proxy for multiple application instances
2. **Shared Queue**: Implement Redis or RabbitMQ for distributed queue
3. **Database Clustering**: Configure PostgreSQL replication
4. **Feed Distribution**: Partition feeds across instances

#### Performance Tuning
```bash
# Increase concurrent feed processing
MAX_CONCURRENT_FEEDS=20

# Optimize batch processing
RSS_FETCH_INTERVAL=2m

# Tune database connections
# Add to docker-compose.yml postgres service
command: -c max_connections=200 -c shared_buffers=256MB
```

### Development Guidelines

#### Code Structure
- **Main Application**: [`main.go`](main.go) - Service orchestration and lifecycle
- **RSS Processing**: [`monitor.go`](monitor.go) - Feed scraping and article extraction
- **AI Integration**: [`summarizer.go`](summarizer.go) - Ollama API communication
- **Queue Management**: [`summarization_scheduler.go`](summarization_scheduler.go) - Thread-safe job scheduling
- **Metrics**: [`metrics.go`](metrics.go) - Prometheus instrumentation
- **Configuration**: [`config/config.go`](config/config.go) - Environment management

#### Testing Strategy
```bash
# Unit tests for core components
go test ./...

# Integration tests with test database
make dev-db
go test -tags=integration ./...
make dev-db-stop

# Performance benchmarks
go test -bench=. -benchmem ./...
```

#### Contributing
1. Fork the repository
2. Create feature branch: `git checkout -b feature/new-feature`
3. Add tests for new functionality
4. Update documentation and README
5. Submit pull request with detailed description

## License & Credits

### License
This project is licensed under the MIT License. See LICENSE file for details.

### Credits
- **RSS Processing**: Built with Go's standard library and `gofeed` parser
- **AI Integration**: Powered by Ollama local LLM infrastructure
- **Monitoring**: Prometheus and Grafana open-source observability stack
- **Database**: PostgreSQL for reliable data persistence
- **Containerization**: Docker and Docker Compose for consistent deployments

### Third-Party Dependencies
- `github.com/lib/pq` - PostgreSQL driver
- `github.com/prometheus/client_golang` - Prometheus metrics
- `github.com/mmcdole/gofeed` - RSS/Atom feed parsing
- Various Go standard library packages

### Security Considerations
- **Use the provided password generation scripts** for secure credentials
- Change all default passwords in production using `./scripts/generate-passwords.sh`
- Use HTTPS with reverse proxy (nginx/Traefik)
- Implement network segmentation
- Regular security updates for all components
- Monitor access logs and implement rate limiting
- Secure Discord webhook URLs and rotate periodically
- Regenerate passwords periodically with `./scripts/regenerate-passwords.sh`
- Never commit `.env` files containing real credentials to version control

---

**Information Broker** - Intelligent RSS Processing with AI-Powered Summarization