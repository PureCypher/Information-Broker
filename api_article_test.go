package main

import "testing"

func TestParseArticleID(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"123", 123, false},
		{"1", 1, false},
		{"", 0, true},
		{"abc", 0, true},
		{"0", 0, true},
		{"-5", 0, true},
	}
	for _, c := range cases {
		got, err := parseArticleID(c.in)
		if c.wantErr && err == nil {
			t.Errorf("parseArticleID(%q): expected error, got nil", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("parseArticleID(%q): unexpected error %v", c.in, err)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("parseArticleID(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
