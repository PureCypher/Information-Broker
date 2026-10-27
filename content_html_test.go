package main

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// TestExtractMainContentCapturesHTMLStructureAndImages verifies that page
// extraction keeps the article body's original structure (headings, lists,
// images) in the HTML form while the text form stays tag-free. Regression
// for the "shows how it is on the website" rendering requirement.
func TestExtractMainContentCapturesHTMLStructureAndImages(t *testing.T) {
	page := `<html><body>
		<nav><a href="/">Home</a> <a href="/cat">Category</a> <a href="/about">About</a></nav>
		<article>
			<h2>Zero-day exploited in the wild</h2>
			<p>` + strings.Repeat("Researchers published detailed analysis of the exploit chain. ", 6) + `</p>
			<figure><img src="https://cdn.example.com/photo.jpg" alt="Attack diagram" width="800" height="450"><figcaption>Figure 1</figcaption></figure>
			<p>` + strings.Repeat("Additional technical detail follows in the second paragraph. ", 6) + `</p>
			<h3>Indicators of compromise</h3>
			<ul><li>Domain: attacker.example</li><li>IP: 203.0.113.9</li></ul>
		</article>
		<footer>© Example News</footer>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	text, bodyHTML := extractMainContent(doc)
	if strings.Contains(text, "<img") || strings.Contains(text, "<h2") || strings.Contains(text, "<li>") {
		t.Fatalf("text form should be tag-free, got: %s", text)
	}
	for _, want := range []string{"<img", "cdn.example.com/photo.jpg", "<h2>", "<h3>", "<ul>", "<li>", "<figcaption>"} {
		if !strings.Contains(bodyHTML, want) {
			t.Fatalf("html form missing %q, got: %s", want, bodyHTML)
		}
	}
	// Chrome (nav/footer) must not leak into either form.
	for _, chrome := range []string{"Home", "Category", "Example News"} {
		if strings.Contains(text, chrome) {
			t.Fatalf("chrome %q leaked into text form: %s", chrome, text)
		}
	}
}

// TestExtractMainContentSkipsHTMLForTinyBodies: a one-paragraph body adds
// nothing over the plain text — don't promote it to HTML (keeps old rows
// semantics for feed-description snippets).
func TestExtractMainContentSkipsHTMLForTinyBodies(t *testing.T) {
	page := `<html><body><main><p>Short note.</p></main></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	text, bodyHTML := extractMainContent(doc)
	if text != "Short note." {
		t.Fatalf("text = %q, want %q", text, "Short note.")
	}
	if bodyHTML != "" {
		t.Fatalf("tiny body should not carry HTML, got %q", bodyHTML)
	}
}

func TestLooksLikeHTML(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"<p>Paragraph</p>", true},
		{"<div class=\"x\">Body</div>", true},
		{"<IMG SRC=\"https://x/i.jpg\">", true},
		{"<br>", true},
		{"<figure><img src=\"https://x/i.jpg\"></figure>", true},
		{"Plain sentence with a < b comparison.", false},
		{"", false},
		{"The <strong>term</strong> matters.", true},
		{"It's <5 units wide.", false},
		{"<table><tr><td>1</td></tr></table>", true},
		{"<h2>Heading</h2>", true},
		{"<em>emphasis</em>", true},
	}
	for _, c := range cases {
		if got := looksLikeHTML(c.in); got != c.want {
			t.Errorf("looksLikeHTML(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestStripTags(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<p>Line <strong>bold</strong> and <em>ital</em>.</p>", "Line bold and ital."},
		{"No tags here", "No tags here"},
		{"<script>alert(1)</script>visible", "visible"},
		{"<div><div>Two</div> levels</div>", "Two levels"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripTags(c.in); got != c.want {
			t.Errorf("stripTags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
