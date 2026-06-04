package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// init runs before main(). When the binary is invoked as
// `information-broker -health-check`, it performs an HTTP liveness probe
// against the local /health endpoint and exits 0 (healthy) or 1 (unhealthy)
// instead of starting the application.
//
// This exists because the runtime image is built `FROM scratch` (see
// Dockerfile), which contains no shell and no wget/curl. A Docker HEALTHCHECK
// therefore cannot shell out to an HTTP client — the binary must probe itself.
// The Dockerfile and docker-compose.yml HEALTHCHECK both invoke this flag.
func init() {
	for _, a := range os.Args[1:] {
		switch a {
		case "-health-check", "--health-check", "-health-check=true":
			runHealthCheckAndExit()
		}
	}
}

// runHealthCheckAndExit probes http://127.0.0.1:<APP_PORT>/health and exits.
func runHealthCheckAndExit() {
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "health-check: request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "health-check: unhealthy (HTTP %d)\n", resp.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}
