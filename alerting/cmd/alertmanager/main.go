package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v2"
)

// Config represents the alertmanager configuration
type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`

	Prometheus struct {
		URL string `yaml:"url"`
	} `yaml:"prometheus"`

	Webhooks struct {
		Discord struct {
			URL     string `yaml:"url"`
			Enabled bool   `yaml:"enabled"`
		} `yaml:"discord"`

		Slack struct {
			URL     string `yaml:"url"`
			Enabled bool   `yaml:"enabled"`
		} `yaml:"slack"`
	} `yaml:"webhooks"`

	Rules []AlertRule `yaml:"rules"`
}

// AlertRule represents an alerting rule
type AlertRule struct {
	Name        string            `yaml:"name"`
	Query       string            `yaml:"query"`
	Threshold   float64           `yaml:"threshold"`
	Operator    string            `yaml:"operator"` // gt, lt, eq, ne
	Duration    string            `yaml:"duration"`
	Severity    string            `yaml:"severity"`
	Description string            `yaml:"description"`
	Labels      map[string]string `yaml:"labels"`
}

// Alert represents an active alert
type Alert struct {
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Severity    string            `json:"severity"`
	Description string            `json:"description"`
	Value       float64           `json:"value"`
	Threshold   float64           `json:"threshold"`
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"starts_at"`
	EndsAt      *time.Time        `json:"ends_at,omitempty"`
}

// PrometheusResponse represents Prometheus query response
type PrometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// AlertManager manages alerting rules and notifications
type AlertManager struct {
	config       Config
	activeAlerts map[string]*Alert
	httpClient   *http.Client
}

func main() {
	// Load configuration
	configFile := getEnv("CONFIG_FILE", "config.yaml")
	config, err := loadConfig(configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Override config with environment variables
	if discordURL := os.Getenv("DISCORD_WEBHOOK_URL"); discordURL != "" {
		config.Webhooks.Discord.URL = discordURL
		config.Webhooks.Discord.Enabled = true
	}

	if slackURL := os.Getenv("SLACK_WEBHOOK_URL"); slackURL != "" {
		config.Webhooks.Slack.URL = slackURL
		config.Webhooks.Slack.Enabled = true
	}

	if promURL := os.Getenv("PROMETHEUS_URL"); promURL != "" {
		config.Prometheus.URL = promURL
	}

	// Create alert manager
	am := &AlertManager{
		config:       *config,
		activeAlerts: make(map[string]*Alert),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Start HTTP server for health checks and status
	mux := http.NewServeMux()
	mux.HandleFunc("/health", am.healthHandler)
	mux.HandleFunc("/alerts", am.alertsHandler)
	mux.HandleFunc("/status", am.statusHandler)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.Server.Port),
		Handler: mux,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting alertmanager on port %d", config.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Start rule evaluation loop
	ctx, cancel := context.WithCancel(context.Background())
	go am.evaluateRules(ctx)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down alertmanager...")
	cancel()

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}

func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// Set defaults
	if config.Server.Port == 0 {
		config.Server.Port = 9093
	}
	if config.Prometheus.URL == "" {
		config.Prometheus.URL = "http://prometheus:9090"
	}

	return &config, nil
}

func (am *AlertManager) evaluateRules(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, rule := range am.config.Rules {
				am.evaluateRule(rule)
			}
		}
	}
}

