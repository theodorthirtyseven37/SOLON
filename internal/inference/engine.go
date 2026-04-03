package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/openclaw/solon/internal/inference/backends"
	"github.com/openclaw/solon/internal/models"
)

// LoadedModel tracks a loaded model with LRU metadata.
type LoadedModel struct {
	Model    *backends.Model
	Backend  backends.Backend
	LastUsed int64 // unix timestamp
	SizeMB   int64
}

// Engine orchestrates model loading, unloading, and inference requests.
type Engine struct {
	backends     []backends.Backend
	ollama       *backends.Ollama
	llamacpp     *backends.LlamaCpp
	proxy        *backends.ProxyBackend
	registry     *models.Registry
	loadedModels map[string]*LoadedModel // model name → loaded model
	memBudgetMB  int64                   // memory budget in MB (0 = auto)
	preload      []string                // models to preload
	mu           sync.RWMutex
}

// EngineOptions configures the engine.
type EngineOptions struct {
	MemoryBudgetMB int64              // 0 = auto (80% system RAM)
	Preload        []string           // models to preload at startup
	Providers      []backends.Provider // external API providers for proxy backend
}

// NewEngine creates a new inference engine and registers available backends.
// It prefers the native llama.cpp backend (in-process) over Ollama (external process).
func NewEngine() (*Engine, error) {
	return NewEngineWithOptions(EngineOptions{})
}

// NewEngineWithOptions creates a new inference engine with custom options.
func NewEngineWithOptions(opts EngineOptions) (*Engine, error) {
	e := &Engine{
		loadedModels: make(map[string]*LoadedModel),
		memBudgetMB:  opts.MemoryBudgetMB,
		preload:      opts.Preload,
	}

	// Initialize model registry
	dataDir, err := models.DataDir()
	if err != nil {
		slog.Warn("could not determine data dir for registry", "error", err)
	} else {
		reg, err := models.NewRegistry(dataDir)
		if err != nil {
			slog.Warn("could not initialize model registry", "error", err)
		} else {
			e.registry = reg
		}
	}

	// Try native llama.cpp backend first (preferred — in-process, no external deps)
	modelsDir := ""
	if dataDir, err := models.DataDir(); err == nil {
		modelsDir = filepath.Join(dataDir, "models", "blobs")
		_ = os.MkdirAll(modelsDir, 0755)
	}
	llamacpp := backends.NewLlamaCpp(modelsDir)
	if llamacpp.Available() {
		e.llamacpp = llamacpp
		e.backends = append(e.backends, llamacpp)
	}

	// Ollama as fallback
	ollama := backends.NewOllama("http://localhost:11434")
	if ollama.Available() {
		e.ollama = ollama
		e.backends = append(e.backends, ollama)
	}

	// Proxy backend for external API providers (Anthropic, OpenAI, etc.)
	if len(opts.Providers) > 0 {
		e.proxy = backends.NewProxyBackend()
		for _, p := range opts.Providers {
			e.proxy.AddProvider(p)
		}
		e.backends = append(e.backends, e.proxy)
	}

	if len(e.backends) == 0 {
		return nil, fmt.Errorf("no inference backends available — install models with 'solon models pull', start Ollama, or add a provider with 'solon providers add'")
	}

	// Preload models if specified
	for _, name := range e.preload {
		if _, err := e.ensureLoaded(name); err != nil {
			slog.Warn("could not preload model", "model", name, "error", err)
		} else {
			slog.Info("preloaded model", "model", name)
		}
	}

	return e, nil
}

// Close shuts down the engine and unloads all loaded models.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for name, lm := range e.loadedModels {
		_ = lm.Backend.UnloadModel(context.Background(), lm.Model)
		delete(e.loadedModels, name)
	}
	return nil
}

// LoadedModelsInfo returns info about currently loaded models.
func (e *Engine) LoadedModelsInfo() []map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []map[string]any
	for name, lm := range e.loadedModels {
		result = append(result, map[string]any{
			"name":      name,
			"backend":   lm.Backend.Name(),
			"last_used": time.Unix(lm.LastUsed, 0).Format(time.RFC3339),
			"size_mb":   lm.SizeMB,
		})
	}
	return result
}

