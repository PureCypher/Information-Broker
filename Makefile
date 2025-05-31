# Information Broker Makefile
# Provides convenient commands for development, testing, and deployment

.PHONY: help build run clean docker-build docker-run logs

# Default target
help:
	@echo "Information Broker - Available Commands"
	@echo "======================================"
	@echo ""
	@echo "Development:"
	@echo "  build              Build the Go application"
	@echo "  run                Run the application locally"
	@echo "  clean              Clean build artifacts"
	@echo ""
	@echo "Database:"
	@echo "  clear-db           Clear database (with confirmation)"
	@echo "  clear-db-force     Clear database (no confirmation)"
	@echo "  reset-db           Clear database and restart app"
	@echo "  backup-db          Backup database to file"
	@echo ""
	@echo "Docker:"
	@echo "  docker-build       Build Docker image"
	@echo "  docker-run         Run application in Docker"
	@echo "  docker-clean       Clean Docker containers and images"
	@echo ""
	@echo "Production:"
	@echo "  deploy             Deploy to production environment"
	@echo "  logs               Show application logs"
	@echo "  health             Check application health"
	@echo ""
	@echo "Monitoring:"
	@echo "  prometheus         Start Prometheus monitoring"
	@echo "  grafana           Start Grafana dashboards"
	@echo "  monitoring        Start complete monitoring stack"
	@echo ""

# Development commands
build:
	@echo "Building Information Broker..."
	go mod tidy
	go build -o bin/information-broker .
	@echo "Build complete: bin/information-broker"

run: build
	@echo "Starting Information Broker..."
	./bin/information-broker

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	go clean
	@echo "Clean complete"

# Docker commands
docker-build:
	@echo "Building Docker image..."
	docker build -t information-broker:latest .
	@echo "Docker image built: information-broker:latest"

docker-run: docker-build
	@echo "Running Information Broker in Docker..."
	docker compose up -d

docker-clean:
	@echo "Cleaning Docker containers and images..."
	docker compose down --volumes --remove-orphans
	docker image prune -f
	docker volume prune -f
	@echo "Docker cleanup complete"

# Production commands
deploy:
	@echo "Deploying to production..."
	chmod +x scripts/deploy.sh
	./scripts/deploy.sh

logs:
	@echo "Showing application logs..."
	docker compose logs -f information-broker

health:
	@echo "Checking application health..."
	@curl -s http://localhost:8080/health | jq . || echo "Application not accessible"

# Monitoring commands
prometheus:
	@echo "Starting Prometheus..."
	docker compose up -d prometheus
	@echo "Prometheus available at http://localhost:9090"

grafana:
	@echo "Starting Grafana..."
	docker compose up -d grafana
	@echo "Grafana available at http://localhost:3000"

monitoring:
	@echo "Starting monitoring stack..."
	docker compose up -d prometheus grafana
	@echo "Monitoring stack started:"
	@echo "  Prometheus: http://localhost:9090"
	@echo "  Grafana: http://localhost:3000"

# Database management
clear-db:
	@echo "Clearing Information Broker database..."
	@echo "This will remove all articles and webhook logs!"
	@read -p "Are you sure? [y/N] " confirm && [ "$$confirm" = "y" ] || exit 1
	docker compose exec -T postgres psql -U postgres -d information_broker -c "TRUNCATE TABLE webhook_logs, summary_logs, discord_error_logs, fetch_logs, articles RESTART IDENTITY CASCADE;"
	@echo "Database cleared successfully - RSS feeds will be refreshed on next run"

clear-db-force:
	@echo "Force clearing Information Broker database (no confirmation)..."
	docker compose exec -T postgres psql -U postgres -d information_broker -c "TRUNCATE TABLE webhook_logs, summary_logs, discord_error_logs, fetch_logs, articles RESTART IDENTITY CASCADE;"
	@echo "Database cleared successfully - RSS feeds will be refreshed on next run"

reset-db: clear-db-force
	@echo "Restarting application to refresh feeds..."
	docker compose restart rss-monitor
	@echo "Application restarted - fresh RSS processing will begin shortly"

