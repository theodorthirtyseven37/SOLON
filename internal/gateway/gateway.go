package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/openclaw/solon/internal/dashboard"
	"github.com/openclaw/solon/internal/guardrails"
	"github.com/openclaw/solon/internal/inference"
	"github.com/openclaw/solon/internal/inference/backends"
	"github.com/openclaw/solon/internal/models"
	"github.com/openclaw/solon/internal/sandbox"
	"github.com/openclaw/solon/internal/storage"
	"github.com/openclaw/solon/internal/telegram"
	"github.com/openclaw/solon/internal/tunnel"
)

// Config holds gateway configuration.
type Config struct {
	Port       int
	Version    string
	Engine     *inference.Engine
	Store      *storage.DB
	Tunnel     tunnel.Tunnel
	Relay      RemoteAccess
	Guardrails *guardrails.Config
	Policies   *guardrails.PolicyStore
	Sandboxes  *sandbox.Manager
}

// RemoteAccess provides relay status info (optional).
type RemoteAccess interface {
	RemoteURL() string
	Connected() bool
	Status() map[string]any
}

// Gateway is the main HTTP server that handles auth, routing, and middleware.
type Gateway struct {
	router     chi.Router
	engine     *inference.Engine
	store      *storage.DB
	tunnel     tunnel.Tunnel
	relay      RemoteAccess
	sandboxes  *sandbox.Manager
	telegram   *telegram.Bridge
	port       int
	version    string
	guardrails *guardrails.Config
	policies   *guardrails.PolicyStore
	shield     *guardrails.Shield
}

// New creates a new Gateway with the given configuration.
func New(cfg Config) (*Gateway, error) {
	grCfg := cfg.Guardrails
	if grCfg == nil {
		grCfg = guardrails.DefaultConfig()
	}

	var shield *guardrails.Shield
	if grCfg.Enabled && grCfg.Shield.Enabled {
		shield = guardrails.NewShield(grCfg.Shield.Threshold)
	}

	g := &Gateway{
		router:     chi.NewRouter(),
		engine:     cfg.Engine,
		store:      cfg.Store,
		tunnel:     cfg.Tunnel,
		relay:      cfg.Relay,
		sandboxes:  cfg.Sandboxes,
		port:       cfg.Port,
		version:    cfg.Version,
		guardrails: grCfg,
		policies:   cfg.Policies,
		shield:     shield,
	}

	// Initialize Telegram bridge if sandboxes are available
	if cfg.Sandboxes != nil && cfg.Store != nil {
		g.telegram = telegram.New(cfg.Sandboxes.ContainerIP, cfg.Store)
	}

	g.setupRoutes()
	return g, nil
}

