//go:build llamacpp

package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	llama "github.com/tcpipuk/llama-go"
)

// LlamaCpp implements the Backend interface using in-process llama.cpp via CGO.
type LlamaCpp struct {
	modelsDir string // ~/.solon/models/blobs/
	model     *llama.Model
	llmCtx    *llama.Context
	modelName string
	modelPath string
	mu        sync.Mutex
}

// NewLlamaCpp creates a new llama.cpp backend.
// modelsDir should be the blobs directory containing GGUF files.
func NewLlamaCpp(modelsDir string) *LlamaCpp {
	return &LlamaCpp{
		modelsDir: modelsDir,
	}
}

func (l *LlamaCpp) Name() string {
	return "llama.cpp"
}

func (l *LlamaCpp) Available() bool {
	// Native backend is always available when compiled in.
	// Actual usability depends on having models downloaded.
	return true
}

func (l *LlamaCpp) LoadModel(ctx context.Context, model *Model) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.loadModelLocked(model.Path, model.Name)
}

func (l *LlamaCpp) UnloadModel(ctx context.Context, model *Model) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.unloadLocked()
	return nil
}

func (l *LlamaCpp) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.ensureModel(req.Model); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	// Text completion (raw prompt)
	if req.Prompt != "" {
		return l.generateText(ctx, id, created, req)
	}

	// Chat completion
	return l.chatComplete(ctx, id, created, req)
}

func (l *LlamaCpp) CompleteStream(ctx context.Context, req *CompletionRequest) (<-chan CompletionChunk, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.ensureModel(req.Model); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())

	messages := toLlamaMessages(req.Messages)
	opts := toLlamaChatOptions(req)

	deltaCh, errCh := l.llmCtx.ChatStream(ctx, messages, opts)

	ch := make(chan CompletionChunk, 16)
	go func() {
		defer close(ch)
		for {
			select {
			case delta, ok := <-deltaCh:
				if !ok {
					// Stream finished — send final chunk with finish reason
					ch <- CompletionChunk{
						ID:           id,
						Content:      "",
						FinishReason: "stop",
						Created:      time.Now().Unix(),
					}
					return
				}
				if delta.Content != "" {
					ch <- CompletionChunk{
						ID:      id,
						Content: delta.Content,
						Created: time.Now().Unix(),
					}
				}
			case err := <-errCh:
				if err != nil {
					ch <- CompletionChunk{
						ID:           id,
						Content:      "",
						FinishReason: "error",
						Created:      time.Now().Unix(),
					}
				}
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

func (l *LlamaCpp) Embeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.ensureModel(req.Model); err != nil {
		return nil, err
	}

	// For embeddings we need a context with embeddings enabled.
	embCtx, err := l.model.NewContext(
		llama.WithContext(2048),
		llama.WithEmbeddings(),
		llama.WithThreads(runtime.NumCPU()),
	)
	if err != nil {
		return nil, fmt.Errorf("creating embedding context: %w", err)
	}
	defer func() { _ = embCtx.Close() }()

	if len(req.Input) == 1 {
		emb, err := embCtx.GetEmbeddings(req.Input[0])
		if err != nil {
			return nil, fmt.Errorf("generating embedding: %w", err)
		}
		return &EmbeddingResponse{
			Embeddings: [][]float64{float32sToFloat64s(emb)},
			Model:      req.Model,
			TokenCount: countTokens(embCtx, req.Input),
		}, nil
	}

	embs, err := embCtx.GetEmbeddingsBatch(req.Input)
	if err != nil {
		return nil, fmt.Errorf("generating embeddings batch: %w", err)
	}

	result := make([][]float64, len(embs))
	for i, emb := range embs {
		result[i] = float32sToFloat64s(emb)
	}

	return &EmbeddingResponse{
		Embeddings: result,
		Model:      req.Model,
		TokenCount: countTokens(embCtx, req.Input),
	}, nil
}

// countTokens returns the total token count across inputs, best-effort. Token
// counting must never fail an otherwise-successful embedding request, so an
// input that fails to tokenize simply contributes zero.
func countTokens(ctx *llama.Context, inputs []string) int {
	total := 0
	for _, s := range inputs {
		toks, err := ctx.Tokenize(s)
		if err != nil {
			continue
		}
		total += len(toks)
	}
	return total
}

// ensureModel ensures the requested model is loaded. Must be called with mu held.
func (l *LlamaCpp) ensureModel(name string) error {
	if l.modelName == name && l.model != nil {
		return nil
	}

	// Try to find the model GGUF file
	path, err := l.findModel(name)
	if err != nil {
		return err
	}

	return l.loadModelLocked(path, name)
}

// loadModelLocked loads a model from the given path. Must be called with mu held.
func (l *LlamaCpp) loadModelLocked(path, name string) error {
	l.unloadLocked()

	model, err := llama.LoadModel(path,
		llama.WithGPULayers(-1), // offload everything to GPU (Metal/CUDA)
		llama.WithMMap(true),
		llama.WithSilentLoading(),
	)
	if err != nil {
		return fmt.Errorf("loading model %s: %w", name, err)
	}

	llmCtx, err := model.NewContext(
		llama.WithContext(4096),
		llama.WithThreads(runtime.NumCPU()),
	)
	if err != nil {
		_ = model.Close()
		return fmt.Errorf("creating context for %s: %w", name, err)
	}

	l.model = model
	l.llmCtx = llmCtx
	l.modelName = name
	l.modelPath = path

	return nil
}

// unloadLocked frees the current model. Must be called with mu held.
func (l *LlamaCpp) unloadLocked() {
	if l.llmCtx != nil {
		_ = l.llmCtx.Close()
		l.llmCtx = nil
	}
	if l.model != nil {
		_ = l.model.Close()
		l.model = nil
	}
	l.modelName = ""
	l.modelPath = ""
}

// findModel locates the GGUF file for a model name.
func (l *LlamaCpp) findModel(name string) (string, error) {
	// Check if name is already an absolute path to a GGUF file
	if filepath.IsAbs(name) && strings.HasSuffix(strings.ToLower(name), ".gguf") {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		}
	}

	// Search in models directory for matching manifest
	safeName := strings.ReplaceAll(name, "/", "--")
	safeName = strings.ReplaceAll(safeName, ":", "-")

	// Check manifests dir (parent of blobs)
	manifestsDir := filepath.Join(filepath.Dir(l.modelsDir), "manifests")
	manifestPath := filepath.Join(manifestsDir, safeName+".json")
	if _, err := os.Stat(manifestPath); err == nil {
		data, err := os.ReadFile(manifestPath)
		if err == nil {
			var m struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(data, &m); err == nil && m.Path != "" {
				absPath := filepath.Join(filepath.Dir(l.modelsDir), m.Path)
				if _, err := os.Stat(absPath); err == nil {
					return absPath, nil
				}
			}
		}
	}

	// Scan blobs directory for any GGUF file (single model scenario)
	entries, err := os.ReadDir(l.modelsDir)
	if err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(strings.ToLower(entry.Name()), ".gguf") {
				return filepath.Join(l.modelsDir, entry.Name()), nil
			}
		}
	}

	return "", fmt.Errorf("model %q not found — run 'solon models pull %s' first", name, name)
}