// ensureLoaded ensures a model is loaded, evicting LRU models if needed.
func (e *Engine) ensureLoaded(model string) (backends.Backend, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Already loaded? Update LRU timestamp.
	if lm, ok := e.loadedModels[model]; ok {
		lm.LastUsed = time.Now().Unix()
		return lm.Backend, nil
	}

	// Select backend
	backend, err := e.selectBackend(model)
	if err != nil {
		return nil, err
	}

	// For Ollama and proxy backends, we don't manage loading ourselves
	if backend == e.ollama || backend == e.proxy {
		e.loadedModels[model] = &LoadedModel{
			Backend:  backend,
			LastUsed: time.Now().Unix(),
		}
		return backend, nil
	}

	// Evict LRU models if we're at capacity (max 3 native models by default)
	maxLoaded := 3
	nativeCount := 0
	for _, lm := range e.loadedModels {
		if lm.Backend != e.ollama && lm.Backend != e.proxy {
			nativeCount++
		}
	}
	for nativeCount >= maxLoaded {
		e.evictLRU()
		nativeCount--
	}

	// Resolve model path for native backend
	var modelPath string
	if e.registry != nil {
		modelPath, _ = e.registry.Resolve(model)
	}

	// Load the model
	modelObj := &backends.Model{Name: model, Path: modelPath}
	if err := backend.LoadModel(context.Background(), modelObj); err != nil {
		return nil, fmt.Errorf("loading model %s: %w", model, err)
	}

	e.loadedModels[model] = &LoadedModel{
		Model:    modelObj,
		Backend:  backend,
		LastUsed: time.Now().Unix(),
	}

	return backend, nil
}

// evictLRU unloads the least recently used non-Ollama model.
func (e *Engine) evictLRU() {
	var oldestName string
	var oldestTime int64 = 1<<63 - 1

	for name, lm := range e.loadedModels {
		if lm.Backend == e.ollama || lm.Backend == e.proxy {
			continue // don't evict externally-managed models
		}
		if lm.LastUsed < oldestTime {
			oldestTime = lm.LastUsed
			oldestName = name
		}
	}

	if oldestName != "" {
		lm := e.loadedModels[oldestName]
		if lm.Model != nil {
			_ = lm.Backend.UnloadModel(context.Background(), lm.Model)
		}
		delete(e.loadedModels, oldestName)
		slog.Info("evicted model", "model", oldestName, "reason", "LRU")
	}
}

// selectBackend picks the right backend for a model without locking.
func (e *Engine) selectBackend(model string) (backends.Backend, error) {
	// Check proxy backend for "provider/model" format
	if e.proxy != nil {
		provName, _ := backends.ParseProviderModel(model)
		if provName != "" && e.proxy.HasProvider(provName) {
			return e.proxy, nil
		}
	}

	if e.llamacpp != nil && e.registry != nil {
		if _, err := e.registry.Resolve(model); err == nil {
			return e.llamacpp, nil
		}
	}
	if e.ollama != nil {
		return e.ollama, nil
	}
	if len(e.backends) > 0 {
		return e.backends[0], nil
	}
	return nil, fmt.Errorf("no inference backends available")
}

// ChatCompletion performs a chat completion using the best available backend.
func (e *Engine) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	backend, err := e.backendForModel(req.Model)
	if err != nil {
		return nil, err
	}

	completionReq := &backends.CompletionRequest{
		Model:       req.Model,
		Messages:    toBackendMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}

	resp, err := backend.Complete(ctx, completionReq)
	if err != nil {
		return nil, fmt.Errorf("completion failed: %w", err)
	}

	return &ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: resp.Created,
		Model:   req.Model,
		Choices: []ChatCompletionChoice{
			{
				Index: 0,
				Message: ChatMessage{
					Role:    "assistant",
					Content: ChatContent{Text: resp.Content},
				},
				FinishReason: resp.FinishReason,
			},
		},
		Usage: Usage{
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
			TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
		},
	}, nil
}

// PullModel downloads a model using the registry (preferred) or Ollama (fallback).
func (e *Engine) PullModel(ctx context.Context, name string, progressFn func(models.DownloadProgress)) error {
	// Try registry first (native model management)
	if e.registry != nil {
		return e.registry.Pull(ctx, name, progressFn)
	}

	// Fall back to Ollama
	if e.ollama != nil {
		return e.ollama.PullModel(ctx, name)
	}

	return fmt.Errorf("no model management backend available")
}

