#!/bin/bash

# Information Broker - Password Generation Script
# This script generates secure passwords for the database and Grafana

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
ENV_FILE=".env"
ENV_EXAMPLE_FILE=".env.example"
BACKUP_FILE=".env.backup.$(date +%Y%m%d_%H%M%S)"

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

# Function to generate a secure password
generate_password() {
    local length=${1:-32}
    # Generate a password with letters, numbers, and safe special characters
    # Using openssl if available, fallback to /dev/urandom
    if command -v openssl &> /dev/null; then
        openssl rand -base64 $((length * 3 / 4)) | tr -d "=+/" | cut -c1-${length}
    else
        # Use dd with urandom to avoid SIGPIPE issues
        dd if=/dev/urandom bs=1 count=100 2>/dev/null | LC_ALL=C tr -dc 'A-Za-z0-9!@#$%^&*()_+-=' | head -c "$length"
    fi
}

# Function to generate a secret key (alphanumeric only for better compatibility)
generate_secret_key() {
    local length=${1:-32}
    # Generate alphanumeric key
    if command -v openssl &> /dev/null; then
        openssl rand -hex $((length / 2)) | head -c "$length"
    else
        # Use dd with urandom to avoid SIGPIPE issues
        dd if=/dev/urandom bs=1 count=100 2>/dev/null | LC_ALL=C tr -dc 'A-Za-z0-9' | head -c "$length"
    fi
}

# Function to check if required tools are available
check_dependencies() {
    print_status "Checking dependencies..."
    
    if ! command -v tr &> /dev/null; then
        print_error "tr command not found. Please install coreutils."
        exit 1
    fi
    
    if [[ ! -e /dev/urandom ]]; then
        print_error "/dev/urandom not available. Cannot generate secure random passwords."
        exit 1
    fi
    
    print_success "All dependencies found."
}

# Function to backup existing .env file
backup_env_file() {
    if [[ -f "$ENV_FILE" ]]; then
        print_status "Backing up existing .env file to $BACKUP_FILE"
        cp "$ENV_FILE" "$BACKUP_FILE"
        print_success "Backup created: $BACKUP_FILE"
    fi
}

# Function to create .env file from template if it doesn't exist
create_env_from_template() {
    if [[ ! -f "$ENV_FILE" ]]; then
        if [[ -f "$ENV_EXAMPLE_FILE" ]]; then
            print_status "Creating .env file from template..."
            cp "$ENV_EXAMPLE_FILE" "$ENV_FILE"
            print_success ".env file created from template."
        else
            print_error ".env.example file not found. Cannot create .env file."
            exit 1
        fi
    fi
}

# Function to update or add environment variable in .env file
update_env_var() {
    local var_name="$1"
    local var_value="$2"
    
    if grep -q "^${var_name}=" "$ENV_FILE"; then
        # Variable exists, update it
        if [[ "$OSTYPE" == "darwin"* ]]; then
            # macOS
            sed -i '' "s/^${var_name}=.*/${var_name}=${var_value}/" "$ENV_FILE"
        else
            # Linux
            sed -i "s/^${var_name}=.*/${var_name}=${var_value}/" "$ENV_FILE"
        fi
        print_success "Updated $var_name"
    else
        # Variable doesn't exist, add it
        echo "${var_name}=${var_value}" >> "$ENV_FILE"
        print_success "Added $var_name"
    fi
}

# Main execution
main() {
    print_status "Starting password generation for Information Broker..."
    echo
    
    # Check dependencies
    check_dependencies
    echo
    
    # Backup existing .env file
    backup_env_file
    echo
    
    # Create .env file if it doesn't exist
    create_env_from_template
    echo
    
    print_status "Generating secure passwords and keys..."
    
    # Generate passwords
    DB_PASSWORD=$(generate_password 32)
    GRAFANA_ADMIN_PASSWORD=$(generate_password 24)
    GRAFANA_SECRET_KEY=$(generate_secret_key 32)
    
    # Update environment variables
    update_env_var "DB_PASSWORD" "$DB_PASSWORD"
    update_env_var "GRAFANA_ADMIN_PASSWORD" "$GRAFANA_ADMIN_PASSWORD"
    update_env_var "GRAFANA_SECRET_KEY" "$GRAFANA_SECRET_KEY"
    
    echo
    print_success "Password generation completed!"
    echo
    
    # Display the generated credentials
    echo -e "${BLUE}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}                    GENERATED CREDENTIALS                   ${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════════${NC}"
    echo
    echo -e "${YELLOW}Database Password:${NC}"
    echo -e "  Variable: ${GREEN}DB_PASSWORD${NC}"
    echo -e "  Value:    ${GREEN}$DB_PASSWORD${NC}"
    echo
    echo -e "${YELLOW}Grafana Admin Password:${NC}"
    echo -e "  Variable: ${GREEN}GRAFANA_ADMIN_PASSWORD${NC}"
    echo -e "  Value:    ${GREEN}$GRAFANA_ADMIN_PASSWORD${NC}"
    echo -e "  Login:    ${GREEN}admin${NC} / ${GREEN}$GRAFANA_ADMIN_PASSWORD${NC}"
    echo
    echo -e "${YELLOW}Grafana Secret Key:${NC}"
    echo -e "  Variable: ${GREEN}GRAFANA_SECRET_KEY${NC}"
    echo -e "  Value:    ${GREEN}$GRAFANA_SECRET_KEY${NC}"
    echo
    echo -e "${BLUE}═══════════════════════════════════════════════════════════${NC}"
    echo
    
    print_warning "IMPORTANT SECURITY NOTES:"
    echo "  • These credentials have been saved to your .env file"
    echo "  • Keep your .env file secure and never commit it to version control"
    echo "  • A backup of your previous .env file was created if it existed"
    echo "  • Use these credentials to access your services:"
    echo "    - Database: Use DB_PASSWORD for PostgreSQL connection"
    echo "    - Grafana: Login with admin / GRAFANA_ADMIN_PASSWORD"
    echo
    
    print_status "Next steps:"
    echo "  1. Review the generated .env file"
    echo "  2. Start your services with: docker-compose up -d"
    echo "  3. Access Grafana at http://localhost:3001"
    echo "  4. Store these credentials securely"
    echo
    
    print_success "Password generation script completed successfully!"
}

# Script usage information
show_help() {
    echo "Information Broker - Password Generation Script"
    echo ""
    echo "Usage: $0 [options]"
    echo ""
    echo "This script generates secure passwords for:"
    echo "  • PostgreSQL database (DB_PASSWORD)"
    echo "  • Grafana admin user (GRAFANA_ADMIN_PASSWORD)"  
    echo "  • Grafana secret key (GRAFANA_SECRET_KEY)"
    echo ""
    echo "Options:"
    echo "  -h, --help     Show this help message"
    echo ""
    echo "The script will:"
    echo "  1. Create a backup of your existing .env file"
    echo "  2. Create .env from .env.example if needed"
    echo "  3. Generate secure random passwords"
    echo "  4. Update the .env file with new credentials"
    echo "  5. Display the generated credentials"
    echo ""
}

# Parse command line arguments
case "${1:-}" in
    -h|--help)
        show_help
        exit 0
        ;;
    "")
        main
        ;;
    *)
        print_error "Unknown option: $1"
        echo "Use -h or --help for usage information."
        exit 1
        ;;
esac