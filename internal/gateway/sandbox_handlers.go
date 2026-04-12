package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/openclaw/solon/internal/sandbox"
)

func (g *Gateway) handleListSandboxes(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sandboxes": []any{}, "available": false})
		return
	}

	sandboxes, err := g.sandboxes.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if sandboxes == nil {
		sandboxes = []*sandbox.Sandbox{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"sandboxes": sandboxes, "available": true})
}

func (g *Gateway) handleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available (Docker not detected)")
		return
	}

	var req sandbox.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sb, err := g.sandboxes.Create(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, sb)
}

func (g *Gateway) handleGetSandbox(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	id := chi.URLParam(r, "id")
	sb, err := g.sandboxes.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, sb)
}

func (g *Gateway) handleStartSandbox(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	id := chi.URLParam(r, "id")
	if err := g.sandboxes.Start(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (g *Gateway) handleStopSandbox(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	id := chi.URLParam(r, "id")
	if err := g.sandboxes.Stop(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (g *Gateway) handleRemoveSandbox(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	id := chi.URLParam(r, "id")
	if err := g.sandboxes.Remove(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (g *Gateway) handleSandboxLogs(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	id := chi.URLParam(r, "id")

	logs, err := g.sandboxes.Logs(r.Context(), id, 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = logs.Close() }()

	// Stream logs as SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		if u, ok2 := w.(interface{ Unwrap() http.ResponseWriter }); ok2 {
			flusher, ok = u.Unwrap().(http.Flusher)
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if ok {
		flusher.Flush()
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := logs.Read(buf)
		if n > 0 {
			// Docker log stream has 8-byte header per frame; strip it for clean output
			data := stripDockerLogHeaders(buf[:n])
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			if ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				_, _ = fmt.Fprintf(w, "data: [error: %s]\n\n", readErr.Error())
			}
			break
		}
	}

	_, _ = fmt.Fprint(w, "data: [done]\n\n")
	if ok {
		flusher.Flush()
	}
}

func (g *Gateway) handleSandboxStats(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	id := chi.URLParam(r, "id")
	stats, err := g.sandboxes.Stats(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func (g *Gateway) handleOpenClawSend(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	// Parse the message from the request body
	var req struct {
		Message  string `json:"message"`
		Agent    string `json:"agent"`
		Thinking string `json:"thinking"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Run openclaw agent directly via docker exec (bypasses buggy agent-api bridge)
	agent := req.Agent
	if agent == "" {
		agent = "main"
	}
	args := []string{"agent", "--agent", agent, "--message", req.Message, "--json", "--timeout", "180"}
	if req.Thinking != "" && req.Thinking != "off" {
		args = append(args, "--thinking", req.Thinking)
	}
	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), args)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("agent error: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}


func (g *Gateway) handleOpenClawStart(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available (Docker not detected)")
		return
	}

	// Get provider key (prefer anthropic)
	providerKey := ""
	for _, name := range []string{"anthropic", "openai"} {
		key, err := g.store.GetProviderKey(name)
		if err == nil && key != "" {
			providerKey = key
			break
		}
	}
	if providerKey == "" {
		providers, _ := g.store.LoadProviders()
		if len(providers) > 0 {
			providerKey = providers[0].APIKey
		}
	}
	if providerKey == "" {
		writeError(w, http.StatusBadRequest, "no inference provider configured — add one at /providers first")
		return
	}

	status, err := g.sandboxes.EnsureOpenClaw(r.Context(), providerKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, status)
}

func (g *Gateway) handleOpenClawStatus(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}

	sandboxes, err := g.sandboxes.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	for _, sb := range sandboxes {
		if sb.Name == "openclaw" {
			writeJSON(w, http.StatusOK, map[string]any{
				"available": true,
				"sandbox":   sb,
				"running":   sb.Status == "running",
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"available": true, "running": false})
}

func (g *Gateway) handleListPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"presets": sandbox.ListPresets()})
}

func (g *Gateway) handleListTiers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tiers": sandbox.ListTiers()})
}

func (g *Gateway) handleOpenClawSessions(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}})
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"sessions", "--json"})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}})
		return
	}

	// Forward the raw JSON from OpenClaw
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}

func (g *Gateway) handleOpenClawChannels(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"channels": []any{}})
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"channels", "status", "--json"})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"channels": []any{}})
		return
	}

	// Parse the status response and transform into a flat channels array
	var status struct {
		Channels map[string]struct {
			Configured bool   `json:"configured"`
			Running    bool   `json:"running"`
			LastError  string `json:"lastError"`
		} `json:"channels"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		// Fallback: return raw output
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(output))
		return
	}

	channels := make([]map[string]any, 0)
	for name, ch := range status.Channels {
		if !ch.Configured {
			continue
		}
		s := "connected"
		if !ch.Running {
			s = "error"
		}
		entry := map[string]any{
			"name":   name,
			"type":   name,
			"status": s,
		}
		if ch.LastError != "" {
			entry["lastError"] = ch.LastError
		}
		channels = append(channels, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (g *Gateway) handleOpenClawAddChannel(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	var req struct {
		Channel  string `json:"channel"`
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Channel == "" {
		writeError(w, http.StatusBadRequest, "channel is required")
		return
	}

	args := []string{"channels", "add", "--channel", req.Channel}
	if req.BotToken != "" {
		args = append(args, "--token", req.BotToken)
	}
	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), args)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("channel add error: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"result": output})
}

func (g *Gateway) handleOpenClawRemoveChannel(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	name := chi.URLParam(r, "name")
	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"channels", "remove", "--channel", name})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("channel remove error: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"result": output})
}

func (g *Gateway) handleOpenClawAgents(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"agents": []any{}})
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"agents", "list", "--json"})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"agents": []any{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}

func (g *Gateway) handleOpenClawSkills(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}})
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"skills", "list", "--json"})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}