# Development database
dev-db:
	@echo "Starting development database..."
	docker run -d \
		--name information-broker-dev-db \
		-e POSTGRES_DB=information_broker \
		-e POSTGRES_USER=postgres \
		-e POSTGRES_PASSWORD=password \
		-p 5432:5432 \
		postgres:15-alpine
	@echo "Development database started on port 5432"

dev-db-stop:
	@echo "Stopping development database..."
	docker stop information-broker-dev-db 2>/dev/null || true
	docker rm information-broker-dev-db 2>/dev/null || true
	@echo "Development database stopped"

# Linting and formatting
lint:
	@echo "Running Go linter..."
	golangci-lint run ./...

format:
	@echo "Formatting Go code..."
	go fmt ./...
	goimports -w .

# Security scanning
security-scan:
	@echo "Running security scan..."
	gosec ./...

# Dependency management
deps-update:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

deps-audit:
	@echo "Auditing dependencies..."
	go list -json -m all | nancy sleuth

# Performance testing
perf-test:
	@echo "Running performance tests..."
	go test -bench=. -benchmem ./...

# Load testing with hey (if installed)
load-test:
	@echo "Running load test on /health endpoint..."
	@command -v hey >/dev/null 2>&1 || { echo "hey not installed. Install with: go install github.com/rakyll/hey@latest"; exit 1; }
	hey -n 1000 -c 10 http://localhost:8080/health

# API testing with curl
api-test:
	@echo "Testing API endpoints..."
	@echo "Health check:"
	@curl -s http://localhost:8080/health | jq . || echo "Failed"
	@echo ""
	@echo "Statistics:"
	@curl -s http://localhost:8080/stats | jq . || echo "Failed"
	@echo ""
	@echo "Feeds:"
	@curl -s http://localhost:8080/feeds | jq . || echo "Failed"
	@echo ""
	@echo "Summarization Stats:"
	@curl -s http://localhost:8080/summarization/stats | jq . || echo "Failed"

# Environment setup
setup-dev:
	@echo "Setting up development environment..."
	@echo "Installing development tools..."
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest
	@echo "Development environment setup complete"

# Generate documentation
docs:
	@echo "Generating documentation..."
	godoc -http=:6060 &
	@echo "Documentation server started at http://localhost:6060"

# All-in-one commands
dev: build run

deploy-full: docker-build deploy

# CI/CD helpers
ci-build:
	@echo "Running CI build..."
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/information-broker .

# Database migrations (if using a migration tool)
migrate-up:
	@echo "Running database migrations..."
	@command -v migrate >/dev/null 2>&1 || { echo "golang-migrate not installed"; exit 1; }
	migrate -path migrations -database "postgres://postgres:password@localhost:5432/information_broker?sslmode=disable" up

migrate-down:
	@echo "Rolling back database migrations..."
	migrate -path migrations -database "postgres://postgres:password@localhost:5432/information_broker?sslmode=disable" down

# Backup and restore
backup-db:
	@echo "Backing up database..."
	docker exec -t information-broker-postgres pg_dump -U postgres information_broker > backup_$(shell date +%Y%m%d_%H%M%S).sql
	@echo "Database backup complete"

# Check dependencies
check-deps:
	@echo "Checking system dependencies..."
	@command -v docker >/dev/null 2>&1 || { echo "Docker not installed"; exit 1; }
	@command -v docker compose >/dev/null 2>&1 || docker compose version >/dev/null 2>&1 || { echo "Docker Compose not installed"; exit 1; }
	@command -v go >/dev/null 2>&1 || { echo "Go not installed"; exit 1; }
	@echo "All dependencies satisfied"

# Show status
status:
	@echo "Information Broker Status"
	@echo "========================"
	@echo "Docker containers:"
	@docker compose ps 2>/dev/null || echo "No containers running"
	@echo ""
	@echo "Application health:"
	@curl -s http://localhost:8080/health 2>/dev/null | jq -r '.status // "Not accessible"' || echo "Not accessible"
	@echo ""
	@echo "Database status:"
	@docker compose exec -T postgres pg_isready 2>/dev/null || echo "Database not accessible"