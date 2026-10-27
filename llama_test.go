package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLlamaChatSendsNativeRequestAndReturnsContent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if ua := r.Header.Get("User-Agent"); ua != "test-agent" {
			t.Errorf("unexpected user agent: %q", ua)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  A short summary.  ","reasoning_content":"ignored"}}]}`))
	}))
	defer srv.Close()

	out, err := llamaChat(context.Background(), srv.Client(), srv.URL, "test-agent", "Summarize this", 256)
	if err != nil {
		t.Fatalf("llamaChat error: %v", err)
	}
	if out != "A short summary." {
		t.Fatalf("want trimmed content only, got %q", out)
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" || msgs[0].(map[string]any)["content"] != "Summarize this" {
		t.Fatalf("unexpected messages: %v", got["messages"])
	}
	if got["max_tokens"] != float64(256) || got["stream"] != false {
		t.Fatalf("unexpected sampling fields: %v", got)
	}
	kwargs, _ := got["chat_template_kwargs"].(map[string]any)
	if kwargs["enable_thinking"] != false {
		t.Fatalf("thinking must be disabled per request, got %v", got["chat_template_kwargs"])
	}
}

func TestLlamaChatSurfacesServerErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`))
	}))
	defer srv.Close()

	_, err := llamaChat(context.Background(), srv.Client(), srv.URL, "ua", "x", 64)
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "Loading model") {
		t.Fatalf("want status and server message in error, got %v", err)
	}
}

func TestLlamaChatRejectsEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  ","reasoning_content":"only thinking"}}]}`))
	}))
	defer srv.Close()

	if _, err := llamaChat(context.Background(), srv.Client(), srv.URL, "ua", "x", 64); err == nil {
		t.Fatal("expected error when the model produced no answer text")
	}
}

func TestLlamaChatRejectsResponseWithoutChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	if _, err := llamaChat(context.Background(), srv.Client(), srv.URL, "ua", "x", 64); err == nil {
		t.Fatal("expected error when choices is empty")
	}
}
