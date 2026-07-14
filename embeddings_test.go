package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0.0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1.0},
		{"mismatched lengths", []float32{1, 2, 3}, []float32{1, 2}, 0.0},
		{"zero vector", []float32{0, 0}, []float32{1, 1}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if diff := got - tt.want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("cosineSimilarity(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestAssignCluster(t *testing.T) {
	seeds := map[int64][]float32{
		100: {1, 0, 0},
		200: {0, 1, 0},
	}

	t.Run("joins the most similar seed above threshold", func(t *testing.T) {
		id, ok := assignCluster([]float32{0.99, 0.01, 0}, seeds, 0.85)
		if !ok || id != 100 {
			t.Fatalf("got (%d, %v), want (100, true)", id, ok)
		}
	})

	t.Run("no seed above threshold seeds a new cluster", func(t *testing.T) {
		_, ok := assignCluster([]float32{0, 0, 1}, seeds, 0.85)
		if ok {
			t.Fatalf("expected ok=false, got a match")
		}
	})

	t.Run("empty seed set never matches", func(t *testing.T) {
		_, ok := assignCluster([]float32{1, 0, 0}, map[int64][]float32{}, 0.85)
		if ok {
			t.Fatalf("expected ok=false with no seeds")
		}
	})
}

func TestFetchEmbedding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "nomic-embed-text" || body["prompt"] != "some title" {
			t.Errorf("unexpected request body: %+v", body)
		}
		w.Write([]byte(`{"embedding":[0.1,0.2,0.3]}`))
	}))
	defer srv.Close()

	emb, err := fetchEmbedding(context.Background(), srv.Client(), srv.URL, "nomic-embed-text", "some title")
	if err != nil {
		t.Fatalf("fetchEmbedding error: %v", err)
	}
	if len(emb) != 3 || emb[0] != 0.1 || emb[1] != 0.2 || emb[2] != 0.3 {
		t.Fatalf("unexpected embedding: %v", emb)
	}
}

func TestFetchEmbeddingNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchEmbedding(context.Background(), srv.Client(), srv.URL, "nomic-embed-text", "x")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}
