package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (g *Gateway) handleGetTelegramIntegration(w http.ResponseWriter, r *http.Request) {
	sandboxID := chi.URLParam(r, "id")

	ti, err := g.store.GetTelegramIntegration(sandboxID)
	if err != nil {
		// No integration exists — return empty
		writeJSON(w, http.StatusOK, map[string]any{"integration": nil})
		return
	}

	// Augment with live connection status from bridge
	if g.telegram != nil {
		if g.telegram.IsConnected(sandboxID) && ti.Status != "connected" {
			ti.Status = "connected"
		} else if !g.telegram.IsConnected(sandboxID) && ti.Status == "connected" {
			ti.Status = "disconnected"
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"integration": ti})
}

func (g *Gateway) handleCreateTelegramIntegration(w http.ResponseWriter, r *http.Request) {
	sandboxID := chi.URLParam(r, "id")

	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.BotToken == "" {
		writeError(w, http.StatusBadRequest, "bot_token is required")
		return
	}

	// Verify sandbox exists
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}
	sb, err := g.sandboxes.Get(r.Context(), sandboxID)
	if err != nil {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}

	// Delete any existing integration first
	_ = g.store.DeleteTelegramIntegration(sandboxID)

	ti, err := g.store.CreateTelegramIntegration(sandboxID, req.BotToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Auto-connect if sandbox is running
	if g.telegram != nil && sb.Status == "running" {
		if err := g.telegram.Connect(sandboxID); err != nil {
			ti.Status = "error"
			ti.ErrorMsg = err.Error()
		} else {
			ti.Status = "connected"
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{"integration": ti})
}

func (g *Gateway) handleDeleteTelegramIntegration(w http.ResponseWriter, r *http.Request) {
	sandboxID := chi.URLParam(r, "id")

	// Disconnect bot first
	if g.telegram != nil {
		g.telegram.Disconnect(sandboxID)
	}

	if err := g.store.DeleteTelegramIntegration(sandboxID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (g *Gateway) handleConnectTelegram(w http.ResponseWriter, r *http.Request) {
	sandboxID := chi.URLParam(r, "id")

	if g.telegram == nil {
		writeError(w, http.StatusServiceUnavailable, "telegram bridge not available")
		return
	}

	if err := g.telegram.Connect(sandboxID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
}

func (g *Gateway) handleDisconnectTelegram(w http.ResponseWriter, r *http.Request) {
	sandboxID := chi.URLParam(r, "id")

	if g.telegram == nil {
		writeError(w, http.StatusServiceUnavailable, "telegram bridge not available")
		return
	}

	g.telegram.Disconnect(sandboxID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}
