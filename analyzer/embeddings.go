package analyzer

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

var newEmbeddingProviderFromConfig = NewEmbeddingProviderFromConfig

type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	Dimension() int
	Name() string
}

type LocalProvider struct {
	endpoint  string
	model     string
	timeout   time.Duration
	dimension int
	client    *http.Client
}

func (p *LocalProvider) Model() string {
	return strings.TrimSpace(p.model)
}

func (p *LocalProvider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if strings.TrimSpace(p.endpoint) != "" {
		return requestEmbeddings(ctx, p.client, p.endpoint, p.model, "", texts)
	}
	return nil, errors.New("embedding local endpoint is required")
}

func (p *LocalProvider) Dimension() int {
	if p.dimension > 0 {
		return p.dimension
	}
	return 16
}

func (p *LocalProvider) Name() string { return "local" }

func (p *LocalProvider) Fingerprint() string {
	return strings.Join([]string{
		p.Name(),
		strings.TrimSpace(p.endpoint),
		strings.TrimSpace(p.model),
		fmt.Sprint(p.Dimension()),
	}, "|")
}

type APIProvider struct {
	baseURL   string
	model     string
	apiKeyEnv string
	timeout   time.Duration
	dimension int
	client    *http.Client
}

func (p *APIProvider) Model() string {
	return p.model
}

func (p *APIProvider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	key := strings.TrimSpace(os.Getenv(p.apiKeyEnv))
	if key == "" {
		return nil, fmt.Errorf("embedding API key env %s is not set", p.apiKeyEnv)
	}
	if strings.TrimSpace(p.baseURL) == "" {
		return nil, errors.New("embedding api base_url is required")
	}
	return requestEmbeddings(ctx, p.client, strings.TrimRight(p.baseURL, "/")+"/embeddings", p.model, key, texts)
}

func (p *APIProvider) Dimension() int { return p.dimension }

func (p *APIProvider) Name() string { return "api" }

func (p *APIProvider) Fingerprint() string {
	return strings.Join([]string{
		p.Name(),
		strings.TrimSpace(p.baseURL),
		strings.TrimSpace(p.model),
		strings.TrimSpace(p.apiKeyEnv),
		fmt.Sprint(p.Dimension()),
	}, "|")
}

type embeddingRequest struct {
	Model string   `json:"model,omitempty"`
	Input []string `json:"input,omitempty"`
}

type embeddingResponse struct {
	Data      []embeddingData `json:"data,omitempty"`
	Embedding []float64       `json:"embedding,omitempty"`
}

type embeddingData struct {
	Embedding []float64 `json:"embedding"`
}

func hasExplicitEmbeddingsProviderConfig(config ConfigEmbeddings) bool {
	if strings.TrimSpace(config.Provider) == "none" {
		return false
	}
	if strings.TrimSpace(config.Provider) == "local" {
		return strings.TrimSpace(config.Local.Endpoint) != ""
	}
	if strings.TrimSpace(config.Provider) == "api" {
		return strings.TrimSpace(config.API.BaseURL) != "" || strings.TrimSpace(config.API.Model) != ""
	}
	return strings.TrimSpace(config.Local.Endpoint) != "" ||
		strings.TrimSpace(config.Local.Model) != "" ||
		strings.TrimSpace(config.API.BaseURL) != "" ||
		strings.TrimSpace(config.API.Model) != "" ||
		strings.TrimSpace(config.API.APIKeyEnv) != "" ||
		config.BatchSize > 0 || config.ChunkSize > 0 || config.Dimension > 0
}

func NewEmbeddingProviderFromConfig(config ConfigEmbeddings) (EmbeddingProvider, error) {
	if !hasExplicitEmbeddingsProviderConfig(config) {
		return nil, nil
	}
	config = normalizeWorkspaceConfig(WorkspaceConfig{Embeddings: config}).Embeddings
	switch config.Provider {
	case "none":
		return nil, nil
	case "api":
		return &APIProvider{
			baseURL:   config.API.BaseURL,
			model:     config.API.Model,
			apiKeyEnv: config.API.APIKeyEnv,
			timeout:   config.API.Timeout.Duration(),
			dimension: config.Dimension,
			client:    &http.Client{Timeout: config.API.Timeout.Duration()},
		}, nil
	case "local":
		return &LocalProvider{
			endpoint:  config.Local.Endpoint,
			model:     config.Local.Model,
			timeout:   config.Local.Timeout.Duration(),
			dimension: config.Dimension,
			client:    &http.Client{Timeout: config.Local.Timeout.Duration()},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported embeddings provider %q", config.Provider)
	}
}

func requestEmbeddings(ctx context.Context, client *http.Client, endpoint, model, bearerToken string, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(embeddingRequest{Model: model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding endpoint returned HTTP %d", resp.StatusCode)
	}
	var decoded embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Data) > 0 {
		out := make([][]float64, len(decoded.Data))
		for i, item := range decoded.Data {
			out[i] = item.Embedding
		}
		return out, nil
	}
	if len(decoded.Embedding) > 0 {
		return [][]float64{decoded.Embedding}, nil
	}
	return nil, errors.New("embedding endpoint returned no embeddings")
}

func embeddingVersionForProvider(provider EmbeddingProvider) int {
	if provider == nil {
		return 0
	}
	type fingerprintProvider interface {
		Fingerprint() string
	}
	label := provider.Name()
	if fp, ok := provider.(fingerprintProvider); ok {
		label = fp.Fingerprint()
	} else {
		type modeler interface {
			Model() string
		}
		if m, ok := provider.(modeler); ok && strings.TrimSpace(m.Model()) != "" {
			label += ":" + strings.TrimSpace(m.Model())
		}
		label += fmt.Sprintf(":dim=%d", provider.Dimension())
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(label))
	return int(h.Sum32())
}

func deterministicEmbedding(text string, dim int) []float64 {
	if dim <= 0 {
		dim = 16
	}
	vec := make([]float64, dim)
	for i, r := range text {
		vec[i%dim] += float64((int(r)%97)+1) / 97.0
	}
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		vec[0] = 1
		return vec
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

func encodeEmbedding(vec []float64) []byte {
	if len(vec) == 0 {
		return nil
	}
	buf := make([]byte, 8*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}

func decodeEmbedding(blob []byte) []float64 {
	if len(blob)%8 != 0 || len(blob) == 0 {
		return nil
	}
	vec := make([]float64, len(blob)/8)
	for i := range vec {
		vec[i] = math.Float64frombits(binary.LittleEndian.Uint64(blob[i*8:]))
	}
	return vec
}

func normalizeSearchQuery(query string) []float64 {
	return deterministicEmbedding(strings.TrimSpace(strings.ToLower(query)), 16)
}

func NormalizeSearchQuery(query string) []float64 {
	return normalizeSearchQuery(query)
}

func EmbedSearchQuery(ctx context.Context, provider EmbeddingProvider, query string) ([]float64, error) {
	query = strings.TrimSpace(query)
	if provider == nil {
		return nil, errors.New("embedding provider is not configured")
	}
	vecs, err := provider.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embedding provider returned %d query vectors", len(vecs))
	}
	return vecs[0], nil
}

func EmbeddingProviderLabel(provider EmbeddingProvider) string {
	if provider == nil {
		return "unconfigured"
	}
	type modeler interface {
		Model() string
	}
	if m, ok := provider.(modeler); ok && strings.TrimSpace(m.Model()) != "" {
		return provider.Name() + ":" + strings.TrimSpace(m.Model())
	}
	return provider.Name()
}
