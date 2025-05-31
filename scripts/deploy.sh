#!/bin/bash
# Information Broker Deployment Script

set -e  # Exit on any error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if docker and docker-compose are installed
check_dependencies() {
    print_status "Checking dependencies..."
    
    if ! command -v docker &> /dev/null; then
        print_error "Docker is not installed. Please install Docker first."
        exit 1
    fi
    
    if ! command -v docker compose &> /dev/null; then
        print_error "Docker Compose is not installed. Please install Docker Compose first."
        exit 1
    fi
    
    print_success "Dependencies check passed"
}

# Function to check if .env file exists
check_environment() {
    print_status "Checking environment configuration..."
    
    if [ ! -f ".env" ]; then
        print_warning ".env file not found. Creating from template..."
        cp .env.example .env
        print_warning "Please edit .env file with your specific configuration before running deploy again."
        print_warning "Pay special attention to database passwords and security settings."
        exit 1
    fi
    
    print_success "Environment configuration found"
}

# Function to build and start services
deploy() {
    print_status "Building and starting Information Broker services..."
    
    # Pull latest images
    print_status "Pulling latest base images..."
    docker compose pull
    
    # Build and start services
    print_status "Building and starting services..."
    docker compose up -d --build
    
    print_success "Services started successfully!"
}

# Function to show service status
show_status() {
    print_status "Service Status:"
    docker compose ps
    
    echo ""
    print_status "Service Health:"
    
    # Wait a moment for services to start
    sleep 5
    
    # Check each service health
    services=("rss-monitor" "postgres" "prometheus" "grafana")
    
    for service in "${services[@]}"; do
        if docker compose ps "$service" | grep -q "Up (healthy)"; then
            print_success "$service: Healthy"
        elif docker compose ps "$service" | grep -q "Up"; then
            print_warning "$service: Running (health check pending...)"
        else
            print_error "$service: Not running properly"
        fi
    done
}

# Function to show access URLs
show_urls() {
    echo ""
    print_status "Access URLs:"
    echo "  📊 API Endpoints:     http://localhost:8080"
    echo "  📈 Prometheus:        http://localhost:9090"
    echo "  📋 Grafana:          http://localhost:3001 (admin/your_password)"
    echo "  🔧 Health Check:     http://localhost:8080/health"
    echo "  📊 Metrics:          http://localhost:8080/metrics"
}

# Function to show logs
show_logs() {
    print_status "Recent logs from all services:"
    docker compose logs --tail=20
}

# Function to stop services
stop() {
    print_status "Stopping Information Broker services..."
    docker compose down
    print_success "Services stopped successfully!"
}

# Function to restart services
restart() {
    print_status "Restarting Information Broker services..."
    docker compose restart
    print_success "Services restarted successfully!"
}

# Function to clean up (remove volumes)
clean() {
    print_warning "This will remove all data including database content!"
    read -p "Are you sure? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_status "Stopping services and removing volumes..."
        docker compose down -v
        print_success "Cleanup completed!"
    else
        print_status "Cleanup cancelled."
    fi
}

# Function to backup database
backup() {
    print_status "Creating database backup..."
    timestamp=$(date +"%Y%m%d_%H%M%S")
    backup_file="backup_${timestamp}.sql"
    
    if docker compose exec -T postgres pg_dump -U postgres information_broker > "$backup_file"; then
        print_success "Database backup created: $backup_file"
    else
        print_error "Backup failed!"
        exit 1
    fi
}

# Function to show help
show_help() {
    echo "Information Broker Deployment Script"
    echo ""
    echo "Usage: $0 [COMMAND]"
    echo ""
    echo "Commands:"
    echo "  deploy      Build and start all services (default)"
    echo "  status      Show service status and health"
    echo "  logs        Show recent logs from all services"
    echo "  stop        Stop all services"
    echo "  restart     Restart all services"
    echo "  clean       Stop services and remove all data (⚠️  DESTRUCTIVE)"
    echo "  backup      Create database backup"
    echo "  help        Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0              # Deploy the application"
    echo "  $0 status       # Check service status"
    echo "  $0 logs         # View recent logs"
    echo "  $0 backup       # Create database backup"
}

# Main script logic
main() {
    case "${1:-deploy}" in
        "deploy")
            check_dependencies
            check_environment
            deploy
            show_status
            show_urls
            ;;
        "status")
            show_status
            show_urls
            ;;
        "logs")
            show_logs
            ;;
        "stop")
            stop
            ;;
        "restart")
            restart
            show_status
            ;;
        "clean")
            clean
            ;;
        "backup")
            backup
            ;;
        "help"|"-h"|"--help")
            show_help
            ;;
        *)
            print_error "Unknown command: $1"
            show_help
            exit 1
            ;;
    esac
}

# Run main function
main "$@"