func (g *Gateway) setupRoutes() {
	r := g.router

	// Global middleware
	r.Use(RequestID)
	r.Use(Logger)
	r.Use(CORS)
	r.Use(Recovery)

	// Health check + system info (no auth required, localhost only for system)
	r.Get("/api/v1/health", g.handleHealth)
	r.Get("/api/v1/system", g.handleSystemInfo)

	// OpenAI-compatible inference API (localhost bypass for dashboard, auth for remote)
	r.Group(func(r chi.Router) {
		r.Use(g.LocalhostOrAuth)
		r.Use(g.RateLimit)

		r.Post("/v1/chat/completions", g.handleChatCompletions)
		r.Post("/v1/completions", g.handleCompletions)
		r.Post("/v1/embeddings", g.handleEmbeddings)
		r.Get("/v1/models", g.handleListModels)
	})

	// Anthropic-native pass-through proxy (accepts x-api-key auth)
	r.Group(func(r chi.Router) {
		r.Use(NormalizeAnthropicAuth)
		r.Use(g.LocalhostOrAuth)
		r.Use(g.RateLimit)
		r.Post("/v1/messages", g.handleAnthropicMessages)
	})

	// Management API (localhost-only, no API key needed for dashboard access)
	r.Group(func(r chi.Router) {
		r.Use(g.LocalhostOrAuth)
		r.Use(g.RequireAdminScope)

		// Key management
		r.Get("/api/v1/keys", g.handleListKeys)
		r.Post("/api/v1/keys", g.handleCreateKey)
		r.Delete("/api/v1/keys/{id}", g.handleRevokeKey)

		// Model management
		r.Get("/api/v1/models", g.handleListModelsDetailed)
		r.Get("/api/v1/models/loaded", g.handleLoadedModels)
		r.Post("/api/v1/models/pull", g.handlePullModel)
		r.Delete("/api/v1/models/{name}", g.handleDeleteModel)

		// Analytics
		r.Get("/api/v1/analytics/requests", g.handleRequestLog)
		r.Get("/api/v1/analytics/usage", g.handleUsageStats)
		r.Get("/api/v1/analytics/usage/keys", g.handleUsageByKey)
		r.Get("/api/v1/analytics/guardrails", g.handleGuardrailEvents)

		// Model catalog
		r.Get("/api/v1/models/catalog", g.handleModelCatalog)

		// Tunnel (legacy)
		r.Get("/api/v1/tunnel/status", g.handleTunnelStatus)
		r.Post("/api/v1/tunnel/enable", g.handleTunnelEnable)
		r.Post("/api/v1/tunnel/disable", g.handleTunnelDisable)

		// Remote access (relay)
		r.Get("/api/v1/remote/status", g.handleRemoteStatus)

		// Provider management
		r.Get("/api/v1/providers", g.handleListProviders)
		r.Post("/api/v1/providers", g.handleAddProvider)
		r.Delete("/api/v1/providers/{name}", g.handleDeleteProvider)

		// Sandbox management
		r.Get("/api/v1/sandboxes", g.handleListSandboxes)
		r.Post("/api/v1/sandboxes", g.handleCreateSandbox)
		r.Get("/api/v1/sandboxes/presets", g.handleListPresets)
		r.Get("/api/v1/sandboxes/tiers", g.handleListTiers)
		r.Get("/api/v1/sandboxes/{id}", g.handleGetSandbox)
		r.Post("/api/v1/sandboxes/{id}/start", g.handleStartSandbox)
		r.Post("/api/v1/sandboxes/{id}/stop", g.handleStopSandbox)
		r.Delete("/api/v1/sandboxes/{id}", g.handleRemoveSandbox)
		r.Get("/api/v1/sandboxes/{id}/logs", g.handleSandboxLogs)
		r.Get("/api/v1/sandboxes/{id}/stats", g.handleSandboxStats)

		// OpenClaw one-click launch
		r.Post("/api/v1/openclaw/start", g.handleOpenClawStart)
		r.Get("/api/v1/openclaw/status", g.handleOpenClawStatus)
		r.Post("/api/v1/openclaw/send", g.handleOpenClawSend)

		// Telegram bot integration
		r.Get("/api/v1/sandboxes/{id}/integrations/telegram", g.handleGetTelegramIntegration)
		r.Post("/api/v1/sandboxes/{id}/integrations/telegram", g.handleCreateTelegramIntegration)
		r.Delete("/api/v1/sandboxes/{id}/integrations/telegram", g.handleDeleteTelegramIntegration)
		r.Post("/api/v1/sandboxes/{id}/integrations/telegram/connect", g.handleConnectTelegram)
		r.Post("/api/v1/sandboxes/{id}/integrations/telegram/disconnect", g.handleDisconnectTelegram)
	})

	// OpenClaw WebSocket proxy (separate group for query param auth support)
	r.Group(func(r chi.Router) {
		r.Use(WSAuthFromQueryParam)
		r.Use(g.LocalhostOrAuth)
		r.Use(g.RequireAdminScope)
		r.Get("/api/v1/openclaw/ws", g.handleOpenClawWS)
	})

	// Dashboard — serve embedded static files (no auth, localhost only)
	r.Handle("/*", dashboard.Handler())
}

// SetRelay sets the remote access provider (can be called after construction).
func (g *Gateway) SetRelay(r RemoteAccess) {
	g.relay = r
}