// ListModels returns all installed models from registry and Ollama.
func (e *Engine) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var allModels []ModelInfo
	seen := make(map[string]bool)

	// List from registry (native models)
	if e.registry != nil {
		regModels, err := e.registry.List()
		if err != nil {
			slog.Warn("could not list registry models", "error", err)
		} else {
			for _, m := range regModels {
				allModels = append(allModels, ModelInfo{
					Name:     m.Name,
					Size:     m.Size,
					Modified: m.PulledAt,
				})
				seen[m.Name] = true
			}
		}
	}

	// List proxy models (external providers)
	if e.proxy != nil {
		for _, m := range e.proxy.ListAvailableModels() {
			if !seen[m.Name] {
				allModels = append(allModels, ModelInfo{
					Name:   m.Name,
					Format: "proxy",
				})
				seen[m.Name] = true
			}
		}
	}

	// Also list from Ollama if available
	if e.ollama != nil {
		ollamaModels, err := e.ollama.ListModels(ctx)
		if err == nil {
			for _, m := range ollamaModels {
				if !seen[m.Name] {
					allModels = append(allModels, ModelInfo{
						Name:         m.Name,
						Size:         m.Size,
						Format:       m.Details.Format,
						Family:       m.Details.Family,
						Params:       m.Details.ParameterSize,
						Quantization: m.Details.QuantizationLevel,
						Modified:     m.ModifiedAt,
					})
				}
			}
		}
	}

	return allModels, nil
}

// GetModelInfo returns info about a specific installed model by name.
func (e *Engine) GetModelInfo(ctx context.Context, name string) (*ModelInfo, error) {
	models, err := e.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	for _, m := range models {
		if m.Name == name {
			return &m, nil
		}
	}

	return nil, fmt.Errorf("model %q not found", name)
}

// RemoveModel deletes a model from registry or Ollama.
func (e *Engine) RemoveModel(ctx context.Context, name string) error {
	// Try registry first
	if e.registry != nil {
		if err := e.registry.Remove(name); err == nil {
			return nil
		}
	}

	// Fall back to Ollama
	if e.ollama != nil {
		return e.ollama.DeleteModel(ctx, name)
	}

	return fmt.Errorf("model %q not found", name)
}

// Embeddings generates embeddings for the given input.
func (e *Engine) Embeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	backend, err := e.backendForModel(req.Model)
	if err != nil {
		return nil, err
	}

	backendReq := &backends.EmbeddingRequest{
		Model: req.Model,
		Input: req.Input,
	}

	resp, err := backend.Embeddings(ctx, backendReq)
	if err != nil {
		return nil, fmt.Errorf("embeddings failed: %w", err)
	}

	data := make([]EmbeddingData, len(resp.Embeddings))
	for i, emb := range resp.Embeddings {
		data[i] = EmbeddingData{
			Object:    "embedding",
			Embedding: emb,
			Index:     i,
		}
	}

	return &EmbeddingResponse{
		Object: "list",
		Data:   data,
		Model:  req.Model,
		Usage: EmbeddingUsage{
			PromptTokens: resp.TokenCount,
			TotalTokens:  resp.TokenCount,
		},
	}, nil
}

// TextCompletion performs a text completion (non-chat).
func (e *Engine) TextCompletion(ctx context.Context, req *TextCompletionRequest) (*TextCompletionResponse, error) {
	backend, err := e.backendForModel(req.Model)
	if err != nil {
		return nil, err
	}

	completionReq := &backends.CompletionRequest{
		Model:       req.Model,
		Prompt:      req.Prompt,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
	}

	resp, err := backend.Complete(ctx, completionReq)
	if err != nil {
		return nil, fmt.Errorf("completion failed: %w", err)
	}

	return &TextCompletionResponse{
		ID:      resp.ID,
		Object:  "text_completion",
		Created: resp.Created,
		Model:   req.Model,
		Choices: []TextCompletionChoice{
			{
				Text:         resp.Content,
				Index:        0,
				FinishReason: resp.FinishReason,
			},
		},
		Usage: Usage{
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
			TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
		},
	}, nil
}

// ChatCompletionStream performs a streaming chat completion.
func (e *Engine) ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan backends.CompletionChunk, error) {
	backend, err := e.backendForModel(req.Model)
	if err != nil {
		return nil, err
	}

	completionReq := &backends.CompletionRequest{
		Model:       req.Model,
		Messages:    toBackendMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
	}

	return backend.CompleteStream(ctx, completionReq)
}

// AddProvider adds an external API provider to the proxy backend at runtime.
func (e *Engine) AddProvider(p backends.Provider) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.proxy == nil {
		e.proxy = backends.NewProxyBackend()
		e.backends = append(e.backends, e.proxy)
	}
	e.proxy.AddProvider(p)
}

