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

	got, _ := extractMainContent(doc, "")
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

	got, _ := extractMainContent(doc, "")
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

	got, _ := extractMainContent(doc, "")
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

	got, _ := extractMainContent(doc, "")
	if strings.Contains(got, "margin:0") || strings.Contains(got, "console.log") {
		t.Fatalf("script/style raw text leaked into extracted content: %s", got)
	}
	if !strings.Contains(got, "real article prose") {
		t.Fatalf("expected the real article prose to remain, got: %s", got)
	}
}

func TestExtractMainContentStripsCybersecurityVenturesTemplateChrome(t *testing.T) {
	// Real-world case found live: cybersecurityventures.com (WordPress
	// "Bridge" theme) renders publisher template chrome inside the
	// <article> match — a .ccfic image-caption signature, the .post_info
	// byline row, Easy-Social-Share rails, the qode print button, a
	// PODCAST./RADIO./Contact-us standing footer, and a duplicate headline
	// in h2.entry_title that carries a "13 Oct" date badge ahead of the
	// title text (so exact/prefix headline rules never match it).
	const title = "GDC Festival of Gaming 2027 Discount Code: CYBERCRIME. Register Now."
	html := `<html><body>
		<article id="post-1">
			<div class="post_content_holder">
				<div class="post_image">
					<img src="https://cybersecurityventures.com/wp-content/uploads/photo.jpg" />
					<div class="ccfic"><span class="ccfic-text">Cybersecurity News. PHOTO: Cybercrime Magazine.</span></div>
				</div>
				<div class="post_text">
					<div class="post_text_inner">
						<h2 itemprop="name" class="entry_title"><span class="date entry_date updated">13 Oct</span> ` + title + `</h2>
						<div class="post_info">
							<span class="time">Posted at 08:42h</span>
							in <a href="https://cybersecurityventures.com/category/blogs/">Blogs</a>
							by <a class="post_author_link" href="https://cybersecurityventures.com/author/tfox/">Taylor Fox</a>
						</div>
						<div class="essb_links"><ul class="essb_links_list"><li class="essb_item">share</li></ul></div>
						<p>` + strings.Repeat("The real article prose that actually matters here. ", 20) + `</p>
						<ul><li><a href="https://cybersecurityventures.com/podcast/">PODCAST.</a> New episodes daily on the Cybercrime Magazine Podcast.</li></ul>
						<ul><li><a href="https://cybersecurityventures.com/radio/">RADIO.</a> Tune into WCYB Digital Radio at Cybercrime.Radio.</li></ul>
						<p><a href="https://cybersecurityventures.com/contact">Contact us</a> to send story tips, feedback and suggestions, and for sponsorship opportunities.</p>
					</div>
				</div>
			</div>
			<div class="icon_social_holder">
				<div class="qode_print"><a href="#" class="qode_print_page"><span>Print page</span></a></div>
			</div>
		</article>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, title)
	for _, leak := range []string{
		"Cybersecurity News. PHOTO",
		"Posted at 08:42h",
		"Taylor Fox",
		"13 Oct",
		"PODCAST.",
		"RADIO.",
		"story tips",
		"Print page",
		title,
	} {
		if strings.Contains(got, leak) {
			t.Fatalf("template chrome %q leaked into extracted content: %s", leak, got)
		}
	}
	if !strings.Contains(got, "real article prose") {
		t.Fatalf("expected the real article prose to remain, got: %s", got)
	}
}

func TestExtractMainContentPrefersTechRepublicAstroContainer(t *testing.T) {
	// Real-world case found live: techrepublic.com (Astro) has no <article>
	// element — the prose sits in <astro-article-contents><div
	// class="article-contents">…</div></astro-article-contents>, and the page
	// carries a mobile nav drawer ("Go Premium", "Popular Topics") that the
	// <body> fallback swept in as leading prose.
	html := `<html><body>
		<div class="flex flex-col">
			<div class="px-4 py-2 text-[10px] font-bold uppercase tracking-wider text-gray-400">Popular Topics</div>
			<a href="/topic/top-products/">Top Products</a>
			<a href="/premium/">Go Premium</a>
		</div>
		<div class="prose"><astro-article-contents><div class="article-contents" data-article-id="4377212"><div class="prose">
			<p>` + strings.Repeat("The real article prose that actually matters here. ", 20) + `</p>
		</div></div></astro-article-contents></div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	if strings.Contains(got, "Go Premium") || strings.Contains(got, "Popular Topics") {
		t.Fatalf("mobile-nav chrome leaked into extracted content: %s", got)
	}
	if !strings.Contains(got, "real article prose") {
		t.Fatalf("expected the real article prose to remain, got: %s", got)
	}
}

