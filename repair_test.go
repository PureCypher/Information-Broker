package main

import (
	"flag"
	"testing"
)

func TestSelectRepairCandidatesOnlyContaminated(t *testing.T) {
	// Default scope: only rows whose stored content contains an
	// extraction-boilerplate marker get re-extracted. Clean rows must be
	// left alone — their summaries stay valid and no model work is queued.
	rows := []repairRow{
		{ID: 1, URL: "https://securityaffairs.com/a", FullContent: "Real body. You might also like Magento Campaign ..."},
		{ID: 2, URL: "https://securityaffairs.com/b", FullContent: "Clean real body about ransomware forensics."},
		{ID: 3, URL: "https://securityaffairs.com/c", FullContent: "Body. We use cookies on our website to give you the most relevant experience."},
		{ID: 4, URL: "https://securityaffairs.com/d", FullContent: "Body. Recent Articles Foo Bar Baz."},
	}
	got := selectRepairCandidates(rows, false, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 contaminated rows, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if r.ID == 2 {
			t.Fatalf("clean row 2 must not be selected: %+v", got)
		}
	}
	if got[0].ID != 1 || got[1].ID != 3 || got[2].ID != 4 {
		t.Fatalf("stable id order required, got %+v", got)
	}
}

func TestSelectRepairCandidatesTitlePrefix(t *testing.T) {
	// securityaffairs row 209's duplicate headline h2 appends an editorial
	// subtitle ("..., per Cybereason."), so the stored first line starts
	// with the title but is longer — an exact-match selector misses it.
	// Prefix matches (case-insensitive) must be selected; a first line that
	// merely mentions part of the title must not.
	rows := []repairRow{
		{ID: 1, URL: "https://securityaffairs.com/a", Title: "Qilin gang offers a Call Lawyer feature",
			FullContent: "Qilin gang offers a Call Lawyer feature, per Cybereason.\n\nReal body..."},
		{ID: 2, URL: "https://securityaffairs.com/b", Title: "Qilin gang offers a Call Lawyer feature",
			FullContent: "Researchers say the Qilin gang offers a Call Lawyer feature to affiliates."},
		{ID: 3, URL: "https://securityaffairs.com/c", Title: "Qilin gang offers a Call Lawyer feature",
			FullContent: "QILIN GANG OFFERS A CALL LAWYER FEATURE, PER CYBEREASON.\nBody..."},
	}
	got := selectRepairCandidates(rows, false, 0)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("expected exactly the prefix-duplicate rows 1 and 3, got %+v", got)
	}
}

func TestSelectRepairCandidatesAll(t *testing.T) {
	rows := []repairRow{
		{ID: 1, URL: "https://securityaffairs.com/a", FullContent: "Clean body."},
	}
	got := selectRepairCandidates(rows, true, 0)
	if len(got) != 1 {
		t.Fatalf("--all must select every matching row, got %d", len(got))
	}
}

func TestSelectRepairCandidatesLimit(t *testing.T) {
	rows := make([]repairRow, 0, 12)
	for i := 1; i <= 12; i++ {
		rows = append(rows, repairRow{ID: int64(i), URL: "https://x/a", FullContent: "Body. You might also like X."})
	}
	got := selectRepairCandidates(rows, false, 5)
	if len(got) != 5 || got[0].ID != 1 || got[4].ID != 5 {
		t.Fatalf("limit must cap batch size preserving id order, got %+v", got)
	}
}

