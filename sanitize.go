package main

import (
	"strings"
	"unicode/utf8"
)

// sanitizeUTF8 returns s with any invalid UTF-8 byte sequences stripped, so the
// value is safe to store in PostgreSQL's strict UTF-8 columns (an invalid byte
// makes the whole INSERT fail with "invalid byte sequence for encoding UTF8").
// Already-valid strings are returned unchanged without allocating.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

// safeTruncate truncates s to at most maxBytes bytes without splitting a
// multi-byte UTF-8 character. Plain byte-slicing (s[:n]) can cut through the
// middle of a rune and leave an invalid trailing byte; this backs the cut point
// off to the nearest rune boundary instead.
func safeTruncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// maxBytes < len(s) here, so s[maxBytes] is a valid index. Walk back while
	// it points at a UTF-8 continuation byte (0x80-0xBF) to find a boundary.
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
