package main

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// Fixtures below are trimmed from real pages (class names kept, markup
// reduced) fetched while onboarding these feeds. Each asserts that publisher
// chrome at the start or end of the container is dropped and the prose stays.

func extractFixture(t *testing.T, html, title string) string {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, _ := extractMainContent(doc, title)
	return got
}

func assertNoChrome(t *testing.T, got string, junk ...string) {
	t.Helper()
	for _, j := range junk {
		if strings.Contains(got, j) {
			t.Errorf("chrome %q leaked into extracted text: %.260q", j, got)
		}
	}
}

func TestExtractMainContentAikidoDropsHeroBylineSidebarAndCTA(t *testing.T) {
	// aikido.dev (Webflow) has no <article>: the extractor fell back to <main>
	// and stored the breadcrumb + title hero, a "Written by / Published on /
	// Last updated on" byline, a sidebar ("Text Link", "Subscribe for news")
	// and a trailing "Tired of false positives? ... Start Now" CTA.
	prose := strings.Repeat("If you run PHP anywhere in your stack and did not upgrade, go upgrade now. ", 6)
	html := `<html><body><main class="main_wrap"><section class="blog-all-content is--blgo-post">
		<div class="blog-all-header u-container-large"><div class="blog_post_hero_container">
			<div class="breadcrumbs"><a class="breadcrumb_link" href="/blog">Blog</a><a class="breadcrumb_link is--2" href="/category/news">News</a><div class="breadcrumb_link is--current">Ghost in the PHP2Shell</div></div>
			<h1 class="blog_post_hero_h1">Ghost in the PHP2Shell</h1>
		</div></div>
		<div class="blog-all-columns u-container-large">
			<div class="blog-all-left"><div class="blog_post_container"><div class="blog_content_main">
				<div class="blog_post_hero_author"><div>Written by</div><a href="/author/dania">Dania Durnas</a></div>
				<div class="blog_card_meta_top"><div class="blog_featured_meta_item"><div>Published on:</div><div>Oct 29, 2026</div></div></div>
				<div class="blog_card_meta_top"><div class="blog_featured_meta_item"><div>Last updated on:</div><div>Oct 28, 2026</div></div></div>
				<p>` + prose + `</p>
				<h2>What the advisory says</h2>
				<p>` + prose + `</p>
				<div class="blog-meta-top"><div class="blog_sidebar_box is--blog-post"><div>Share:</div><a href="https://www.aikido.dev/blog/ghost-php2shell">https://www.aikido.dev/blog/ghost-php2shell</a></div></div>
			</div></div></div>
			<div class="blog_sidebar_box is--toc"><div class="blog_sidebar_link_wrapper"><a class="blog_sidebar_link" href="#x">Text Link</a></div><div class="blog_subscribe"><h3>Subscribe for news</h3></div></div>
		</div>
	</section>
	<div class="blog-ctas-wrapper"><div class="container-medium"><div class="blog_hero_newsletter"><div class="blog_author_meta">4.7/5Tired of false positives? Try Aikido like 100k others.</div><a class="u-button-primary" href="/start">Start Now</a></div></div></div>
	</main></body></html>`

	got := extractFixture(t, html, "Ghost in the PHP2Shell")
	if !strings.Contains(got, "If you run PHP anywhere in your stack") || !strings.Contains(got, "What the advisory says") {
		t.Fatalf("article prose was lost: %.260q", got)
	}
	assertNoChrome(t, got, "Written by", "Published on", "Last updated on", "Text Link", "Subscribe for news", "Tired of false positives", "Start Now", "Share:")
	if strings.HasPrefix(got, "Blog") {
		t.Errorf("text still opens with the breadcrumb: %.120q", got)
	}
}

func TestExtractMainContentBishopFoxDropsShareRail(t *testing.T) {
	// bishopfox.com opens its <article> with a sticky "Share" rail (label plus
	// social links) ahead of the real content column. The article's own "TL;DR"
	// blockquote is content and must stay.
	prose := strings.Repeat("PHP runs much of the web and these flaws affect its core engine. ", 8)
	html := `<html><body><article class="content-wrapper content-wrapper__narrow"><div class="relative flex flex-col md:flex-row">
		<div><div class="sticky md:top-[20px] flex flex-col"><p class="font-proxima-nova-semibold uppercase">Share</p>
			<div class="flex"><a href="https://www.facebook.com/share.php?u=x"><span><svg width="7"><path d="M1 1"/></svg></span></a></div></div></div>
		<div class="flex-grow"><div class="prose mx-auto">
			<blockquote><p>TL;DR ` + prose + `</p></blockquote>
			<h2>Background</h2><p>` + prose + `</p>
		</div></div>
	</div></article></body></html>`

	got := extractFixture(t, html, "")
	if strings.HasPrefix(got, "Share") {
		t.Errorf("text still opens with the Share rail: %.120q", got)
	}
	if !strings.Contains(got, "TL;DR") || !strings.Contains(got, "Background") {
		t.Fatalf("article content was lost: %.260q", got)
	}
}