func TestExtractMainContentStripsNoscriptTrackingPixel(t *testing.T) {
	// Found live: techrepublic/cert.org/socprime pages carry a GT tag
	// manager <noscript> block; the html tokenizer parses noscript contents
	// as raw text, so the literal `<iframe src=...>` markup reached .Text()
	// and was stored as the opening line of the article prose.
	html := `<html><body>
		<article>
			<noscript><iframe src="https://www.googletagmanager.com/ns.html?id=GTM-57GHMWX" height="0" width="0" style="display:none;visibility:hidden"></iframe></noscript>
			<p>` + strings.Repeat("The real article prose that actually matters here. ", 20) + `</p>
		</article>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	if strings.Contains(got, "iframe") || strings.Contains(got, "googletagmanager") {
		t.Fatalf("noscript tracking-pixel markup leaked into extracted content: %s", got)
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

	got, _ := extractMainContent(doc, "")
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
	got, _ := extractMainContent(doc, "")
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

	got, _ := extractMainContent(doc, "")
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

	text, bodyHTML := extractMainContent(doc, "")
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

	got, _ := extractMainContent(doc, "")
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

func TestExtractMainContentSecurityAffairsSignatureFooter(t *testing.T) {
	// securityaffairs.com row 90330 (post-201840 fix): every article ends
	// with a standing author signature inside .article-details-block — a
	// "Follow me on Twitter: ..." paragraph, the author's name as a bare
	// link paragraph, the "(SecurityAffairs – <topics>)" line and a
	// .social-media share row ("facebook linkedin twitter"). These are
	// chrome: the reading page renders the author itself, and the share-row
	// link soup read as article text (stored tails ended with "facebook
	// linkedin twitter"). Class removals can't catch the signature
	// paragraphs (wp-block-paragraph is generic Gutenberg markup), so they
	// are matched by whole-paragraph text.
	body := strings.Repeat("CISA added the PHP flaws PHP2Shell to its Known Exploited Vulnerabilities catalog. ", 20)
	html := `<html><body>
		<div class="article-details-block">
			<h2>U.S. CISA adds PHP flaws PHP2Shell to its KEV catalog</h2>
			<p>` + body + `</p>
			<p>Follow me on Twitter: <a href="https://twitter.com/securityaffairs">@securityaffairs</a> and <a href="https://www.facebook.com/secaffairs">Facebook</a></p>
			<p><a href="/author/paganinip">Pierluigi Paganini</a></p>
			<p>(SecurityAffairs – hacking, CISA)</p>
			<div class="social-media"><a href="#">facebook</a> <a href="#">linkedin</a> <a href="#">twitter</a></div>
		</div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	for _, marker := range []string{"Follow me on Twitter", "Pierluigi Paganini", "SecurityAffairs", "facebook", "linkedin", "twitter"} {
		if strings.Contains(got, marker) {
			t.Fatalf("signature footer leaked into extracted content (%s): %.200q", marker, got)
		}
	}
	if !strings.Contains(got, "Known Exploited Vulnerabilities catalog") {
		t.Fatalf("article body lost: %.200q", got)
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

	got, _ := extractMainContent(doc, "")
	for _, junk := range []string{"Share this article", "Follow us", "Join the conversation", "Latest Videos From", "Best VPNs", "freelance journalist", "Features", "Reviews"} {
		if strings.Contains(got, junk) {
			t.Fatalf("widget chrome %q leaked into extracted content: %.300q", junk, got)
		}
	}
	if !strings.Contains(got, "highest ever vulnerability in ASP.NET Core") {
		t.Fatalf("expected the article prose, got: %.300q", got)
	}
}

func TestExtractMainContentTechradarMidBodyRecirculationWidget(t *testing.T) {
	// Real-world case found live on techradar.com (2026-10): a "You might
	// also like" recirculation widget is injected mid-article with elk-*
	// ids — an anchor, a section heading and a related-links <ul> — inside
	// the prose-bearing <div id="article-body">. Stripping only data-
	// component-name widgets left it in: the stored body switched from the
	// story to a shopping list mid-sentence.
	prose := strings.Repeat("Attackers are exploiting a patched WordPress plugin flaw to hijack administrator sessions. ", 10)
	html := `<html><body>
		<article>
			<div id="article-body" class="text-copy bodyCopy auto">
				<h2>Attackers hijack admin sessions via patched plugin flaw</h2>
				<p>` + prose + `</p>
				<a id="elk-you-might-also-like"></a>
				<h3 class="article-body__section" id="section-you-might-also-like"><span>You might also like</span></h3>
				<ul id="elk-5a30070b-608e-4fef-8979-ce3547028430">
					<li>Take a look at our picks for the <a href="https://www.techradar.com/best/best-malware-removal" target="_blank">best malware removal</a></li>
					<li><a href="https://www.techradar.com/best/best-vpn">Best VPN services</a></li>
				</ul>
				<p>Admins running the plugin on public sites should update immediately.</p>
				<a id="elk-collectionwidget-f205fc142f513c7f675ed59c688c1943"></a>
				<div data-link="https://example.com/deal" data-merchant-name="Aura Inc" data-link-text="View Deal">
					<div class="hawk-deal-widget-main">Save up to 68% on identity theft protection! <a href="https://example.com/deal">View Deal</a></div>
				</div>
				<div id="slice-container-newsletterForm-articleInbodyContent-Sq2cyejoE56hb6sXvAaQcZ" class="slice-container newsletter-inbodyContent-slice">
					<div class="newsletter-form__header">Are you a pro? Subscribe to our newsletter</div>
				</div>
			</div>
			<div data-component-name="PostArticleLinks">
				<div class="border-b border-b-black border-solid py-2.5">TOPICS</div>
				<nav><a href="/tag/routers">Routers</a> <a href="/tag/security">Security</a></nav>
			</div>
			<aside class="w-full" data-nosnippet data-mrf-recirculation="article-river-stacked">
				<div class="text-white py-4 px-3 mb-4 bg-[#333] uppercase font-bold custom-header-styles">Latest in Security</div>
				<div class="grid grid-cols-1 gap-4 mt-4 mb-5"><a href="/latest">More security news</a></div>
			</aside>
		</article>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	for _, junk := range []string{"You might also like", "best malware removal", "Best VPN services", "elk-",
		"View Deal", "identity theft protection", "Subscribe to our newsletter", "TOPICS", "Routers",
		"Latest in Security", "More security news"} {
		if strings.Contains(got, junk) {
			t.Fatalf("recirculation widget %q leaked into extracted content: %.300q", junk, got)
		}
	}
	if !strings.Contains(got, "update immediately") {
		t.Fatalf("prose after the widget was lost: %.300q", got)
	}
	if !strings.Contains(got, "hijack administrator sessions") {
		t.Fatalf("expected the article prose, got: %.300q", got)
	}
}

func TestExtractMainContentDropsDuplicateTitleH1(t *testing.T) {
	// Real-world cases (2026-10): bleepingcomputer.com embeds the headline
	// in a plain <div class="article_section"> — outside any <header> — so
	// the header-chrome strip missed it and the stored body began with a
	// second copy of the title plus the byline block; theregister.com's
	// .k5a-article embeds <h1 class="headline">+subtitle the same way. A
	// leading h1 whose text equals the article title is dropped; the
	// subtitle paragraph (real editorial content) and genuine section h1s
	// elsewhere must survive. Whitespace/newlines inside the publisher h1
	// must not defeat the match.
	prose := strings.Repeat("Researchers documented eleven flaws in Tailscale that allow root privileges. ", 8)
	html := `<html><body>
		<article>
			<div class="article_section">
				<h1>Tailscale warns of SSH flaws allowing root privilege escalation</h1>
				<div class="cz-news-story-title-section">
					By <h6><a href="/author/x" rel="author">Sergiu Gatlan</a></h6>
					<ul><li>October 27, 2026</li></ul>
				</div>
				<div class="articleBody"><p>` + prose + `</p></div>
			</div>
		</article>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, htmlOut := extractMainContent(doc, "Tailscale warns of SSH flaws allowing root privilege escalation")
	if strings.Count(got, "Tailscale warns of SSH flaws") != 0 {
		t.Fatalf("duplicate title survived in text: %.200q", got)
	}
	if strings.Contains(got, "Sergiu Gatlan") {
		t.Fatalf("byline leaked into prose-only .articleBody: %.200q", got)
	}
	if !strings.Contains(got, "root privileges") {
		t.Fatalf("prose missing: %.200q", got)
	}
	if strings.Contains(htmlOut, "<h1") {
		t.Fatalf("duplicate title h1 survived in HTML: %.200q", htmlOut)
	}
}

func TestExtractMainContentDropsSecurityAffairsTitleBlock(t *testing.T) {
	// securityaffairs.com (2026-10, row 90330) keeps the duplicate headline
	// inside the prose container as an <h2> wrapped in a
	// .common-heading.article-title div — the leading h1 check never sees an
	// h2. Its .post-time byline block and trailing .cta-tags tag cloud live
	// inside .article-details-block too, so they must be stripped as chrome
	// or they are stored as article prose. The hero image is real content
	// and must survive.
	prose := strings.Repeat("CISA added the PHP flaws tracked as PHP2Shell to its exploited vulnerabilities catalog. ", 8)
	html := `<html><body>
		<div class="article-details-block wow fadeInUp">
			<div class="common-heading line-bottom article-title mb-3">
				<h2>U.S. CISA adds PHP flaws PHP2Shell to its Known Exploited Vulnerabilities catalog</h2>
			</div>
			<div class="post-time mb-3">
				<span><i><img src="user-icon.svg" alt=""/></i> <a href="/author/paganinip">Pierluigi Paganini</a></span>
				<span><i><img src="clock-icon.svg" alt=""/></i> October 27, 2026</span>
			</div>
			<img class="img-fluid mb-4" src="https://securityaffairs.com/wp/hero.jpg" alt="PHP2Shell"/>
			<p>` + prose + `</p>
			<div class="cta-tags">
				<a class="cat-tag" href="/tag/cisa">CISA</a>
				<a class="cat-tag" href="/tag/hacking">Hacking</a>
				<a class="cat-tag" href="/tag/security-news">Security News</a>
				<a class="cat-tag" href="/tag/php">PHP</a>
				<a class="cat-tag" href="/tag/php2shell">php2shell</a>
			</div>
		</div>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, htmlOut := extractMainContent(doc, "U.S. CISA adds PHP flaws PHP2Shell to its Known Exploited Vulnerabilities catalog")
	if strings.Count(got, "U.S. CISA adds PHP flaws PHP2Shell") != 0 {
		t.Fatalf("duplicate title survived in text: %.200q", got)
	}
	if strings.Contains(got, "Pierluigi Paganini") || strings.Contains(got, "October 27, 2026") {
		t.Fatalf("byline leaked into prose: %.200q", got)
	}
	if strings.Contains(got, "cat-tag") || strings.Contains(got, "Security News") {
		t.Fatalf("tag cloud leaked into prose: %.200q", got)
	}
	if !strings.Contains(got, "exploited vulnerabilities catalog") {
		t.Fatalf("prose missing: %.200q", got)
	}
	if !strings.Contains(htmlOut, "hero.jpg") {
		t.Fatalf("hero image was dropped: %.300q", htmlOut)
	}
}

func TestExtractMainContentDropsSubtitleHeadlineAndShareThis(t *testing.T) {
	// securityaffairs.com row 209 (2026-10): the duplicate headline h2
	// carries an editorial subtitle after the headline text ("..., per
	// Cybereason."), so an exact title match never fires; and a
	// sharethis-inline-share-buttons div sits between the hero image and the
	// prose. The prefix rule (headings only) must drop the h2, the sharethis
	// widget must be stripped, and a genuine lead paragraph that happens to
	// open with the title text must survive.
	prose := strings.Repeat("Cybereason researchers observed the gang calling affiliates to negotiate ransom terms. ", 8)
	html := `<html><body>
		<div class="article-details-block wow fadeInUp">
			<img class="img-fluid mb-4" src="https://securityaffairs.com/wp/hero.png" alt=""/>
			<div style="margin-top: 0px; margin-bottom: 0px;" class="sharethis-inline-share-buttons"></div>
			<h2 class="wp-block-heading">Qilin ransomware gang now offers a “Call Lawyer” feature to pressure victims, per Cybereason.</h2>
			<p>` + prose + `</p>
		</div>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	title := "Qilin ransomware gang now offers a “Call Lawyer” feature to pressure victims"
	got, htmlOut := extractMainContent(doc, title)
	if strings.Contains(got, "per Cybereason") || strings.Contains(htmlOut, "wp-block-heading") {
		t.Fatalf("subtitle-carrying duplicate h2 survived: %.200q / %.200q", got, htmlOut)
	}
	if strings.Contains(htmlOut, "sharethis") {
		t.Fatalf("sharethis widget survived in HTML: %.200q", htmlOut)
	}
	if !strings.Contains(got, "Cybereason researchers observed") {
		t.Fatalf("prose missing: %.200q", got)
	}

	// A lead paragraph that opens with the title text is real prose (the
	// prefix rule is heading-only) and must survive.
	leadDoc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<html><body><div class="article-details-block"><h2>` + title + `, per Cybereason.</h2>
		<p>` + title + ` — the FBI issued a joint advisory this morning, and the
		` + prose + `</p></div></body></html>`))
	got2, _ := extractMainContent(leadDoc, title)
	if !strings.Contains(got2, "joint advisory") {
		t.Fatalf("lead paragraph opening with title text was dropped: %.200q", got2)
	}
}

func TestExtractMainContentKeepsSubtitleAndGenuineH1(t *testing.T) {
	// theregister.com: the h1.headline matches the RSS title and must be
	// dropped, but its <p class="subtitle"> is real editorial content and a
	// later h1 that is NOT the title must survive (the h1→h2 demotion and
	// "keep the subtitle" behaviour of the reading page depend on it).
	prose := strings.Repeat("Microsoft releases out-of-band Windows fixes with certificate updates. ", 8)
	html := `<html><body>
		<article class="k5a-article">
			<h1 class="headline">
				Microsoft releases out-of-band Windows fixes with certificate updates
			</h1>
			<p class="subtitle">Did you hear the one about eMudhra and the power of 4? Time to get certified.</p>
			<p>` + prose + `</p>
			<h1>Reader comment thread guidelines</h1>
		</article>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, htmlOut := extractMainContent(doc, "Microsoft releases out-of-band Windows fixes with certificate updates")
	if strings.Count(htmlOut, "<h1") != 1 || !strings.Contains(htmlOut, "Reader comment thread guidelines") {
		t.Fatalf("expected exactly the genuine h1 to survive: %.300q", htmlOut)
	}
	if !strings.Contains(got, "Did you hear the one about eMudhra") {
		t.Fatalf("subtitle paragraph was dropped: %.200q", got)
	}
	if !strings.Contains(got, "Reader comment thread guidelines") {
		t.Fatalf("genuine non-title h1 was dropped: %.200q", got)
	}
}

func TestExtractMainContentTruncatesCSOOnlineNewsletterTail(t *testing.T) {
	// csoonline.com (IDG) rows ~307/308/303: the prose container ends with a
	// JS-rendered "SUBSCRIBE TO OUR NEWSLETTER" widget (heading in arbitrary
	// casing, pitch copy, email capture). Everything from the heading on is
	// widget chrome and must be cut; the prose before it must survive.
	prose := strings.Repeat("Another threat group has started abusing the same toolkit for initial access. ", 8)
	html := `<html><body>
		<div class="content">
			<p>` + prose + `</p>
			<div class="newsletter-widget">
				<h3>SUBSCRIBE TO OUR NEWSLETTER</h3>
				<p>From our editors straight to your inbox</p>
				<p>Get started by entering your email address below.</p>
				<div>Please enter a valid email address</div>
			</div>
		</div>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, _ := extractMainContent(doc, "Serpentine#Cloud attack opens RAT-based backdoor")
	if strings.Contains(strings.ToLower(got), "newsletter") || strings.Contains(got, "inbox") {
		t.Fatalf("newsletter widget survived in prose: %.300q", got)
	}
	if !strings.Contains(got, "Another threat group has started abusing") {
		t.Fatalf("prose before the widget was dropped: %.200q", got)
	}
}

func TestExtractMainContentDropsReadMoreLinkParagraphs(t *testing.T) {
	// infosecurity-magazine.com rows 1556/1870/2639: related-article links
	// are embedded mid-prose as standalone <p><em><a>Read more: <headline>
	// </a></em></p> blocks. They are recirculation lines, not prose; prose
	// that merely mentions "read more" mid-sentence must survive.
	prose := "The attackers sought to sell it to a state-linked actor seeking intelligence. More about read more handling in prose here. "
	html := `<html><body>
		<div class="content">
			<p>` + prose + `</p>
			<p><em><a href="https://www.infosecurity-magazine.com/news/related/" target="_blank">Read more: Chinese State Hackers Exploiting Newly Disclosed Ivanti Flaw</a></em></p>
			<p>ANSSI's assessment of the Ivanti flaw confirmed the findings.</p>
		</div>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, _ := extractMainContent(doc, "ANSSI assesses Ivanti flaw exploitation")
	if strings.Contains(got, "Chinese State Hackers Exploiting") {
		t.Fatalf("Read more: link paragraph survived: %.300q", got)
	}
	if !strings.Contains(got, "read more handling in prose") {
		t.Fatalf("prose mentioning the phrase was dropped: %.200q", got)
	}
	if !strings.Contains(got, "ANSSI's assessment") {
		t.Fatalf("paragraph after the link block was dropped: %.200q", got)
	}
}

func TestExtractMainContentPrefersNestedPreciseContainer(t *testing.T) {
	// infostealers.com (2026-10): <article> wraps the real prose in
	// .entry-content alongside a sidebar <aside> holding "Recent articles",
	// a sightings dashboard and a "Weekly Briefing" signup — together about
	// half the container's text. The aside guard keeps asides only when
	// removal would take more than half, and this sidebar sat exactly at
	// 50.4%, so the stored body was half sidebar chrome. When the winning
	// container wraps a precise candidate that holds most of its non-aside
	// prose, prefer that inner container.
	prose := strings.Repeat("The infostealer malware harvested browser credentials and session cookies from the victim machines. ", 12)
	sidebar := strings.Repeat("Recent articles Weekly Briefing Total Sightings 49,631 Read report ", 12)
	html := `<html><body>
		<main>
			<article>
				<div class="entry-content prose">
					<h1>Revolut Hackers Used Infostealers for Elaborate Social Engineering</h1>
					<p>` + prose + `</p>
				</div>
				<aside class="lg:col-span-3">
					<div class="glass-card">` + sidebar + `</div>
				</aside>
			</article>
		</main>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, _ := extractMainContent(doc, "Revolut Hackers Used Infostealers for Elaborate Social Engineering")
	for _, junk := range []string{"Recent articles", "Weekly Briefing", "Total Sightings", "Read report"} {
		if strings.Contains(got, junk) {
			t.Fatalf("sidebar chrome %q leaked into extracted content: %.200q", junk, got)
		}
	}
	if !strings.Contains(got, "harvested browser credentials") {
		t.Fatalf("expected the article prose, got: %.200q", got)
	}
}

func TestExtractMainContentStripsEmbeddedPageTitle(t *testing.T) {
	// infostealers.com embeds a complete HTML document (its own <head>,
	// <title> and <style>) inside the prose container. The style tag was
	// already dropped, but the embedded page <title> survived as a phantom
	// headline line above the real body.
	prose := strings.Repeat("The investigation linked the campaign to a financially motivated threat group. ", 8)
	embedded := `<!DOCTYPE html><html lang="en"><head><title>Revolut Hackers Exploited Infostealers for Social Engineering</title><style>body { background-color: #151d2c; }</style></head><body><h1>Revolut Hackers Used Infostealers for Elaborate Social Engineering</h1><p>` + prose + `</p></body></html>`
	html := `<html><body>
		<article><div class="entry-content prose">
			` + embedded + `
			<p>Follow-up reporting confirmed the initial findings.</p>
		</div></article>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, _ := extractMainContent(doc, "Revolut Hackers Used Infostealers for Elaborate Social Engineering")
	for _, junk := range []string{"Exploited Infostealers for Social Engineering", "background-color"} {
		if strings.Contains(got, junk) {
			t.Fatalf("embedded-document chrome %q leaked into extracted content: %.300q", junk, got)
		}
	}
	if !strings.Contains(got, "linked the campaign") {
		t.Fatalf("expected the article prose, got: %.300q", got)
	}
}

func TestExtractMainContentRapid7BlogTagsAndFooter(t *testing.T) {
	// rapid7.com's blog template puts the tag cloud and share footer INSIDE
	// <article> (data-testid="blog-post-article-tags" / "blog-post-footer");
	// the stored body ended with "Article tagsEmergent Threat Response...
	// Share this articleStephen FewerAuthor's articles".
	prose := strings.Repeat("Rapid7 disclosed a patched Microsoft SharePoint remote code execution vulnerability. ", 8)
	html := `<html><body>
		<main><article id="blog-content-section">
			<div class="blog-content"><p>` + prose + `</p></div>
			<section data-testid="blog-post-article-tags">
				<h2>Article tags</h2>
				<ul><li><a href="/blog/tag/emergent-threat-response/">Emergent Threat Response</a></li></ul>
			</section>
			<footer data-testid="blog-post-footer">
				<p>Share this article</p>
				<span>Stephen Fewer</span><a href="/blog/author/stephen-fewer/">Author's articles</a>
			</footer>
		</article></main>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, _ := extractMainContent(doc, "CVE-2026-63520: Microsoft SharePoint remote code execution fixed")
	for _, junk := range []string{"Article tags", "Emergent Threat Response", "Share this article", "Stephen Fewer", "Author's articles"} {
		if strings.Contains(got, junk) {
			t.Fatalf("rapid7 blog chrome %q leaked into extracted content: %.200q", junk, got)
		}
	}
	if !strings.Contains(got, "SharePoint remote code execution") {
		t.Fatalf("expected the article prose, got: %.200q", got)
	}
}

func TestExtractMainContentElasticLabsChrome(t *testing.T) {
	// elastic.co security-labs report pages have no article wrapper, so
	// extraction falls to <main>: a newsletter promo <section> (promo-banner
	// class family) and a "Report an issue" feedback link row after the
	// prose leaked into the stored body.
	prose := strings.Repeat("Elastic Security achieved a perfect detection score across the evaluated platforms. ", 8)
	html := `<html><body>
		<main>
			<section class="bg-surface py-10"><div>
				<p>` + prose + `</p>
				<p>Full results are available on the AV-Comparatives website.</p>
				<div class="flex items-start gap-4"><a href="https://discuss.elastic.co/c/labs/">Report an issue</a></div>
			</div></section>
			<section class="bg-surface py-20"><div>
				<div class="size-promo-banner-dog-ear"></div>
				<h3>Elastic Security Labs Newsletter</h3>
				<p>Subscribe for monthly research updates.</p>
			</div></section>
		</main>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, _ := extractMainContent(doc, "Elastic Security scores 100% in AV-Comparatives Business Security Test")
	for _, junk := range []string{"Report an issue", "Newsletter", "Subscribe for monthly", "promo"} {
		if strings.Contains(got, junk) {
			t.Fatalf("elastic labs chrome %q leaked into extracted content: %.200q", junk, got)
		}
	}
	if !strings.Contains(got, "perfect detection score") && !strings.Contains(got, "AV-Comparatives website") {
		t.Fatalf("expected the article prose, got: %.200q", got)
	}
}

func TestExtractMainContentStripsBleepingComputerAdAndRelatedArticles(t *testing.T) {
	// Real production case (article 79255, bleepingcomputer.com): the page
	// stores a Ziff Davis promo card (.article-callout with a linked image,
	// CTA heading, and "Get the report" link to a hubs.li tracker) plus a
	// "Related Articles:" recirculation grid (.cz-related-article-wrapp) at
	// the end of the body. Both are publisher chrome and must not reach the
	// stored article text or content_html.
	prose := strings.Repeat("Cisco patched the actively exploited SD-WAN zero-day. ", 30)
	html := `<html><body>
		<div class="articleBody">
			<p>` + prose + `</p>
			<div class="article-callout">
				<div class="article-media"><a href="https://hubs.li/Q04sB3fb0"><img src="https://www.bleepstatic.com/c/p/p-blue-report-26.jpg" alt="article image"/></a></div>
				<div class="article-body">
					<h2><a href="https://hubs.li/Q04sB3fb0">Once attackers have valid credentials, only 37% of their actions are blocked</a></h2>
					<p>Overall prevention scores can hide what happens after initial access.</p>
					<a class="article-link" href="https://hubs.li/Q04sB3fb0">Get the report</a>
				</div>
			</div>
			<div class="cz-related-article-wrapp">
				<h3>Related Articles:</h3>
				<p><a class="ras" href="https://www.bleepingcomputer.com/news/security/ivanti/">Ivanti warns of Connect Secure zero-day exploited in attacks</a></p>
			</div>
		</div>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	got, bodyHTML := extractMainContent(doc, "Cisco patches new SD-WAN zero-day exploited in attacks")
	for _, junk := range []string{"Get the report", "Related Articles:", "Ivanti warns", "37% of their actions"} {
		if strings.Contains(got, junk) {
			t.Fatalf("bleepingcomputer chrome %q leaked into extracted text: %.200q", junk, got)
		}
		if strings.Contains(bodyHTML, junk) {
			t.Fatalf("bleepingcomputer chrome %q leaked into content_html: %.200q", junk, bodyHTML)
		}
	}
	if !strings.Contains(got, "SD-WAN zero-day") {
		t.Fatalf("expected the article prose, got: %.200q", got)
	}
}

func TestExtractMainContentUnwrapsBloggerTemplateBody(t *testing.T) {
	// Found live: security.googleblog.com (Blogger dynamic layout) ships the
	// post as HTML inside <script type='text/template'> under the
	// articleBody container (rendered client-side) and again inside
	// <noscript>. Stripping script+noscript left the container empty and
	// repair stored 0 chars. The template body is the article; the noscript
	// copy must stay stripped so the prose is neither lost nor duplicated.
	prose := strings.Repeat("Real Blogger article prose that matters. ", 20)
	html := `<html><body>
		<div class='post-body'><div class='post-content' itemprop='articleBody'>
			<script type='text/template'><p>` + prose + `</p></script>
		</div></div>
		<noscript><p>` + prose + `</p></noscript>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	if n := strings.Count(got, "Real Blogger article prose"); n != 20 {
		t.Fatalf("expected the template body exactly once (20 sentences), found %d in: %q", n, got)
	}
	if strings.Contains(got, "<p>") || strings.Contains(got, "&lt;") {
		t.Fatalf("template markup leaked as literal text: %q", got)
	}
}

func TestExtractMainContentIgnoresUnrelatedTextTemplates(t *testing.T) {
	// Only the articleBody template is article prose; UI templates elsewhere
	// ({{placeholders}} for comment widgets and the like) must not be injected.
	html := `<html><body>
		<article>
			<p>` + strings.Repeat("Ordinary article prose lives right here. ", 20) + `</p>
			<div class="comments"><script type="text/template"><li>{{user}} said {{text}}</li></script></div>
		</article>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	if strings.Contains(got, "{{user}}") {
		t.Fatalf("unrelated text/template leaked into prose: %q", got)
	}
	if !strings.Contains(got, "Ordinary article prose") {
		t.Fatalf("expected the real prose to remain, got: %q", got)
	}
}

func TestExtractMainContentKeepsArticleWhenNewsletterIsAnInlineLink(t *testing.T) {
	// securitylabs.datadoghq.com: the closing paragraph of the article says
	// "...or open an issue. You can also <a href="#subscribe">subscribe to our
	// newsletter</a> or RSS feed". The CSO tail rule matched that inline link
	// (its whole text is the phrase) and deleted the container's direct child,
	// which on this template is the entire article column, so the row was stored
	// with 0 characters. An inline link inside a sentence is prose, not the widget.
	prose := strings.Repeat("Real Datadog Security Labs research prose about the vulnerability. ", 12)
	html := `<html><body><main>
		<article class="grid grid-cols-12">
			<aside class="article-toc hidden">On this page</aside>
			<div class="col-span-12 desktop:col-span-8">
				<div class="article-content">
					<p>` + prose + `</p>
					<p>We're always eager to hear from you. If you have any questions, send us a message, or open an issue. You can also <a href="#subscribe">subscribe to our newsletter</a> or RSS feed.</p>
				</div>
			</div>
			<div id="feedback-card-sticky">Was this article helpful?</div>
		</article>
	</main></body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	if !strings.Contains(got, "Real Datadog Security Labs research prose") {
		t.Fatalf("article body was erased by the newsletter rule (got %d chars): %.200q", len(got), got)
	}
	if !strings.Contains(got, "send us a message") {
		t.Fatalf("closing paragraph was dropped: %.200q", got)
	}
}

func TestExtractMainContentNewsletterBannerAboveProseDoesNotEraseArticle(t *testing.T) {
	// The tail rule deletes the heading's top-level block AND every later
	// sibling. A newsletter banner heading at the TOP of the container would
	// therefore erase the whole article that follows it. A tail widget can never
	// be most of the container, so a cut that discards more than half is a
	// misidentified match and must not be applied.
	prose := strings.Repeat("The actual article starts after the sign-up banner and runs for a while. ", 10)
	html := `<html><body>
		<div class="content">
			<div class="signup-banner"><h3>Subscribe to our newsletter</h3></div>
			<p>` + prose + `</p>
		</div>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, _ := extractMainContent(doc, "")
	if !strings.Contains(got, "The actual article starts after the sign-up banner") {
		t.Fatalf("article after a top-of-page newsletter banner was erased (got %d chars): %.200q", len(got), got)
	}
}
