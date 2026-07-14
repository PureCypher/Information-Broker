package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
)

// cosineSimilarity returns the cosine similarity between two vectors, in
// [-1, 1]. Returns 0 for mismatched lengths or either vector being zero,
// rather than erroring -- callers treat 0 as "not similar," which is the
// correct behavior for both cases in the clustering decision this backs.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// assignCluster finds the seed (keyed by its cluster's seed article id) most
// similar to newEmbedding. Returns ok=false if no seed's similarity reaches
// threshold (or there are no seeds at all) -- the caller should then seed a
// new cluster using the new article's own id.
func assignCluster(newEmbedding []float32, seeds map[int64][]float32, threshold float64) (seedID int64, ok bool) {
	bestSimilarity := threshold
	found := false
	for id, seedEmbedding := range seeds {
		sim := cosineSimilarity(newEmbedding, seedEmbedding)
		if sim >= bestSimilarity {
			bestSimilarity = sim
			seedID = id
			found = true
		}
	}
	return seedID, found
}

// fetchEmbedding calls Ollama's /api/embeddings for a single text input.
func fetchEmbedding(ctx context.Context, httpClient *http.Client, ollamaURL, model, text string) ([]float32, error) {
	reqBody, err := json.Marshal(map[string]string{"model": model, "prompt": text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaURL+"/api/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed request returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	return parsed.Embedding, nil
}