var workspaceAllowlist = map[string]bool{
	"SOUL.md":     true,
	"IDENTITY.md": true,
	"USER.md":     true,
	"AGENTS.md":   true,
}

func (g *Gateway) handleOpenClawReadFile(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	filename := chi.URLParam(r, "filename")
	if !workspaceAllowlist[filename] {
		writeError(w, http.StatusForbidden, "filename not allowed")
		return
	}

	filePath := "/root/.openclaw/workspace/" + filename
	content, err := g.sandboxes.ExecInContainer(r.Context(), []string{"cat", filePath})
	if err != nil {
		content = ""
	}

	writeJSON(w, http.StatusOK, map[string]string{"filename": filename, "content": content})
}

func (g *Gateway) handleOpenClawWriteFile(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	filename := chi.URLParam(r, "filename")
	if !workspaceAllowlist[filename] {
		writeError(w, http.StatusForbidden, "filename not allowed")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	filePath := "/root/.openclaw/workspace/" + filename
	if err := g.sandboxes.WriteToContainer(r.Context(), filePath, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("write error: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleOpenClawUIProxy reverse-proxies HTTP requests to the OpenClaw control UI
// running inside the Docker container. The prefix "/api/v1/openclaw/ui" is stripped
// before forwarding so relative asset paths like "./assets/foo.js" resolve correctly
// under the same proxy path (browser requests /api/v1/openclaw/ui/assets/foo.js which
// becomes /assets/foo.js upstream).
//
// Auth: browser iframes can't set custom headers, so UIAuthFromCookieOrQuery middleware
// converts ?token=<key> or cookie "solon-ui-token" into the Authorization header before
// the normal auth middleware validates.
func (g *Gateway) handleOpenClawUIProxy(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	containerIP, err := g.sandboxes.OpenClawContainerIP(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "OpenClaw is not running — start it first via the dashboard or 'solon openclaw'")
		return
	}

	upstream, err := url.Parse(fmt.Sprintf("http://%s:%d", containerIP, openclawGatewayPort))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("invalid upstream URL: %v", err))
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		// Strip the route prefix so upstream sees paths like /, /assets/foo.js
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api/v1/openclaw/ui")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.Host = upstream.Host
		// Remove Solon-specific auth — upstream runs with --auth none
		req.Header.Del("Authorization")
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
		writeError(rw, http.StatusBadGateway, fmt.Sprintf("OpenClaw UI proxy error: %v", err))
	}

	// Set a short-lived cookie on the initial HTML load so subsequent
	// asset/API requests from the iframe authenticate automatically.
	if token := r.URL.Query().Get("token"); token != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     "solon-ui-token",
			Value:    token,
			Path:     "/api/v1/openclaw/ui",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   3600, // 1 hour
		})
	}

	proxy.ServeHTTP(w, r)
}

// stripDockerLogHeaders removes the 8-byte Docker multiplex log headers.
// Docker log format: [stream_type(1) 0 0 0 size(4)] payload
func stripDockerLogHeaders(data []byte) []byte {
	var result []byte
	for len(data) >= 8 {
		// Read the 4-byte size (big-endian) at bytes 4-7
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		data = data[8:]
		if size > len(data) {
			size = len(data)
		}
		result = append(result, data[:size]...)
		data = data[size:]
	}
	// If we didn't parse any frames, return raw data (non-TTY mode)
	if len(result) == 0 {
		return data
	}
	return result
}
