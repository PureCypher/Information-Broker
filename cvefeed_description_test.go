package main

import (
	"strings"
	"testing"
)

func TestCleanCvefeedDescription(t *testing.T) {
	// Real-world shape (decoded by gofeed): <strong>label</strong>value<br />
	// separated lines.
	decoded := "\n  <strong>CVE ID :</strong>CVE-2026-77924\n  <br />\n  <strong>Published :</strong> Oct. 26, 2026, 10:17 p.m. | 17 minutes ago\n  <br />\n  <strong>Description :</strong>Rejected reason: withdrawn.\n  <br />\n  <strong>Severity:</strong> 0.0 | NA\n  <br />\n  Visit the link for more details...\n"
	got := cleanCvefeedDescription(decoded)
	for _, want := range []string{"CVE ID :CVE-2026-77924", "Published :", "Severity: 0.0 | NA", "Visit the link for more details..."} {
		if !strings.Contains(got, want) {
			t.Errorf("cleanCvefeedDescription() missing %q in:\n%s", want, got)
		}
	}
	if strings.ContainsAny(got, "<>") {
		t.Errorf("cleanCvefeedDescription() left HTML tags:\n%s", got)
	}
	if strings.Count(got, "\n\n") > 0 {
		t.Errorf("cleanCvefeedDescription() left blank-line runs:\n%q", got)
	}
}

func TestCleanCvefeedDescriptionEncodedForm(t *testing.T) {
	// Tolerate the still-encoded (un-decoded) variant too.
	encoded := "&lt;strong&gt;CVE ID :&lt;/strong&gt;CVE-2026-1&lt;br /&gt;&lt;strong&gt;Description :&lt;/strong&gt;A flaw.&lt;br /&gt;"
	got := cleanCvefeedDescription(encoded)
	if strings.ContainsAny(got, "<>") {
		t.Errorf("cleanCvefeedDescription() left HTML tags in encoded form:\n%s", got)
	}
	if !strings.Contains(got, "A flaw.") {
		t.Errorf("cleanCvefeedDescription() lost body text:\n%s", got)
	}
}

func TestCleanCvefeedDescriptionEmpty(t *testing.T) {
	if got := cleanCvefeedDescription(""); got != "" {
		t.Errorf("empty description should yield empty string, got %q", got)
	}
}
