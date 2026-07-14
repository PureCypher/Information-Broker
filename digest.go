package main

import "time"

// digestWindowOrDefault maps a digest range parameter to a lookback window.
// Unknown or empty values fall back to the daily (24h) window — same
// whitelist-and-normalize style as buildArticlesQuery's sort param.
func digestWindowOrDefault(rangeParam string) time.Duration {
	switch rangeParam {
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}
