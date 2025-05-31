package main

import (
	"information-broker/config"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

func TestArticleDateFiltering(t *testing.T) {
	// Create test configuration with cutoff date of 2025-05-31T00:00:00Z
	cfg := &config.Config{
		App: config.AppConfig{
			ArticleCutoffDate: time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC),
			InitiationDate:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	// We'll test the date logic directly without creating a monitor

	tests := []struct {
		name           string
		publishDate    *time.Time
		expectedResult bool
		description    string
	}{
		{
			name:           "Article published before cutoff date",
			publishDate:    timePtr(time.Date(2025, 5, 30, 23, 59, 59, 0, time.UTC)),
			expectedResult: false,
			description:    "Should be filtered out - published before 2025-05-31T00:00:00Z",
		},
		{
			name:           "Article published exactly at cutoff date",
			publishDate:    timePtr(time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC)),
			expectedResult: true,
			description:    "Should pass - published exactly at 2025-05-31T00:00:00Z",
		},
		{
			name:           "Article published after cutoff date",
			publishDate:    timePtr(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)),
			expectedResult: true,
			description:    "Should pass - published after cutoff date",
		},
		{
			name:           "Article published today",
			publishDate:    timePtr(time.Now().UTC()),
			expectedResult: true,
			description:    "Should pass - published today",
		},
		{
			name:           "Article with different timezone before cutoff",
			publishDate:    timePtr(time.Date(2025, 5, 30, 18, 0, 0, 0, time.FixedZone("EST", -5*60*60))),
			expectedResult: false,
			description:    "Should be filtered out - 6PM EST on May 30 is 11PM UTC on May 30, before cutoff",
		},
		{
			name:           "Article with different timezone after cutoff",
			publishDate:    timePtr(time.Date(2025, 5, 31, 1, 0, 0, 0, time.FixedZone("CET", 1*60*60))),
			expectedResult: true,
			description:    "Should pass - 1AM CET on May 31 is 00:00 UTC on May 31",
		},
		{
			name:           "Article with no publish date",
			publishDate:    nil,
			expectedResult: false,
			description:    "Should be filtered out - no publish date available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock gofeed.Item
			item := &gofeed.Item{
				Title:           "Test Article: " + tt.name,
				Link:            "https://example.com/article-" + tt.name,
				PublishedParsed: tt.publishDate,
			}

			// Test the date filtering logic (extracted from processArticle)
			result := shouldProcessArticle(item, cfg)

			if result != tt.expectedResult {
				t.Errorf("Test %s failed: expected %v, got %v. %s",
					tt.name, tt.expectedResult, result, tt.description)
			}

			// Log the test case for manual verification
			if tt.publishDate != nil {
				t.Logf("Test %s: Published at %s (UTC: %s), Expected: %v, Got: %v",
					tt.name,
					tt.publishDate.Format("2006-01-02T15:04:05Z07:00"),
					tt.publishDate.UTC().Format("2006-01-02T15:04:05Z"),
					tt.expectedResult,
					result)
			} else {
				t.Logf("Test %s: No publish date, Expected: %v, Got: %v",
					tt.name, tt.expectedResult, result)
			}
		})
	}
}

// shouldProcessArticle extracts the date filtering logic for testing
func shouldProcessArticle(item *gofeed.Item, cfg *config.Config) bool {
	if item.Link == "" {
		return false
	}

	// Parse and normalize the publish date to UTC
	var publishDate time.Time
	if item.PublishedParsed != nil {
		publishDate = item.PublishedParsed.UTC()
	} else {
		// If no publish date is available, skip the article as per requirements
		return false
	}

	// Check publication date against the cutoff date (2025-05-31T00:00:00Z)
	cutoffDate := cfg.App.ArticleCutoffDate.UTC()
	if publishDate.Before(cutoffDate) {
		return false
	}

	// Check publication date against initiation date (keep existing logic for backward compatibility)
	if publishDate.Before(cfg.App.InitiationDate) {
		return false
	}

	return true
}

// timePtr returns a pointer to a time.Time value
func timePtr(t time.Time) *time.Time {
	return &t
}

// Benchmark test for date filtering performance
func BenchmarkDateFiltering(b *testing.B) {
	cfg := &config.Config{
		App: config.AppConfig{
			ArticleCutoffDate: time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC),
			InitiationDate:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	// Create test articles with various dates
	testArticles := []*gofeed.Item{
		{
			Link:            "https://example.com/old-article",
			PublishedParsed: timePtr(time.Date(2025, 5, 30, 12, 0, 0, 0, time.UTC)),
		},
		{
			Link:            "https://example.com/new-article",
			PublishedParsed: timePtr(time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)),
		},
		{
			Link:            "https://example.com/cutoff-article",
			PublishedParsed: timePtr(time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC)),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, article := range testArticles {
			shouldProcessArticle(article, cfg)
		}
	}
}

// TestTimezoneHandling specifically tests timezone-agnostic comparison
func TestTimezoneHandling(t *testing.T) {
	cfg := &config.Config{
		App: config.AppConfig{
			ArticleCutoffDate: time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC),
			InitiationDate:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	timezones := []struct {
		name     string
		timezone *time.Location
		hour     int
		day      int
		expected bool
	}{
		{"UTC", time.UTC, 0, 31, true},                                 // 12AM UTC May 31 = 12AM UTC May 31
		{"EST_before", time.FixedZone("EST", -5*60*60), 18, 30, false}, // 6PM EST May 30 = 11PM UTC May 30
		{"EST_at", time.FixedZone("EST", -5*60*60), 19, 30, true},      // 7PM EST May 30 = 12AM UTC May 31
		{"CET", time.FixedZone("CET", 1*60*60), 1, 31, true},           // 1AM CET May 31 = 12AM UTC May 31
		{"PST_before", time.FixedZone("PST", -8*60*60), 15, 30, false}, // 3PM PST May 30 = 11PM UTC May 30
		{"PST_at", time.FixedZone("PST", -8*60*60), 16, 30, true},      // 4PM PST May 30 = 12AM UTC May 31
		{"JST", time.FixedZone("JST", 9*60*60), 9, 31, true},           // 9AM JST May 31 = 12AM UTC May 31
	}

	for _, tz := range timezones {
		t.Run(tz.name, func(t *testing.T) {
			publishTime := time.Date(2025, 5, tz.day, tz.hour, 0, 0, 0, tz.timezone)

			item := &gofeed.Item{
				Link:            "https://example.com/tz-test",
				PublishedParsed: &publishTime,
			}

			result := shouldProcessArticle(item, cfg)

			if result != tz.expected {
				t.Errorf("Timezone %s test failed: time %s (UTC: %s) expected %v, got %v",
					tz.name,
					publishTime.Format("2006-01-02T15:04:05Z07:00"),
					publishTime.UTC().Format("2006-01-02T15:04:05Z"),
					tz.expected,
					result)
			}
		})
	}
}
