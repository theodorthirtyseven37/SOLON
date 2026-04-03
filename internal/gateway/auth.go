package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type contextKey string

const keyContextKey contextKey = "api_key"

// Authenticate is middleware that validates API keys on every request.
// Auth is mandatory — there is no way to disable it.
func (g *Gateway) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing Authorization header")
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "invalid Authorization header format, expected 'Bearer <key>'")
			return
		}

		rawKey := strings.TrimPrefix(authHeader, "Bearer ")
		if !strings.HasPrefix(rawKey, "sol_sk_") {
			writeError(w, http.StatusUnauthorized, "invalid API key format")
			return
		}

		apiKey, err := g.store.ValidateKey(rawKey)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}

		// Check tunnel access: if request came through tunnel and key has tunnel_access=false, reject
		if !apiKey.TunnelAccess && isTunnelRequest(r) {
			writeError(w, http.StatusForbidden, "this API key does not have tunnel access")
			return
		}

		// Convert to gateway KeyInfo for downstream handlers
		keyInfo := &KeyInfo{
			ID:            apiKey.ID,
			Name:          apiKey.Name,
			Scope:         apiKey.Scope,
			RateLimit:     apiKey.RateLimit,
			AllowedModels: apiKey.AllowedModels,
		}

		ctx := context.WithValue(r.Context(), keyContextKey, keyInfo)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LocalhostOrAuth allows requests from localhost without auth (for the dashboard),
// but requires API key auth for remote requests.
func (g *Gateway) LocalhostOrAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLocalhost(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Remote request — require auth
		g.Authenticate(next).ServeHTTP(w, r)
	})
}

func isLocalhost(r *http.Request) bool {
	// Reject if any proxy headers are present — these indicate the request
	// was forwarded from an external client through a reverse proxy.
	if r.Header.Get("X-Forwarded-For") != "" ||
		r.Header.Get("X-Real-Ip") != "" ||
		r.Header.Get("Cf-Connecting-Ip") != "" {
		return false
	}

	// Parse the actual TCP peer address (not spoofable)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false // Malformed RemoteAddr — reject
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// isTunnelRequest detects if a request came through a Cloudflare tunnel.
// Cloudflare sets Cf-Connecting-Ip header on tunneled requests.
func isTunnelRequest(r *http.Request) bool {
	return r.Header.Get("Cf-Connecting-Ip") != "" || r.Header.Get("Cf-Ray") != ""
}

// NormalizeAnthropicAuth converts x-api-key header to Authorization Bearer format.
// This allows Anthropic-native clients (like Claude Code) to authenticate with Solon
// keys via the x-api-key header they normally send to api.anthropic.com.
func NormalizeAnthropicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if xAPIKey := r.Header.Get("x-api-key"); xAPIKey != "" {
				r.Header.Set("Authorization", "Bearer "+xAPIKey)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// CheckModelAccess verifies that the authenticated key is allowed to use the requested model.
// Returns nil if allowed, or an error message if restricted.
func CheckModelAccess(r *http.Request, model string) error {
	keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo)
	if !ok || keyInfo == nil {
		return nil // localhost access, no restrictions
	}
	if len(keyInfo.AllowedModels) == 0 {
		return nil // no restrictions
	}
	for _, allowed := range keyInfo.AllowedModels {
		if allowed == model {
			return nil
		}
	}
	return fmt.Errorf("model %q not allowed for this API key", model)
}
