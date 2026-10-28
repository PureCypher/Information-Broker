package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBackfillFetchFlareSolverrFallback verifies that a WAF challenge status
// (403) on the direct fetch is retried through FlareSolverr, and that the
// solved page's HTML is extracted with the standard extractor — the failure
// mode that left cyberdefensemagazine articles permanently stuck on the
// feed-description fallback.
func TestBackfillFetchFlareSolverrFallback(t *testing.T) {
	page := `<!doctype html><html><head><title>T</title>
	<noscript><iframe src="https://www.googletagmanager.com/ns.html?id=GTM-XYZ" height="0"></iframe></noscript>
	<script>var tracking = {};</script>
	</head><body>
	<nav>Menu Home About</nav>
	<article><h1>T</h1><p>Real article body paragraph with substantive cybersecurity analysis text.</p><p>Second paragraph adds more depth to the body content.</p></article>
	<footer>Copyright footer chrome</footer>
	</body></html>`

	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Cmd string `json:"cmd"`
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("solver decode: %v", err)
		}
		if req.Cmd != "request.get" || req.URL == "" {
			t.Errorf("unexpected solver request: cmd=%q url=%q", req.Cmd, req.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"solution": map[string]any{
				"status":   http.StatusOK,
				"response": page,
			},
		})
	}))
	defer fs.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer origin.Close()

	originURL := origin.URL + "/article"
	client := &http.Client{Timeout: 5 * time.Second}
	content, contentHTML, err := backfillFetch(client, "test-agent", originURL, "T", 10000, 100000, fs.URL, 10*time.Second)
	if err != nil {
		t.Fatalf("backfillFetch: %v", err)
	}
	if strings.Contains(content, "googletagmanager") || strings.Contains(content, "tracking") {
		t.Errorf("content leaked chrome: %q", content[:min(120, len(content))])
	}
	if !strings.Contains(content, "Real article body paragraph") {
		t.Errorf("content missing body: %q", content)
	}
	if contentHTML != "" {
		t.Errorf("expected empty contentHTML for text-only extraction, got %q", contentHTML[:min(80, len(contentHTML))])
	}
}

// TestBackfillFetchNoSolverOnRealErrors verifies that non-challenge statuses
// (404) fail immediately without invoking the expensive headless-browser
// solver.
func TestBackfillFetchNoSolverOnRealErrors(t *testing.T) {
	solverCalled := false
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		solverCalled = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"solution": map[string]any{"status": http.StatusOK, "response": "<html><body>ok</body></html>"},
		})
	}))
	defer fs.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer origin.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, _, err := backfillFetch(client, "test-agent", origin.URL+"/gone", "T", 10000, 100000, fs.URL, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected HTTP 404 error, got %v", err)
	}
	if solverCalled {
		t.Error("solver must not run for non-challenge statuses")
	}
}

// TestBackfillFetchSolverFailureSurfaces verifies that a solver-side failure
// is reported as an error instead of silently keeping the contaminated row.
func TestBackfillFetchSolverFailureSurfaces(t *testing.T) {
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": "challenge not solved",
		})
	}))
	defer fs.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer origin.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, _, err := backfillFetch(client, "test-agent", origin.URL+"/article", "T", 10000, 100000, fs.URL, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "FlareSolverr") {
		t.Fatalf("expected FlareSolverr error, got %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
