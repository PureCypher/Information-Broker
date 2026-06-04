package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A byte-slice truncation that cuts through a multi-byte rune (the production
// bug behind the DB "invalid byte sequence for encoding UTF8" save failures)
// must never produce an invalid string.
func TestSafeTruncateNeverProducesInvalidUTF8(t *testing.T) {
	// "…" (U+2026) encodes as 0xe2 0x80 0xa6; cutting inside it is invalid.
	s := "threat alert… details end"
	for n := 0; n <= len(s)+2; n++ {
		got := safeTruncate(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("safeTruncate(%q, %d) = %q is not valid UTF-8", s, n, got)
		}
		if len(got) > n {
			t.Fatalf("safeTruncate(%q, %d) returned %d bytes, more than requested", s, n, len(got))
		}
	}
}

// Reproduces the exact production failure shape: a dangling 0xe2 lead byte
// followed by an appended "..." — Postgres rejected "0xe2 0x2e 0x2e".
func TestSanitizeUTF8FixesTruncatedLeadByte(t *testing.T) {
	bad := "Cybersecurity report" + string([]byte{0xe2}) + "..."
	if utf8.ValidString(bad) {
		t.Fatal("test setup: expected invalid UTF-8 input")
	}
	got := sanitizeUTF8(bad)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeUTF8 returned invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, "Cybersecurity report") {
		t.Fatalf("sanitizeUTF8 mangled the valid prefix: %q", got)
	}
}

// Valid strings (including multi-byte ones) must pass through unchanged.
func TestSanitizeUTF8PreservesValid(t *testing.T) {
	for _, s := range []string{"", "plain ascii", "café — déjà vu …", "日本語"} {
		if got := sanitizeUTF8(s); got != s {
			t.Fatalf("sanitizeUTF8(%q) = %q, altered a valid string", s, got)
		}
	}
}