// ListenAndServe starts the HTTP server.
func (g *Gateway) ListenAndServe() error {
	// Reconnect any previously-connected Telegram bots
	if g.telegram != nil && g.store != nil {
		go g.reconnectTelegramBots()
	}

	addr := fmt.Sprintf(":%d", g.port)
	return http.ListenAndServe(addr, g.router)
}

// Shutdown gracefully shuts down gateway resources.
func (g *Gateway) Shutdown() {
	if g.telegram != nil {
		g.telegram.Shutdown()
	}
}

// reconnectTelegramBots reconnects bots that were connected before a restart.
func (g *Gateway) reconnectTelegramBots() {
	integrations, err := g.store.ListTelegramIntegrations()
	if err != nil {
		log.Printf("[telegram] failed to list integrations for reconnect: %v", err)
		return
	}
	for _, ti := range integrations {
		if ti.Status == "connected" || ti.Status == "error" {
			if err := g.telegram.Connect(ti.SandboxID); err != nil {
				log.Printf("[telegram] reconnect failed for sandbox %s: %v", ti.SandboxID, err)
			}
		}
	}
}

// --- Inference handlers ---

func (g *Gateway) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Limit body size to prevent OOM
	r.Body = http.MaxBytesReader(w, r.Body, int64(g.guardrails.Gate.MaxBodyBytes))

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}

	var req inference.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		log.Printf("decode error: %v | body: %s", err, string(body[:min(len(body), 500)]))
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	// Step 1: Structural validation
	if err := validateChatRequest(&req); err != nil {
		g.logGuardrailEvent(r, req.Model, "gate", "block", err.Error(), 0)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check per-key model restrictions
	if err := CheckModelAccess(r, req.Model); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	// Step 2: Shield — prompt injection detection
	if g.shield != nil {
		msgs := chatMessagesToGuardrailMessages(req.Messages)
		result := g.shield.Scan(msgs)
		if result.Blocked && g.guardrails.Shield.Action == "block" {
			g.logGuardrailEvent(r, req.Model, "shield", "block", fmt.Sprintf("patterns: %v", result.Patterns), result.Score)
			writeError(w, http.StatusBadRequest, "request blocked by content policy")
			return
		}
		if len(result.Patterns) > 0 {
			g.logGuardrailEvent(r, req.Model, "shield", "flag", fmt.Sprintf("patterns: %v", result.Patterns), result.Score)
		}
	}

	// Step 3: Policy — system prompt pinning + content tagging
	if g.policies != nil {
		if policy := g.policies.ForModel(req.Model); policy != nil {
			msgs := chatMessagesToGuardrailMessages(req.Messages)
			transformed := policy.Apply(msgs)
			req.Messages = guardrailMessagesToChatMessages(transformed)
		}
	}

	start := time.Now()

	if req.Stream {
		g.handleChatCompletionsStream(w, r, &req, start)
		return
	}

	resp, err := g.engine.ChatCompletion(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Log request
	if keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo); ok {
		prov, _ := backends.ParseProviderModel(req.Model)
		_ = g.store.LogRequest(keyInfo.ID, r.Method, r.URL.Path, req.Model,
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens,
			int(time.Since(start).Milliseconds()), http.StatusOK, prov)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (g *Gateway) handleChatCompletionsStream(w http.ResponseWriter, r *http.Request, req *inference.ChatCompletionRequest, start time.Time) {
	// Unwrap to get the underlying http.Flusher (chi wraps the ResponseWriter)
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Try unwrapping
		if u, ok2 := w.(interface{ Unwrap() http.ResponseWriter }); ok2 {
			flusher, ok = u.Unwrap().(http.Flusher)
		}
	}
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	chunks, err := g.engine.ChatCompletionStream(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	totalCompletion := 0
	for chunk := range chunks {
		totalCompletion++
		data := inference.ChatCompletionStreamChunk{
			ID:      chunk.ID,
			Object:  "chat.completion.chunk",
			Created: chunk.Created,
			Model:   req.Model,
			Choices: []inference.ChatCompletionStreamChoice{
				{
					Index: 0,
					Delta: inference.ChatMessageDelta{
						Role:    "assistant",
						Content: chunk.Content,
					},
					FinishReason: chunk.FinishReason,
				},
			},
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return
		}

		_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonData)
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()

	// Log request
	if keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo); ok {
		prov, _ := backends.ParseProviderModel(req.Model)
		_ = g.store.LogRequest(keyInfo.ID, r.Method, r.URL.Path, req.Model,
			0, totalCompletion, int(time.Since(start).Milliseconds()), http.StatusOK, prov)
	}
}

func (g *Gateway) handleCompletions(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(g.guardrails.Gate.MaxBodyBytes))

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}

	var req inference.TextCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	// Structural validation
	if err := validateTextRequest(&req); err != nil {
		g.logGuardrailEvent(r, req.Model, "gate", "block", err.Error(), 0)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check per-key model restrictions
	if err := CheckModelAccess(r, req.Model); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	start := time.Now()

	resp, err := g.engine.TextCompletion(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo); ok {
		prov, _ := backends.ParseProviderModel(req.Model)
		_ = g.store.LogRequest(keyInfo.ID, r.Method, r.URL.Path, req.Model,
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens,
			int(time.Since(start).Milliseconds()), http.StatusOK, prov)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (g *Gateway) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(g.guardrails.Gate.MaxBodyBytes))

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}

	// OpenAI accepts input as string or []string — normalize to []string
	var raw struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	var input []string
	// Try string first
	var single string
	if err := json.Unmarshal(raw.Input, &single); err == nil {
		input = []string{single}
	} else {
		// Try []string
		if err := json.Unmarshal(raw.Input, &input); err != nil {
			writeError(w, http.StatusBadRequest, "input must be a string or array of strings")
			return
		}
	}

	// Check per-key model restrictions
	if err := CheckModelAccess(r, raw.Model); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	req := &inference.EmbeddingRequest{
		Model: raw.Model,
		Input: input,
	}

	start := time.Now()

	resp, err := g.engine.Embeddings(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo); ok {
		prov, _ := backends.ParseProviderModel(req.Model)
		_ = g.store.LogRequest(keyInfo.ID, r.Method, r.URL.Path, req.Model,
			resp.Usage.PromptTokens, 0,
			int(time.Since(start).Milliseconds()), http.StatusOK, prov)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (g *Gateway) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := g.engine.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Format as OpenAI-compatible response
	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	var data []modelObj
	for _, m := range models {
		data = append(data, modelObj{
			ID:      m.Name,
			Object:  "model",
			Created: m.Modified.Unix(),
			OwnedBy: "solon",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

// --- Anthropic pass-through proxy ---

// anthropicProxyClient is a shared HTTP client for proxying to Anthropic.
var anthropicProxyClient = &http.Client{Timeout: 10 * time.Minute}

func (g *Gateway) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(g.guardrails.Gate.MaxBodyBytes)))
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}

	// Get Anthropic provider config
	provider, err := g.store.GetProvider("anthropic")
	if err != nil {
		writeError(w, http.StatusBadGateway, "anthropic provider not configured")
		return
	}
	apiKey, err := g.store.GetProviderKey("anthropic")
	if err != nil {
		writeError(w, http.StatusBadGateway, "anthropic provider not configured or disabled")
		return
	}

	// Build upstream request
	upstreamURL := provider.BaseURL + "/v1/messages"
	upReq, err := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create upstream request")
		return
	}

	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("x-api-key", apiKey)
	if v := r.Header.Get("anthropic-version"); v != "" {
		upReq.Header.Set("anthropic-version", v)
	} else {
		upReq.Header.Set("anthropic-version", "2023-06-01")
	}
	if beta := r.Header.Get("anthropic-beta"); beta != "" {
		upReq.Header.Set("anthropic-beta", beta)
	}

	start := time.Now()

	resp, err := anthropicProxyClient.Do(upReq)
	if err != nil {
		log.Printf("anthropic proxy error: %v", err)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream error: %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Copy response headers
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream response body — flush for SSE support
	flusher, ok := w.(http.Flusher)
	if !ok {
		if u, ok2 := w.(interface{ Unwrap() http.ResponseWriter }); ok2 {
			flusher, ok = u.Unwrap().(http.Flusher)
		}
	}

	if ok {
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				flusher.Flush()
			}
			if readErr != nil {
				break
			}
		}
	} else {
		_, _ = io.Copy(w, resp.Body)
	}

	// Log request (best-effort, after response completes)
	if keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo); ok {
		var parsed struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &parsed)
		_ = g.store.LogRequest(keyInfo.ID, r.Method, r.URL.Path, parsed.Model,
			0, 0, int(time.Since(start).Milliseconds()), resp.StatusCode, "anthropic")
	}
}

