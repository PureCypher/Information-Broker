package main

import (
	"testing"
	"time"
)

func TestDigestWindowOrDefault(t *testing.T) {
	tests := []struct {
		rangeParam string
		want       time.Duration
	}{
		{"daily", 24 * time.Hour},
		{"weekly", 7 * 24 * time.Hour},
		{"monthly", 30 * 24 * time.Hour},
		{"", 24 * time.Hour},
		{"garbage", 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := digestWindowOrDefault(tt.rangeParam); got != tt.want {
			t.Errorf("digestWindowOrDefault(%q) = %v, want %v", tt.rangeParam, got, tt.want)
		}
	}
}