func TestExtractMainContentGoogleSecurityBlogDropsHeroAndTags(t *testing.T) {
	// blog.google/security: the <article> opens with .uni-article-hero (the
	// "Chrome Security" eyebrow, the title and a date | author line) and the body
	// ends with a .uni-blog-article-tags "Posted in:" row.
	prose := strings.Repeat("Insecure HTTP traffic exposes Chrome users to risks of malware and phishing. ", 6)
	html := `<html><body><article class="uni-article-wrapper">
		<section class="uni-article-hero uni-article-hero--aquamarine"><div class="uni-page"><div class="uni-grid uni-article-hero__header"><p class="uni-article-hero__eyebrow font-eyebrow">Chrome Security</p></div>
			<div class="uni-grid uni-article-hero__content-grid"><h1>Chrome Now Warns Before Insecure HTTP Connections</h1><p>Oct 21, 2026 | Joe Doe</p></div></div></section>
		<section class="uni-container article-container"><div class="uni-content uni-blog-article-container article-container__content">
			<section class="uni-page uni-article-paragraph"><div class="rich-text"><p>` + prose + `</p></div></section>
			<section class="uni-page uni-article-paragraph"><div class="rich-text"><p>` + prose + `</p></div></section>
			<div class="uni-blog-article-tags article-tags uni-page"><div class="uni-grid__col--layout-8"><div class="uni-blog-article-tags__wrapper">Posted in:</div></div></div>
		</div></section>
	</article></body></html>`

	got := extractFixture(t, html, "Chrome Now Warns Before Insecure HTTP Connections")
	if !strings.Contains(got, "Insecure HTTP traffic exposes Chrome users") {
		t.Fatalf("article prose was lost: %.260q", got)
	}
	assertNoChrome(t, got, "Chrome Security", "Chrome Now Warns", "Oct 21, 2026", "Joe Doe", "Posted in:")
}

func TestExtractMainContentDatadogDropsAuthorsDateAndShareRow(t *testing.T) {
	// securitylabs.datadoghq.com opens the article column with author cards (name
	// and job title) and a "LAST UPDATED <date>" badge row, and ends it with a
	// .share-article "twitter reddit" link list. Prose that merely mentions
	// "last updated" has no <time> badge and must survive.
	prose := strings.Repeat("Datadog Security Labs observed exploitation attempts in the wild. ", 8)
	html := `<html><body><article class="grid grid-cols-12"><div class="col-span-12 desktop:col-span-8">
		<div class="authors grid grid-cols-2">
			<a class="author-card flex" href="/a/1"><div><p>Nick Frichette</p><p>Staff Security Researcher</p></div></a>
			<a class="author-card flex" href="/a/2"><div><p>Emile Spir</p><p>Senior Security Researcher</p></div></a>
		</div>
		<div class="flex gap-2 items-center"><span class="uppercase font-extrabold">LAST UPDATED</span><time class="badge" datetime="2026-10-23">October 23, 2026</time></div>
		<div class="article-content relative">
			<h2>Key points and observations</h2>
			<p>` + prose + `</p>
			<p>Last updated: the PHP project revised its advisory after this post was first written.</p>
		</div>
		<div class="share-article"><ul class="flex gap-3"><li class="inline-flex"><a href="https://twitter.com/intent/tweet">twitter</a></li><li class="inline-flex"><a href="https://www.reddit.com/submit">reddit</a></li></ul></div>
	</div></article></body></html>`

	got := extractFixture(t, html, "")
	if !strings.Contains(got, "Key points and observations") || !strings.Contains(got, "exploitation attempts in the wild") {
		t.Fatalf("article prose was lost: %.260q", got)
	}
	assertNoChrome(t, got, "Staff Security Researcher", "Senior Security Researcher", "LAST UPDATED", "October 23, 2026", "twitter", "reddit")
	if !strings.Contains(got, "revised its advisory") {
		t.Errorf("a prose sentence that mentions 'last updated' must survive: %.260q", got)
	}
}