func TestSelectRepairCandidatesCaseInsensitiveMarkers(t *testing.T) {
	// Real-world: securityaffairs stores the widget heading in title case
	// ("You Might Also Like"), so case-sensitive matching missed 1378 of
	// the 1380 contaminated rows. Matching must be case-insensitive.
	rows := []repairRow{
		{ID: 1, URL: "https://securityaffairs.com/a", FullContent: "Body. You Might Also Like X."},
		{ID: 2, URL: "https://securityaffairs.com/b", FullContent: "Body. RECENT ARTICLES Y."},
		{ID: 3, URL: "https://securityaffairs.com/c", FullContent: "Body. WE USE COOKIES on our website."},
		{ID: 4, URL: "https://cybersecurityventures.com/d", FullContent: "Body. CONTACT US TO SEND STORY TIPS and sponsorship."},
		{ID: 5, URL: "https://securityaffairs.com/e", FullContent: "Clean body."},
	}
	got := selectRepairCandidates(rows, false, 0)
	if len(got) != 4 {
		t.Fatalf("expected 4 case-insensitive matches, got %d: %+v", len(got), got)
	}
}

func TestSelectRepairCandidatesSignatureMarkers(t *testing.T) {
	// securityaffairs.com rows repaired before the signature-footer fix
	// still end with the standing author signature; the two new markers
	// must select them. The name marker also matches mid-prose mentions —
	// those rows cost one re-fetch and come back unchanged, so selecting
	// them is acceptable; a truly clean row stays untouched.
	rows := []repairRow{
		{ID: 1, URL: "https://securityaffairs.com/a", FullContent: "Real body.\n\nPierluigi Paganini\n\nfacebook\nlinkedin\ntwitter"},
		{ID: 2, URL: "https://securityaffairs.com/b", FullContent: "Real body.\n\nFollow me on Twitter: @securityaffairs"},
		{ID: 3, URL: "https://securityaffairs.com/c", FullContent: "Researchers interviewed Pierluigi Paganini about the campaign."},
		{ID: 4, URL: "https://securityaffairs.com/d", FullContent: "Clean body."},
	}
	got := selectRepairCandidates(rows, false, 0)
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("expected signature rows 1, 2 and the prose mention 3, got %+v", got)
	}
}

func TestParseRepairArgsFlagsAfterPositional(t *testing.T) {
	// Go's flag package stops at the first positional argument, so
	// `repair securityaffairs.com --dry-run` silently ignored the flag and
	// ran for real. Flags must be honoured wherever they appear.
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "")
	dryRun := fs.Bool("dry-run", false, "")
	all := fs.Bool("all", false, "")
	if err := parseRepairArgs(fs, []string{"securityaffairs.com", "--dry-run", "--limit", "5"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !*dryRun || *limit != 5 || *all {
		t.Fatalf("flags ignored: dryRun=%v limit=%d all=%v", *dryRun, *limit, *all)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "securityaffairs.com" {
		t.Fatalf("positional args wrong: %v", fs.Args())
	}
}

func TestClassifyRepairResultRejectsEmptyExtraction(t *testing.T) {
	// Found live: security.googleblog.com id=2399 was re-fetched, the
	// extractor produced nothing, and repair overwrote 10003 chars of
	// (contaminated but present) prose with an empty string. An empty
	// extraction means the extractor failed on this page, not that the
	// article is empty — never replace stored content with it.
	stored := `<span class="byline-author">Posted by Eric Lynch</span> real prose`
	for name, fetched := range map[string]string{
		"empty":           "",
		"whitespace only": " \n\t ",
	} {
		if got := classifyRepairResult(stored, fetched); got != repairRejectEmpty {
			t.Errorf("%s: classifyRepairResult = %v, want repairRejectEmpty", name, got)
		}
	}
}

func TestClassifyRepairResultUnchangedAndApply(t *testing.T) {
	if got := classifyRepairResult("same prose", "  same prose\n"); got != repairUnchanged {
		t.Errorf("identical after trim: got %v, want repairUnchanged", got)
	}
	if got := classifyRepairResult("<iframe src=x>", "real prose"); got != repairApply {
		t.Errorf("genuine change: got %v, want repairApply", got)
	}
	// A row that was already empty can accept a real extraction.
	if got := classifyRepairResult("", "real prose"); got != repairApply {
		t.Errorf("empty stored, real fetched: got %v, want repairApply", got)
	}
}
