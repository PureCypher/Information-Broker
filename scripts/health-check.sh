#!/bin/sh
# Health check script for Information Broker application

# Set default values
HOST=${HOST:-localhost}
PORT=${PORT:-8080}
ENDPOINT=${ENDPOINT:-/health}

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Try different HTTP clients in order of preference
if command_exists curl; then
    HTTP_CLIENT="curl -f -s --max-time 10"
elif command_exists wget; then
    HTTP_CLIENT="wget -q --timeout=10 --tries=1 -O-"
else
    echo "ERROR: Neither curl nor wget found"
    exit 1
fi

# Perform health check
URL="http://${HOST}:${PORT}${ENDPOINT}"

if $HTTP_CLIENT "$URL" >/dev/null 2>&1; then
    echo "OK: Application is healthy"
    exit 0
else
    echo "ERROR: Health check failed for $URL"
    exit 1
fi