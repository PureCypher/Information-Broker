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

	got, _ := extractMainContent(doc)
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

	got, _ := extractMainContent(doc)
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

	got, _ := extractMainContent(doc)
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

	got, _ := extractMainContent(doc)
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

	got, _ := extractMainContent(doc)
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
	got, _ := extractMainContent(doc)
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

	got, _ := extractMainContent(doc)
	if !strings.Contains(got, "GPT-5.6 has deleted users' files") {
		t.Fatalf("did not extract the k5a-article body; got %d chars: %.120q", len(got), got)
	}
	if strings.Contains(got, "RAMpocalypse") {
		t.Fatalf("extracted a teaser card instead of the article body: %.120q", got)
	}
}

func TestExtractMainContentStripsEmbeddedHeaderChrome(t *testing.T) {
	// Live on arstechnica.com: the real <article> opens with its own
	// <header> — kicker, headline, byline, publish date, category icon —
	// all inside the container the longest-match rule selects. The stored
	// article HTML must not duplicate the title the page shell already
	// renders, so the embedded header subtree is stripped at capture time.
	html := `<html><body>
		<article class="h-entry post-1">
			<header>
				<div class="upper-deck"><span>SECURITY</span></div>
				<h1>Ransomware Group Weaponizes VPN Zero-Day</h1>
				<div class="byline">By Jane Doe | Oct 27, 2026</div>
			</header>
			<p>` + strings.Repeat("The genuine article body prose about the intrusion. ", 25) + `</p>
		</article>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	text, bodyHTML := extractMainContent(doc)
	for _, chrome := range []string{"Ransomware Group Weaponizes VPN Zero-Day", "By Jane Doe", "SECURITY"} {
		if strings.Contains(text, chrome) {
			t.Fatalf("text kept embedded-header chrome %q: %s", chrome, text[:min(200, len(text))])
		}
		if strings.Contains(bodyHTML, chrome) {
			t.Fatalf("bodyHTML kept embedded-header chrome %q: %s", chrome, bodyHTML[:min(300, len(bodyHTML))])
		}
	}
	if !strings.Contains(text, "genuine article body prose") {
		t.Fatalf("article body text lost: %s", text)
	}
	if !strings.Contains(bodyHTML, "genuine article body prose") {
		t.Fatalf("article body HTML lost: %s", bodyHTML)
	}
}

func TestExtractMainContentSecurityAffairsArticleDetailsBlock(t *testing.T) {
	// Real-world case found live on securityaffairs.com (article id 90330):
	// the real post body lives in <div class="article-details-block">, which
	// matches none of the precise/fallback selectors, so extraction fell all
	// the way back to <body> — sweeping in the "You might also like"
	// (.recommended-post) and "Recent Articles" (.sngl-article/.top-article)
	// widgets, the header headline marquee, the newsletter form, and the
	// cookie-consent banner into full_content (and the summary generated
	// from it). Adding .article-details-block to the precise tier makes the
	// body win on length; the widget-class removals keep the cookie banner
	// and marquee out of any future body fallback too.
	body := strings.Repeat("CISA added the PHP flaws PHP2Shell to its Known Exploited Vulnerabilities catalog. ", 20)
	html := `<html><body>
		<header>
			<div class="text-slider-wrap"><p><a href="/201840">U.S. CISA adds PHP flaws PHP2Shell to its Known Exploited Vulnerabilities catalog</a></p><p><a href="/201827">Attackers Take Control of Dahua Cameras</a></p></div>
		</header>
		<div class="article-details-block wow fadeInUp">
			<h2>U.S. CISA adds PHP flaws PHP2Shell to its KEV catalog</h2>
			<p>` + body + `</p>
		</div>
		<div class="recommended-post wow fadeInUp">
			<h3>You might also like</h3>
			<article class="sngl-article"><h6><a href="/201786">AI-Generated Fake Bank Calls and NFC App Enabled Fraud</a></h6><p class="cat-date">Cyber Crime / October 26, 2026</p></article>
		</div>
		<div class="top-article">
			<div class="sngl-article-recent"><h6><a href="/201795">Magento Campaign Infects 953 Online Stores</a></h6><p class="cat-date">Malware / October 26, 2026</p></div>
		</div>
		<div class="cli-bar-popup">We use cookies on our website to give you the most relevant experience by remembering your preferences and repeat visits.</div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc)
	if strings.Contains(got, "You might also like") || strings.Contains(got, "Fake Bank Calls") {
		t.Fatalf("recommended-post widget leaked into extracted content: %.200q", got)
	}
	if strings.Contains(got, "Magento Campaign") {
		t.Fatalf("top-article recent widget leaked into extracted content: %.200q", got)
	}
	if strings.Contains(got, "We use cookies") {
		t.Fatalf("cookie banner leaked into extracted content: %.200q", got)
	}
	if strings.Contains(got, "Attackers Take Control of Dahua") {
		t.Fatalf("header marquee leaked into extracted content: %.200q", got)
	}
	if !strings.Contains(got, "Known Exploited Vulnerabilities catalog") {
		t.Fatalf("expected the .article-details-block body, got: %.200q", got)
	}
}

func TestExtractMainContentTechradarFuturePlcWidgets(t *testing.T) {
	// Real-world case found live on techradar.com (2026 template, article
	// id 90215): the prose paragraphs sit directly inside <article>, which
	// also contains the share/follow rail (#utility-bar), a video carousel,
	// "more from" link rivers, the author bio and comment embeds — all
	// tagged data-component-name="...". The <article> match swept them in:
	// the stored content opened with the share rail ("Share this article",
	// "Join the conversation", "Follow us") and ended with the author bio,
	// and the summary generated from it described newsletter subscriptions.
	// Widgets are stripped before selector ranking so <article> is clean.
	prose := strings.Repeat("Microsoft has confirmed it recently fixed its highest ever vulnerability in ASP.NET Core. ", 10)
	html := `<html><body>
		<nav><a href="/features">Features</a> <a href="/reviews">Reviews</a></nav>
		<article>
			<div id="article-body" class="text-copy bodyCopy auto">
				<div data-analytics-id="utility-bar" data-component-name="UtilityBar" class="flex justify-between">
					<button id="utilitybar__button--share" aria-label="Share this article">Share this article</button>
					<a class="button-social" href="https://twitter.com/techradar" aria-label="Follow us">Follow us</a>
				</div>
				<p>` + prose + `</p>
				<div class="my-6 w-full" data-component-name="JwPlayer:Carousel">
					<div data-jwp-carousel-header="">Latest Videos From <span class="jwp-carousel-brand">TechRadar</span></div>
				</div>
			</div>
			<div data-component-name="Recirculation:ArticleRiver">
				<a href="/pro/best-vpn">Best VPNs</a> <a href="/pro/best-antivirus">Best antivirus</a>
			</div>
			<div data-component-name="Slice:AuthorBio">
				<div class="author__social">Sead is a seasoned freelance journalist based in Sarajevo.</div>
			</div>
			<div data-component-name="Viafora:Comments">Join the conversation</div>
		</article>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc)
	for _, junk := range []string{"Share this article", "Follow us", "Join the conversation", "Latest Videos From", "Best VPNs", "freelance journalist", "Features", "Reviews"} {
		if strings.Contains(got, junk) {
			t.Fatalf("widget chrome %q leaked into extracted content: %.300q", junk, got)
		}
	}
	if !strings.Contains(got, "highest ever vulnerability in ASP.NET Core") {
		t.Fatalf("expected the article prose, got: %.300q", got)
	}
}