// chatComplete performs a non-streaming chat completion.
func (l *LlamaCpp) chatComplete(ctx context.Context, id string, created int64, req *CompletionRequest) (*CompletionResponse, error) {
	messages := toLlamaMessages(req.Messages)
	opts := toLlamaChatOptions(req)

	resp, err := l.llmCtx.Chat(ctx, messages, opts)
	if err != nil {
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	return &CompletionResponse{
		ID:           id,
		Content:      resp.Content,
		FinishReason: "stop",
		Created:      created,
	}, nil
}

// generateText performs a raw text completion.
func (l *LlamaCpp) generateText(ctx context.Context, id string, created int64, req *CompletionRequest) (*CompletionResponse, error) {
	opts := toLlamaGenerateOptions(req)

	result, err := l.llmCtx.Generate(req.Prompt, opts...)
	if err != nil {
		return nil, fmt.Errorf("text generation: %w", err)
	}

	return &CompletionResponse{
		ID:           id,
		Content:      result,
		FinishReason: "stop",
		Created:      created,
	}, nil
}

func toLlamaMessages(messages []Message) []llama.ChatMessage {
	result := make([]llama.ChatMessage, len(messages))
	for i, m := range messages {
		result[i] = llama.ChatMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}
	return result
}

func toLlamaChatOptions(req *CompletionRequest) llama.ChatOptions {
	opts := llama.ChatOptions{}
	if req.MaxTokens > 0 {
		opts.MaxTokens = llama.Int(req.MaxTokens)
	}
	if req.Temperature > 0 {
		opts.Temperature = llama.Float32(float32(req.Temperature))
	}
	if req.TopP > 0 {
		opts.TopP = llama.Float32(float32(req.TopP))
	}
	return opts
}

func toLlamaGenerateOptions(req *CompletionRequest) []llama.GenerateOption {
	var opts []llama.GenerateOption
	if req.MaxTokens > 0 {
		opts = append(opts, llama.WithMaxTokens(req.MaxTokens))
	}
	if req.Temperature > 0 {
		opts = append(opts, llama.WithTemperature(float32(req.Temperature)))
	}
	if req.TopP > 0 {
		opts = append(opts, llama.WithTopP(float32(req.TopP)))
	}
	return opts
}

func float32sToFloat64s(f32s []float32) []float64 {
	f64s := make([]float64, len(f32s))
	for i, v := range f32s {
		f64s[i] = float64(v)
	}
	return f64s
}
