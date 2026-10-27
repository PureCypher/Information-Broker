package main

import "testing"

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
