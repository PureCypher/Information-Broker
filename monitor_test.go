package main

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestExtractMainContentPrefersLongestArticleMatch(t *testing.T) {
	// Reproduces a real production bug: hackread.com (and other sites using a
	// similar WordPress theme) render a "related/latest posts" grid of short
	// <article> teasers before the actual post body in DOM order.
	// doc.Find("article").First() picked the first (unrelated, short) teaser
	// instead of the real article, so the stored full_content — and the
	// summary generated from it — was about a completely different story.
	html := `<html><body>
		<div class="related-posts">
			<article class="post-1"><h2>Fake Interpol Investigation Emails Push Ransomware</h2><p>Short teaser about a different article entirely.</p></article>
			<article class="post-2"><h2>Another Related Post</h2><p>Another short teaser.</p></article>
		</div>
		<article class="post-main">
			<h1>Turning Indicators into Intelligence in OpenCTI with Criminal IP</h1>
			<p>` + strings.Repeat("This is the real, much longer main article body text. ", 20) + `</p>
		</article>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := extractMainContent(doc)
	if strings.Contains(got, "Fake Interpol") {
		t.Fatalf("extracted the wrong (short, unrelated) article teaser instead of the main article: %s", got)
	}
	if !strings.Contains(got, "real, much longer main article body") {
		t.Fatalf("expected the main article's own text, got: %s", got)
	}
}

func TestExtractMainContentPrefersLongerMatchFromLaterSelector(t *testing.T) {
	// Real-world case found live: hackread.com's related-post <article> cards
	// are short (a few hundred chars) but non-empty, while the real post body
	// lives in a .entry-content div that isn't wrapped in <article> at all.
	// The fix must compare candidates across the whole selector tier, not
	// stop at the first selector merely because it has any non-empty match.
	html := `<html><body>
		<article class="teaser-1"><p>Short unrelated teaser one.</p></article>
		<article class="teaser-2"><p>Short unrelated teaser two.</p></article>
		<div class="entry-content"><p>` + strings.Repeat("The real article body, much longer than any teaser card. ", 20) + `</p></div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := extractMainContent(doc)
	if !strings.Contains(got, "real article body") {
		t.Fatalf("expected the .entry-content text despite <article> matching first in priority order, got: %s", got)
	}
}

func TestExtractMainContentStripsAudioPlayerWidget(t *testing.T) {
	// Real-world case found live: hackread.com's actual post body is wrapped
	// in .entry-content, but that same container's FIRST child is a
	// "Listen to this article" audio-player widget (#ar-widget) whose own
	// text — playback controls, speed options, a long voice-selector list —
	// got included as if it were article prose, since .Text() walks all
	// descendants. The widget must be stripped before measuring/using text.
	html := `<html><body>
		<div class="entry-content">
			<div id="ar-widget">Listen to this article 0:00 Play 10s Speed 0.75x 1x 1.25x Voice Emma James Liam</div>
			<p>` + strings.Repeat("The real article prose that actually matters here. ", 20) + `</p>
		</div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := extractMainContent(doc)
	if strings.Contains(got, "Listen to this article") || strings.Contains(got, "Voice") {
		t.Fatalf("audio-player widget text leaked into extracted content: %s", got)
	}
	if !strings.Contains(got, "real article prose") {
		t.Fatalf("expected the real article prose to remain, got: %s", got)
	}
}

func TestExtractMainContentFallsBackToBody(t *testing.T) {
	html := `<html><body>Just some plain body text, no article/content wrapper.</body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := extractMainContent(doc)
	if !strings.Contains(got, "Just some plain body text") {
		t.Fatalf("expected body fallback text, got: %s", got)
	}
}
