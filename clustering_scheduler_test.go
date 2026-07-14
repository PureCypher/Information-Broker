package main

import "testing"

func TestIsIdleFromStats(t *testing.T) {
	tests := []struct {
		name       string
		queueDepth int
		current    bool
		want       bool
	}{
		{"empty queue, nothing in flight", 0, false, true},
		{"queue has pending work", 3, false, false},
		{"queue empty but a request is in flight", 0, true, false},
		{"both busy", 2, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := map[string]interface{}{
				"queue_depth":     tt.queueDepth,
				"current_request": tt.current,
			}
			if got := isIdleFromStats(stats); got != tt.want {
				t.Errorf("isIdleFromStats(%+v) = %v, want %v", stats, got, tt.want)
			}
		})
	}
}
