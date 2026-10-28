package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// llamaTemperature keeps summaries close to deterministic; they are factual condensations.
const llamaTemperature = 0.3

type llamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type llamaChatRequest struct {
	Model              string             `json:"model,omitempty"` // ignored by llama-server (single-model); required by other OpenAI-compatible servers (e.g. Ollama)
	Messages           []llamaChatMessage `json:"messages"`
	MaxTokens          int                `json:"max_tokens"`
	Temperature        float64            `json:"temperature"`
	Stream             bool               `json:"stream"`
	ChatTemplateKwargs map[string]any     `json:"chat_template_kwargs"`
}

type llamaChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *llamaError `json:"error,omitempty"`
}

type llamaError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// llamaServerError formats a non-200 llama-server reply, preferring its JSON error message.
func llamaServerError(status int, body []byte) error {
	var parsed struct {
		Error *llamaError `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != nil && parsed.Error.Message != "" {
		return fmt.Errorf("llama.cpp returned status %d: %s", status, parsed.Error.Message)
	}
	return fmt.Errorf("llama.cpp returned status %d: %s", status, strings.TrimSpace(string(body)))
}

// llamaChat sends one user prompt to llama-server's /v1/chat/completions and returns the answer
// text. The server applies the model's own chat template, which the raw /completion route would
// not. Thinking is switched off per request: Granite 4.2 otherwise spends most of its tokens
// reasoning before a two-sentence summary.
func llamaChat(ctx context.Context, client *http.Client, baseURL, model, userAgent, prompt string, maxTokens int) (string, error) {
	payload, err := json.Marshal(llamaChatRequest{
		Model:              model,
		Messages:           []llamaChatMessage{{Role: "user", Content: prompt}},
		MaxTokens:          maxTokens,
		Temperature:        llamaTemperature,
		Stream:             false,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", llamaServerError(resp.StatusCode, body)
	}

	var parsed llamaChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llama.cpp response contained no choices")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("received empty summary from llama.cpp")
	}
	return content, nil
}
