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

func TestExtractMainContentStripsScriptAndStyleTags(t *testing.T) {
	// goquery's .Text() includes the raw source text of <script>/<style>
	// elements (browsers don't render them, but they're still DOM text
	// nodes) — found live via the ar-widget plugin's inline <style> block
	// leaking raw CSS rules into the stored article content.
	html := `<html><body>
		<div class="entry-content">
			<style>#ar-widget{margin:0 0 2rem;font-family:sans-serif;}</style>
			<script>console.log("tracking pixel fired");</script>
			<p>` + strings.Repeat("The real article prose that actually matters here. ", 20) + `</p>
		</div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := extractMainContent(doc)
	if strings.Contains(got, "margin:0") || strings.Contains(got, "console.log") {
		t.Fatalf("script/style raw text leaked into extracted content: %s", got)
	}
	if !strings.Contains(got, "real article prose") {
		t.Fatalf("expected the real article prose to remain, got: %s", got)
	}
}

func TestExtractMainContentFindsPageContentWrapper(t *testing.T) {
	// Real-world case found live: cvefeed.io (a Bootstrap admin-dashboard
	// theme, 6258 articles -- the largest single feed in the corpus) has no
	// <main> tag and no <article>/.entry-content/.post-content wrapper. Its
	// real per-CVE description lives in a .page-content div; the header/nav
	// (a search button placeholder "CVE ID, Product, Vendor ...", a
	// "Pricing" nav link) comes first in DOM order, outside that wrapper.
	// Falling through to <body> pulled in that header/nav chrome ahead of
	// the real content, and Ollama's summary ended up describing the site's
	// generic search feature instead of the specific CVE.
	html := `<html><body>
		<header>
			<button>CVE ID, Product, Vendor ...</button>
			<a href="/pricing">Pricing</a>
		</header>
		<div class="page-content">
			<p>` + strings.Repeat("CVE-2026-47303 is an elevation of privilege vulnerability in ASP.NET Core. ", 10) + `</p>
		</div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := extractMainContent(doc)
	if strings.Contains(got, "CVE ID, Product, Vendor") || strings.Contains(got, "Pricing") {
		t.Fatalf("header/nav chrome leaked into extracted content: %s", got)
	}
	if !strings.Contains(got, "elevation of privilege vulnerability in ASP.NET Core") {
		t.Fatalf("expected the .page-content text, got: %s", got)
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

func TestExtractMainContentTheRegisterK5aArticle(t *testing.T) {
	// The Register (theregister.com) puts the article body in
	// <section class="main article k5a-article" id="main">, while short
	// <article class="column ..."> teaser cards for other stories sit elsewhere
	// on the page. Without a selector matching k5a-article, only the generic
	// "article" tag selector matched — hitting the teaser cards — so the stored
	// content was an unrelated teaser instead of the story.
	body := strings.Repeat("OpenAI has confirmed reports that GPT-5.6 has deleted users' files without authorization. ", 12)
	html := `<html><body><main class="pageWidth">
		<section class="main article k5a-article" id="main">
			<h1>OpenAI admits GPT-5.6 occasionally deletes files</h1>
			<p>` + body + `</p>
		</section>
		<div class="related-stories">
			<article class="column small-12" data-tag="china"><h2>Chinese memory ban would cut off RAMpocalypse relief</h2></article>
			<article class="column small-12" data-tag="ai"><h2>Thinking Machines first open weights model alternative</h2></article>
			<article class="column small-12" data-tag="paas"><h2>Amazon Web Services most vocal customer now runs EC2</h2></article>
		</div>
	</main></body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := extractMainContent(doc)
	if !strings.Contains(got, "GPT-5.6 has deleted users' files") {
		t.Fatalf("did not extract the k5a-article body; got %d chars: %.120q", len(got), got)
	}
	if strings.Contains(got, "RAMpocalypse") {
		t.Fatalf("extracted a teaser card instead of the article body: %.120q", got)
	}
}