// RemoveProvider removes an external API provider from the proxy backend at runtime.
func (e *Engine) RemoveProvider(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.proxy != nil {
		e.proxy.RemoveProvider(name)
	}
}

// backendForModel selects the best backend for a given model.
// Auto-loads the model on first request; evicts LRU when at capacity.
func (e *Engine) backendForModel(model string) (backends.Backend, error) {
	if len(e.backends) == 0 {
		return nil, fmt.Errorf("no inference backends available")
	}

	return e.ensureLoaded(model)
}

func toBackendMessages(messages []ChatMessage) []backends.Message {
	result := make([]backends.Message, len(messages))
	for i, m := range messages {
		result[i] = backends.Message{
			Role:    m.Role,
			Content: m.Content.Text,
		}
	}
	return result
}

// --- OpenAI-compatible types ---

// ChatCompletionRequest represents an OpenAI-compatible chat completion request.
type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	TopP        float64       `json:"top_p,omitempty"`
	N           int           `json:"n,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

// ChatMessage represents a message in a chat conversation.
// Content can be a string or an array of content parts (OpenAI multi-modal format).
type ChatMessage struct {
	Role    string         `json:"role"`
	Content ChatContent    `json:"content"`
}

// ChatContent handles both string and array content formats from the OpenAI API.
type ChatContent struct {
	Text string
}

func (c ChatContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.Text)
}

func (c *ChatContent) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.Text = s
		return nil
	}

	// Try array of content parts: [{"type":"text","text":"..."}]
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &parts); err == nil {
		for _, p := range parts {
			if p.Type == "text" {
				c.Text += p.Text
			}
		}
		return nil
	}

	return fmt.Errorf("content must be a string or array of content parts")
}

// ChatCompletionResponse represents an OpenAI-compatible chat completion response.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   Usage                  `json:"usage"`
}

// ChatCompletionChoice represents a single completion choice.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage represents token usage information.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionStreamChunk represents a single SSE chunk in a streaming response.
type ChatCompletionStreamChunk struct {
	ID      string                       `json:"id"`
	Object  string                       `json:"object"`
	Created int64                        `json:"created"`
	Model   string                       `json:"model"`
	Choices []ChatCompletionStreamChoice `json:"choices"`
}

// ChatCompletionStreamChoice represents a choice in a streaming chunk.
type ChatCompletionStreamChoice struct {
	Index        int              `json:"index"`
	Delta        ChatMessageDelta `json:"delta"`
	FinishReason string           `json:"finish_reason,omitempty"`
}

// ChatMessageDelta represents the delta content in a streaming chunk.
type ChatMessageDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ModelInfo holds information about an installed model.
type ModelInfo struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Format       string `json:"format"`
	Family       string `json:"family"`
	Params       string `json:"params"`
	Quantization string `json:"quantization"`
	Modified     Time   `json:"modified"`
}

// SizeHuman returns a human-readable file size.
func (m ModelInfo) SizeHuman() string {
	const (
		MB = 1024 * 1024
		GB = 1024 * MB
	)
	switch {
	case m.Size >= GB:
		return fmt.Sprintf("%.1f GB", float64(m.Size)/float64(GB))
	case m.Size >= MB:
		return fmt.Sprintf("%.1f MB", float64(m.Size)/float64(MB))
	default:
		return fmt.Sprintf("%d B", m.Size)
	}
}

// ModifiedHuman returns a human-readable modification time.
func (m ModelInfo) ModifiedHuman() string {
	return m.Modified.Format("2006-01-02")
}

// --- Embedding types (OpenAI-compatible) ---

// EmbeddingRequest represents an OpenAI-compatible embedding request.
type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbeddingResponse represents an OpenAI-compatible embedding response.
type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  EmbeddingUsage  `json:"usage"`
}

// EmbeddingData holds a single embedding vector.
type EmbeddingData struct {
	Object    string    `json:"object"`
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

// EmbeddingUsage holds token usage for embedding requests.
type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// --- Text completion types (OpenAI-compatible) ---

// TextCompletionRequest represents an OpenAI-compatible text completion request.
type TextCompletionRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	N           int     `json:"n,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Stream      bool    `json:"stream,omitempty"`
}

// TextCompletionResponse represents an OpenAI-compatible text completion response.
type TextCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []TextCompletionChoice `json:"choices"`
	Usage   Usage                  `json:"usage"`
}

// TextCompletionChoice represents a choice in a text completion response.
type TextCompletionChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason"`
}
