package config

import "testing"

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
