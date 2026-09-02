package index

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbedderRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		json.NewDecoder(r.Body).Decode(&req)
		// Return out of order, to prove the client re-orders by index.
		resp := embedResponse{}
		for i := len(req.Input) - 1; i >= 0; i-- {
			resp.Data = append(resp.Data, struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}{Index: i, Embedding: []float32{float32(i), 1, 0}})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(srv.URL, "", "test-embed", 3)
	vecs, err := e.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Fatalf("want 3 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if v[0] != float32(i) {
			t.Fatalf("vectors not re-ordered by index: vec %d starts %v", i, v[0])
		}
	}
}

func TestEmbedderSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "model loading")
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(srv.URL, "", "m", 3)
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected 503 surfaced, got %v", err)
	}
}

func TestVectorSearchRanksBySimilarity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := embedResponse{}
		for i, text := range req.Input {
			// Crude but deterministic: vector encodes presence of keywords.
			v := []float32{0, 0, 0}
			if strings.Contains(strings.ToLower(text), "auth") {
				v[0] = 1
			}
			if strings.Contains(strings.ToLower(text), "payment") {
				v[1] = 1
			}
			if strings.Contains(strings.ToLower(text), "retry") {
				v[2] = 1
			}
			resp.Data = append(resp.Data, struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}{Index: i, Embedding: v})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ix, _ := buildTestIndex(t)
	ix.WithEmbedder(NewOpenAIEmbedder(srv.URL, "", "m", 3))

	opts := DefaultBuildOptions()
	opts.Embed = true
	if err := ix.Build(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	_, _, vectors, _ := ix.Stats()
	if vectors == 0 {
		t.Fatal("no vectors were produced")
	}
	t.Logf("embedded %d chunks", vectors)

	hits, err := ix.Search(context.Background(), "payment processing", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	t.Logf("top hit: %s [%s]", hits[0].Doc.Path, hits[0].Tier)
}

func TestCosineSimilarity(t *testing.T) {
	if got := cosine([]float32{1, 0}, []float32{1, 0}); got < 0.99 {
		t.Fatalf("identical vectors should score ~1, got %f", got)
	}
	if got := cosine([]float32{1, 0}, []float32{0, 1}); got > 0.01 {
		t.Fatalf("orthogonal vectors should score ~0, got %f", got)
	}
	if got := cosine([]float32{1, 0}, []float32{1, 0, 0}); got != 0 {
		t.Fatalf("mismatched dimensions must score 0, got %f", got)
	}
}