func (am *AlertManager) evaluateRule(rule AlertRule) {
	// Query Prometheus
	url := fmt.Sprintf("%s/api/v1/query?query=%s", am.config.Prometheus.URL, rule.Query)
	resp, err := am.httpClient.Get(url)
	if err != nil {
		log.Printf("Failed to query Prometheus for rule %s: %v", rule.Name, err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read Prometheus response: %v", err)
		return
	}

	var promResp PrometheusResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		log.Printf("Failed to parse Prometheus response: %v", err)
		return
	}

	// Check if alert should fire
	for _, result := range promResp.Data.Result {
		if len(result.Value) < 2 {
			continue
		}

		value, ok := result.Value[1].(string)
		if !ok {
			continue
		}

		var numValue float64
		if _, err := fmt.Sscanf(value, "%f", &numValue); err != nil {
			continue
		}

		shouldAlert := false
		switch rule.Operator {
		case "gt":
			shouldAlert = numValue > rule.Threshold
		case "lt":
			shouldAlert = numValue < rule.Threshold
		case "eq":
			shouldAlert = numValue == rule.Threshold
		case "ne":
			shouldAlert = numValue != rule.Threshold
		}

		alertKey := rule.Name
		if shouldAlert {
			if _, exists := am.activeAlerts[alertKey]; !exists {
				// New alert
				alert := &Alert{
					Name:        rule.Name,
					Status:      "firing",
					Severity:    rule.Severity,
					Description: rule.Description,
					Value:       numValue,
					Threshold:   rule.Threshold,
					Labels:      rule.Labels,
					StartsAt:    time.Now(),
				}
				am.activeAlerts[alertKey] = alert
				am.sendAlert(alert)
				log.Printf("Alert fired: %s (value: %f, threshold: %f)", rule.Name, numValue, rule.Threshold)
			}
		} else {
			if alert, exists := am.activeAlerts[alertKey]; exists {
				// Alert resolved
				now := time.Now()
				alert.EndsAt = &now
				alert.Status = "resolved"
				am.sendAlert(alert)
				delete(am.activeAlerts, alertKey)
				log.Printf("Alert resolved: %s", rule.Name)
			}
		}
	}
}

func (am *AlertManager) sendAlert(alert *Alert) {
	if am.config.Webhooks.Discord.Enabled && am.config.Webhooks.Discord.URL != "" {
		am.sendDiscordAlert(alert)
	}

	if am.config.Webhooks.Slack.Enabled && am.config.Webhooks.Slack.URL != "" {
		am.sendSlackAlert(alert)
	}
}

func (am *AlertManager) sendDiscordAlert(alert *Alert) {
	color := 15158332 // Red for firing
	if alert.Status == "resolved" {
		color = 3066993 // Green for resolved
	}

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("Alert: %s", alert.Name),
				"description": alert.Description,
				"color":       color,
				"fields": []map[string]interface{}{
					{"name": "Status", "value": alert.Status, "inline": true},
					{"name": "Severity", "value": alert.Severity, "inline": true},
					{"name": "Value", "value": fmt.Sprintf("%.2f", alert.Value), "inline": true},
					{"name": "Threshold", "value": fmt.Sprintf("%.2f", alert.Threshold), "inline": true},
				},
				"timestamp": alert.StartsAt.Format(time.RFC3339),
			},
		},
	}

	am.sendWebhook(am.config.Webhooks.Discord.URL, payload)
}

func (am *AlertManager) sendSlackAlert(alert *Alert) {
	color := "danger"
	if alert.Status == "resolved" {
		color = "good"
	}

	payload := map[string]interface{}{
		"attachments": []map[string]interface{}{
			{
				"title": fmt.Sprintf("Alert: %s", alert.Name),
				"text":  alert.Description,
				"color": color,
				"fields": []map[string]interface{}{
					{"title": "Status", "value": alert.Status, "short": true},
					{"title": "Severity", "value": alert.Severity, "short": true},
					{"title": "Value", "value": fmt.Sprintf("%.2f", alert.Value), "short": true},
					{"title": "Threshold", "value": fmt.Sprintf("%.2f", alert.Threshold), "short": true},
				},
				"ts": alert.StartsAt.Unix(),
			},
		},
	}

	am.sendWebhook(am.config.Webhooks.Slack.URL, payload)
}

func (am *AlertManager) sendWebhook(url string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal webhook payload: %v", err)
		return
	}

	resp, err := am.httpClient.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Failed to send webhook: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("Webhook returned error status: %d", resp.StatusCode)
	}
}

func (am *AlertManager) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func (am *AlertManager) alertsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	alerts := make([]*Alert, 0, len(am.activeAlerts))
	for _, alert := range am.activeAlerts {
		alerts = append(alerts, alert)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"alerts": alerts,
		"count":  len(alerts),
	})
}

func (am *AlertManager) statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"active_alerts": len(am.activeAlerts),
		"rules_count":   len(am.config.Rules),
		"webhooks": map[string]bool{
			"discord": am.config.Webhooks.Discord.Enabled,
			"slack":   am.config.Webhooks.Slack.Enabled,
		},
	})
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