// --- Management handlers ---

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	v := g.version
	if v == "" {
		v = "dev"
	}
	resp := map[string]any{
		"status":  "ok",
		"version": v,
	}
	if g.engine != nil {
		resp["backends"] = g.engine.BackendStatus()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (g *Gateway) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	totalBytes := totalMemoryBytes()
	totalMB := totalBytes / (1024 * 1024)

	writeJSON(w, http.StatusOK, map[string]any{
		"total_memory_mb": totalMB,
	})
}

func (g *Gateway) handleListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := g.store.ListKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (g *Gateway) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string   `json:"name"`
		Scope         string   `json:"scope"`
		RateLimit     int      `json:"rate_limit"`
		TTLSeconds    int      `json:"ttl_seconds"`
		AllowedModels []string `json:"allowed_models"`
		TunnelAccess  *bool    `json:"tunnel_access"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Scope == "" {
		req.Scope = "user"
	}
	if req.Scope != "admin" && req.Scope != "user" {
		writeError(w, http.StatusBadRequest, "scope must be 'admin' or 'user'")
		return
	}

	// Only admin keys (or localhost with no key) can create admin-scoped keys
	if req.Scope == "admin" {
		if keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo); ok && keyInfo.Scope != "admin" {
			writeError(w, http.StatusForbidden, "only admin keys can create admin-scoped keys")
			return
		}
	}

	opts := storage.CreateKeyOptions{
		Name:          req.Name,
		Scope:         req.Scope,
		RateLimit:     req.RateLimit,
		AllowedModels: req.AllowedModels,
		TunnelAccess:  req.TunnelAccess,
	}
	if req.TTLSeconds > 0 {
		opts.TTL = time.Duration(req.TTLSeconds) * time.Second
	}

	key, err := g.store.CreateKeyWithOptions(opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"key":  key.Raw,
		"name": key.Name,
		"id":   key.ID,
	})
}

func (g *Gateway) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := g.store.RevokeKey(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (g *Gateway) handleListModelsDetailed(w http.ResponseWriter, r *http.Request) {
	models, err := g.engine.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (g *Gateway) handlePullModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Stream bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "model name is required")
		return
	}

	if !req.Stream {
		if err := g.engine.PullModel(r.Context(), req.Name, nil); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "pulled"})
		return
	}

	// SSE streaming progress
	flusher, ok := w.(http.Flusher)
	if !ok {
		if u, ok2 := w.(interface{ Unwrap() http.ResponseWriter }); ok2 {
			flusher, ok = u.Unwrap().(http.Flusher)
		}
	}
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	err := g.engine.PullModel(r.Context(), req.Name, func(p models.DownloadProgress) {
		data, _ := json.Marshal(p)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	})

	if err != nil {
		data, _ := json.Marshal(map[string]string{"event": "error", "message": err.Error()})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}

	data, _ := json.Marshal(map[string]string{"event": "done", "status": "pulled"})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func (g *Gateway) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := g.engine.RemoveModel(r.Context(), name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (g *Gateway) handleRequestLog(w http.ResponseWriter, r *http.Request) {
	logs, err := g.store.GetRequestLog(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": logs})
}

func (g *Gateway) handleUsageStats(w http.ResponseWriter, r *http.Request) {
	stats, err := g.store.GetUsageStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (g *Gateway) handleRemoteStatus(w http.ResponseWriter, r *http.Request) {
	if g.relay == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, g.relay.Status())
}

func (g *Gateway) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	if g.tunnel == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "provider": ""})
		return
	}
	status, err := g.tunnel.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *Gateway) handleTunnelEnable(w http.ResponseWriter, r *http.Request) {
	if g.tunnel == nil {
		writeError(w, http.StatusBadRequest, "no tunnel provider configured")
		return
	}
	if err := g.tunnel.Enable(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, _ := g.tunnel.Status(r.Context())
	writeJSON(w, http.StatusOK, status)
}

func (g *Gateway) handleTunnelDisable(w http.ResponseWriter, r *http.Request) {
	if g.tunnel == nil {
		writeError(w, http.StatusBadRequest, "no tunnel provider configured")
		return
	}
	if err := g.tunnel.Disable(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

func (g *Gateway) handleUsageByKey(w http.ResponseWriter, r *http.Request) {
	usage, err := g.store.GetUsageByKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
}

func (g *Gateway) handleLoadedModels(w http.ResponseWriter, r *http.Request) {
	loaded := g.engine.LoadedModelsInfo()
	writeJSON(w, http.StatusOK, map[string]any{"loaded": loaded})
}

func (g *Gateway) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	catalog := models.GetCatalog()
	writeJSON(w, http.StatusOK, map[string]any{"models": catalog})
}

func (g *Gateway) handleGuardrailEvents(w http.ResponseWriter, r *http.Request) {
	events, err := g.store.GetGuardrailEvents(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stats, _ := g.store.GetGuardrailStats()
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"stats":  stats,
	})
}

// --- Provider management handlers ---

func (g *Gateway) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := g.store.ListProviders()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (g *Gateway) handleAddProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "name and api_key are required")
		return
	}

	// Auto-fill base_url from well-known defaults
	if req.BaseURL == "" {
		if url, ok := storage.WellKnownProviders[req.Name]; ok {
			req.BaseURL = url
		} else {
			writeError(w, http.StatusBadRequest, "base_url is required for unknown providers")
			return
		}
	}

	provider, err := g.store.CreateProvider(req.Name, req.BaseURL, req.APIKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Hot-reload into engine
	g.engine.AddProvider(backends.Provider{
		Name:    req.Name,
		BaseURL: req.BaseURL,
		APIKey:  req.APIKey,
	})

	writeJSON(w, http.StatusCreated, provider)
}

func (g *Gateway) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := g.store.DeleteProvider(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Hot-remove from engine
	g.engine.RemoveProvider(name)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    http.StatusText(status),
		},
	})
}

// --- Guardrails helpers ---

func chatMessagesToGuardrailMessages(msgs []inference.ChatMessage) []guardrails.Message {
	result := make([]guardrails.Message, len(msgs))
	for i, m := range msgs {
		result[i] = guardrails.Message{Role: m.Role, Content: m.Content.Text}
	}
	return result
}

func guardrailMessagesToChatMessages(msgs []guardrails.Message) []inference.ChatMessage {
	result := make([]inference.ChatMessage, len(msgs))
	for i, m := range msgs {
		result[i] = inference.ChatMessage{
			Role:    m.Role,
			Content: inference.ChatContent{Text: m.Content},
		}
	}
	return result
}

func (g *Gateway) logGuardrailEvent(r *http.Request, model, stage, action, reason string, score float64) {
	if g.store == nil || g.guardrails == nil || !g.guardrails.Audit.Enabled {
		return
	}

	requestID := r.Header.Get("X-Request-ID")
	keyID := ""
	if keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo); ok {
		keyID = keyInfo.ID
	}

	_ = g.store.LogGuardrailEvent(requestID, keyID, model, stage, action, reason, score)
}
