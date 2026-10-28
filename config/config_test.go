package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestIsFeedExcluded(t *testing.T) {
	tests := []struct {
		name     string
		excluded []string
		feedURL  string
		want     bool
	}{
		{
			name:     "empty exclusion list never excludes",
			excluded: []string{},
			feedURL:  "https://cvefeed.io/rssfeed/latest.xml",
			want:     false,
		},
		{
			name:     "substring matches full feed URL",
			excluded: []string{"cvefeed.io"},
			feedURL:  "https://cvefeed.io/rssfeed/latest.xml",
			want:     true,
		},
		{
			name:     "case-insensitive match",
			excluded: []string{"CVEFeed.IO"},
			feedURL:  "https://cvefeed.io/rssfeed/latest.xml",
			want:     true,
		},
		{
			name:     "non-matching feed is not excluded",
			excluded: []string{"cvefeed.io"},
			feedURL:  "https://www.bleepingcomputer.com/feed/",
			want:     false,
		},
		{
			name:     "matches one of several entries",
			excluded: []string{"example.com", "cvefeed.io", "foo.bar"},
			feedURL:  "https://cvefeed.io/rssfeed/latest.xml",
			want:     true,
		},
		{
			name:     "empty feed URL is never excluded",
			excluded: []string{"cvefeed.io"},
			feedURL:  "",
			want:     false,
		},
		{
			name:     "whitespace-only entry is ignored",
			excluded: []string{"   "},
			feedURL:  "https://cvefeed.io/rssfeed/latest.xml",
			want:     false,
		},
		{
			name:     "entry with surrounding whitespace still matches",
			excluded: []string{"  cvefeed.io  "},
			feedURL:  "https://cvefeed.io/rssfeed/latest.xml",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DiscordConfig{ExcludedFeeds: tt.excluded}
			if got := d.IsFeedExcluded(tt.feedURL); got != tt.want {
				t.Errorf("IsFeedExcluded(%q) with excluded=%v = %v, want %v",
					tt.feedURL, tt.excluded, got, tt.want)
			}
		})
	}
}

func TestLoadLLMDefaultsPointAtComposeServices(t *testing.T) {
	for _, k := range []string{"LLAMA_URL", "LLAMA_EMBED_URL", "LLAMA_MODEL", "LLAMA_TIMEOUT", "LLAMA_MAX_RETRIES", "LLAMA_MAX_TOKENS"} {
		t.Setenv(k, "")
	}

	cfg := Load()

	if cfg.LLM.URL != "http://llama-granite:8080" || cfg.LLM.EmbedURL != "http://llama-embed:8081" {
		t.Fatalf("unexpected default URLs: %+v", cfg.LLM)
	}
	if cfg.LLM.Model != "granite-4.2-3b" || cfg.LLM.MaxRetries != 3 || cfg.LLM.MaxTokens != 512 {
		t.Fatalf("unexpected defaults: %+v", cfg.LLM)
	}
}

func TestLoadLLMReadsLlamaEnv(t *testing.T) {
	t.Setenv("LLAMA_URL", "http://gpu:9000")
	t.Setenv("LLAMA_EMBED_URL", "http://cpu:9001")
	t.Setenv("LLAMA_MAX_TOKENS", "256")

	cfg := Load()

	if cfg.LLM.URL != "http://gpu:9000" || cfg.LLM.EmbedURL != "http://cpu:9001" || cfg.LLM.MaxTokens != 256 {
		t.Fatalf("env not honoured: %+v", cfg.LLM)
	}
}

func TestIsSilentOnboarding(t *testing.T) {
	until := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	aikido := "https://www.aikido.dev/blog/rss.xml"
	on := DiscordConfig{SilentFeeds: []string{aikido, " BLOG.GOOGLE/security "}, SilentUntil: until}
	tests := []struct {
		name    string
		cfg     DiscordConfig
		feedURL string
		now     time.Time
		want    bool
	}{
		{"matching feed inside the window", on, aikido, until.Add(-time.Minute), true},
		{"match is case-insensitive and trims entries", on, "https://blog.google/security/rss/", until.Add(-time.Hour), true},
		{"exactly at the deadline the window is over", on, aikido, until, false},
		{"after the deadline", on, aikido, until.Add(time.Second), false},
		{"other feeds are never silenced", on, "https://www.wired.com/feed/", until.Add(-time.Minute), false},
		{"zero deadline means off", DiscordConfig{SilentFeeds: on.SilentFeeds}, aikido, until.Add(-time.Minute), false},
		{"no feeds means off", DiscordConfig{SilentUntil: until}, aikido, until.Add(-time.Minute), false},
		{"empty feed url", on, "", until.Add(-time.Minute), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSilentOnboarding(tt.feedURL, tt.now); got != tt.want {
				t.Errorf("IsSilentOnboarding(%q, %v) = %v, want %v", tt.feedURL, tt.now, got, tt.want)
			}
		})
	}
}

func TestParseSilentUntil(t *testing.T) {
	want := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{"empty means off", "", time.Time{}, false},
		{"RFC3339 UTC", "2026-10-29T12:00:00Z", want, false},
		{"RFC3339 offset is the same instant", "2026-10-29T13:00:00+01:00", want, false},
		{"date-only is ambiguous and rejected", "2026-10-29", time.Time{}, true},
		{"garbage is rejected", "tomorrow-ish", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSilentUntil(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSilentUntil(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !got.Equal(tt.want) {
				t.Errorf("ParseSilentUntil(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoadReadsSilentOnboardingEnv(t *testing.T) {
	t.Setenv("DISCORD_SILENT_FEEDS", "a.example, b.example")
	t.Setenv("DISCORD_SILENT_UNTIL", "2026-10-29T12:00:00Z")
	cfg := Load()
	if len(cfg.Discord.SilentFeeds) != 2 || cfg.Discord.SilentFeeds[1] != "b.example" {
		t.Errorf("SilentFeeds = %v", cfg.Discord.SilentFeeds)
	}
	if !cfg.Discord.SilentUntil.Equal(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("SilentUntil = %v", cfg.Discord.SilentUntil)
	}
}

// A typo in the deadline must never quietly turn silence off: the backlog
// would then post to Discord. Load exits, so run it in a subprocess.
func TestLoadRejectsMalformedSilentUntil(t *testing.T) {
	if os.Getenv("CONFIG_TEST_LOAD") == "1" {
		Load()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLoadRejectsMalformedSilentUntil$")
	cmd.Env = append(os.Environ(), "CONFIG_TEST_LOAD=1", "DISCORD_SILENT_UNTIL=tomorrow-ish")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("Load must exit non-zero on a malformed DISCORD_SILENT_UNTIL; output: %s", out)
	}
	if !strings.Contains(string(out), "DISCORD_SILENT_UNTIL") {
		t.Errorf("the error should name the variable, got: %s", out)
	}
}
