package analyzer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNewEmbeddingProviderFromConfig_DisablesProvider(t *testing.T) {
	provider, err := NewEmbeddingProviderFromConfig(ConfigEmbeddings{Provider: "none"})
	if err != nil {
		t.Fatalf("NewEmbeddingProviderFromConfig failed: %v", err)
	}
	if provider != nil {
		t.Fatalf("expected nil provider for none, got %T", provider)
	}
}

func TestLocalProviderCallsConfiguredEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "bge-m3" || len(req.Input) != 2 || req.Input[0] != "alpha" {
			t.Fatalf("unexpected embedding request: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(embeddingResponse{Data: []embeddingData{
			{Embedding: []float64{1, 0, 0}},
			{Embedding: []float64{0, 1, 0}},
		}})
	}))
	defer server.Close()

	provider, err := NewEmbeddingProviderFromConfig(ConfigEmbeddings{
		Provider: "local",
		Local: ConfigEmbeddingEndpoint{
			Endpoint: server.URL + "/api/embeddings",
			Model:    "bge-m3",
			Timeout:  ConfigDuration(time.Second),
		},
		Dimension: 3,
	})
	if err != nil {
		t.Fatalf("NewEmbeddingProviderFromConfig failed: %v", err)
	}
	if provider.Name() != "local" || provider.Dimension() != 3 {
		t.Fatalf("unexpected provider metadata: %s %d", provider.Name(), provider.Dimension())
	}
	vecs, err := provider.Embed(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 || vecs[1][1] != 1 {
		t.Fatalf("unexpected embeddings: %#v", vecs)
	}
}

func TestAPIProviderUsesAPIKeyEnvAndBaseURL(t *testing.T) {
	t.Setenv("TEST_EMBEDDING_KEY", "secret-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Fatalf("unexpected auth header %q", got)
		}
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "text-embedding-3-small" || len(req.Input) != 1 || req.Input[0] != "query" {
			t.Fatalf("unexpected embedding request: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(embeddingResponse{Data: []embeddingData{{Embedding: []float64{0.5, 0.5}}}})
	}))
	defer server.Close()

	provider, err := NewEmbeddingProviderFromConfig(ConfigEmbeddings{
		Provider: "api",
		API: ConfigEmbeddingAPI{
			BaseURL:   server.URL,
			Model:     "text-embedding-3-small",
			APIKeyEnv: "TEST_EMBEDDING_KEY",
			Timeout:   ConfigDuration(time.Second),
		},
		Dimension: 2,
	})
	if err != nil {
		t.Fatalf("NewEmbeddingProviderFromConfig failed: %v", err)
	}
	vecs, err := provider.Embed(context.Background(), []string{"query"})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 2 || vecs[0][0] != 0.5 {
		t.Fatalf("unexpected embeddings: %#v", vecs)
	}
}

func TestAPIProviderRequiresConfiguredAPIKey(t *testing.T) {
	_ = os.Unsetenv("MISSING_EMBEDDING_KEY")
	provider, err := NewEmbeddingProviderFromConfig(ConfigEmbeddings{
		Provider: "api",
		API: ConfigEmbeddingAPI{
			BaseURL:   "https://example.com/v1",
			Model:     "text-embedding-3-small",
			APIKeyEnv: "MISSING_EMBEDDING_KEY",
		},
	})
	if err != nil {
		t.Fatalf("NewEmbeddingProviderFromConfig failed: %v", err)
	}
	if _, err := provider.Embed(context.Background(), []string{"query"}); err == nil {
		t.Fatal("expected missing API key error")
	}
}
