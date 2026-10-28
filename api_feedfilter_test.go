package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lib/pq"
)

func distinctFeeds(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("https://f%d.example/rss", i)
	}
	return out
}

func TestNormalizeFeedFilters(t *testing.T) {
	alternating := make([]string, 26)
	for i := range alternating {
		alternating[i] = []string{"a", "b"}[i%2]
	}
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr error
	}{
		{"nil", nil, nil, nil},
		{"only blanks", []string{"", "  "}, nil, nil},
		{"trim and dedupe keep first-seen order", []string{" a ", "b", "a"}, []string{"a", "b"}, nil},
		{"25 distinct is allowed", distinctFeeds(25), distinctFeeds(25), nil},
		{"26 distinct is rejected", distinctFeeds(26), nil, errTooManyFeedFilters},
		{"cap applies after dedupe", alternating, []string{"a", "b"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeFeedFilters(c.in)
			if err != c.wantErr {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
	if errTooManyFeedFilters.Error() != "too many feed filters (max 25)" {
		t.Errorf("error message = %q", errTooManyFeedFilters.Error())
	}
}

func TestBuildArticlesWhereMultiFeed(t *testing.T) {
	cases := []struct {
		name      string
		feeds     []string
		q         string
		wantSub   []string
		wantArgs  int
		wantNext  int
		wantFirst interface{} // expected args[0] when args exist
	}{
		{"single feed keeps plain equality", []string{"a"}, "", []string{"feed_url = $1"}, 1, 2, "a"},
		{"two feeds use ANY with one array arg", []string{"a", "b"}, "", []string{"feed_url = ANY($1)"}, 1, 2, pq.Array([]string{"a", "b"})},
		{"two feeds then search renumber placeholders", []string{"a", "b"}, "cve", []string{"feed_url = ANY($1)", "title ILIKE $2"}, 4, 5, pq.Array([]string{"a", "b"})},
		{"no feeds no filter", nil, "", nil, 0, 1, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			where, args, next := buildArticlesWhere(c.feeds, c.q)
			for _, sub := range c.wantSub {
				if !strings.Contains(where, sub) {
					t.Errorf("WHERE %q missing %q", where, sub)
				}
			}
			if len(c.wantSub) == 0 && where != "" {
				t.Errorf("want empty WHERE, got %q", where)
			}
			if len(args) != c.wantArgs || next != c.wantNext {
				t.Fatalf("args=%d next=%d, want args=%d next=%d", len(args), next, c.wantArgs, c.wantNext)
			}
			if c.wantArgs > 0 && !reflect.DeepEqual(args[0], c.wantFirst) {
				t.Errorf("args[0] = %#v, want %#v", args[0], c.wantFirst)
			}
		})
	}
}

func TestGetArticlesRejectsTooManyFeeds(t *testing.T) {
	// A nil db proves the cap is enforced before any database access.
	s := &APIServer{}
	q := make([]string, 0, 26)
	for _, f := range distinctFeeds(26) {
		q = append(q, "feed="+f)
	}
	req := httptest.NewRequest(http.MethodGet, "/articles?"+strings.Join(q, "&"), nil)
	rec := httptest.NewRecorder()

	s.getArticles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "max 25") {
		t.Errorf("body = %q, want it to mention the cap", rec.Body.String())
	}
}
