# Multi-stage build for Go application
FROM golang:1.26-alpine AS builder

# Install dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Some publishers serve incomplete TLS chains (e.g. infosecurity-magazine.com
# omits its GlobalSign GCC R46 intermediate, fetched 2026-10-28 from
# http://secure.globalsign.com/cacert/gsgccr46ovtlsca2025.crt — Go/curl do not
# chase AIA "CA Issuers" URLs like browsers do). Appending the intermediate to
# the system bundle lets x509 verification build the chain.
RUN cat /app/certs/*.crt >> /etc/ssl/certs/ca-certificates.crt

# Build the application with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o information-broker .

# Final stage - minimal runtime image
FROM scratch

# Copy CA certificates and timezone data from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the binary
COPY --from=builder /app/information-broker /app/information-broker

# Copy feeds file
COPY feeds.txt /app/feeds.txt

# Create non-root user (using numeric IDs for scratch image)
USER 65534:65534

# Set working directory
WORKDIR /app

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/app/information-broker", "-health-check"] || exit 1

# Run the application
CMD ["./information-broker"]