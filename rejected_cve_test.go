package main

import "testing"

func TestIsRejectedCVE(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"CVE-2026-66045 - Rejected reason: This CVE ID has been rejected or reserved by the CVE Numbering Authority.", true},
		{"CVE-2026-9999 - REJECTED REASON: duplicate of CVE-2026-8888", true},
		{"CVE-2026-1234 - Reserved", true},
		{"CVE-2026-116748 - Transmission 4.0.0 before 4.1.3 Permissive CORS Leaks RPC Anti-CSRF Token", false},
		{"Rejected reason: This CVE ID has been rejected", false}, // not a CVE-prefixed title
		{"Sweden bans ChatGPT and Claude for government use", false},
		{"", false},
		{"cve-2026-1 - rejected reason: x", true},
	}
	for _, c := range cases {
		if got := isRejectedCVE(c.title); got != c.want {
			t.Errorf("isRejectedCVE(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}
