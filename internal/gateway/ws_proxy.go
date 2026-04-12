package gateway

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	openclawGatewayPort  = 18789
	openclawGatewayToken = "solon-openclaw-token"
	wsReadLimit          = 1 << 20    // 1 MB per message
	wsProxyTimeout       = 10 * time.Minute
	maxWSConnections     = 10
)

var activeWSConns atomic.Int32

// WSAuthFromQueryParam extracts a token from the ?token= query parameter
// and sets it as the Authorization header. This allows browser WebSocket
// connections to authenticate (browsers can't set custom WS headers).
func WSAuthFromQueryParam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if token := r.URL.Query().Get("token"); token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// UIAuthFromCookieOrQuery extracts a token from the "solon-ui-token" cookie
// or the ?token= query parameter and sets it as the Authorization header.
// This allows browser iframe/asset requests to authenticate (iframes can't
// set custom headers; cookies are the only same-origin auth mechanism
// automatically included by the browser).
func UIAuthFromCookieOrQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if token := r.URL.Query().Get("token"); token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			} else if c, err := r.Cookie("solon-ui-token"); err == nil && c.Value != "" {
				r.Header.Set("Authorization", "Bearer "+c.Value)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isWebSocketUpgrade returns true when the request is an HTTP→WebSocket upgrade.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// handleOpenClawWS upgrades an HTTP connection to WebSocket and proxies
// it bidirectionally to the OpenClaw gateway inside the Docker container.
// Auth is validated by middleware BEFORE this handler is called.
func (g *Gateway) handleOpenClawWS(w http.ResponseWriter, r *http.Request) {
	// Guard: sandbox manager must be available
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available (Docker not detected)")
		return
	}

	// Resolve upstream OpenClaw container IP
	// Connect to the WS bridge port (gateway+1). The bridge runs inside the
	// container and connects to the gateway on localhost (gets admin scopes).
	// Solon connects to the bridge via the Docker bridge network.
	containerIP, err := g.sandboxes.OpenClawContainerIP(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "OpenClaw is not running — start it first via the dashboard or 'solon openclaw'")
		return
	}
	upstreamURL := fmt.Sprintf("ws://%s:%d", containerIP, openclawGatewayPort+1)
	g.proxyWS(w, r, upstreamURL)
}

// proxyWS is the shared WebSocket bidirectional-proxy routine used by both
// the /api/v1/openclaw/ws bridge and the UI reverse proxy's WS upgrades.
func (g *Gateway) proxyWS(w http.ResponseWriter, r *http.Request, upstreamURL string) {
	// Guard: connection limit
	if activeWSConns.Load() >= maxWSConnections {
		writeError(w, http.StatusServiceUnavailable, "too many WebSocket connections")
		return
	}

	// Validate origin (before upgrade)
	if !g.isAllowedWSOrigin(r) {
		writeError(w, http.StatusForbidden, "WebSocket origin not allowed")
		return
	}

	// Accept browser WebSocket
	browserConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // We validate origin manually above
	})
	if err != nil {
		log.Printf("[ws-proxy] accept error: %v", err)
		return
	}
	defer func() { _ = browserConn.CloseNow() }()
	browserConn.SetReadLimit(wsReadLimit)

	activeWSConns.Add(1)
	defer activeWSConns.Add(-1)

	// Connect to upstream OpenClaw gateway
	ctx, cancel := context.WithTimeout(r.Context(), wsProxyTimeout)
	defer cancel()

	upstreamConn, _, err := websocket.Dial(ctx, upstreamURL, nil)
	if err != nil {
		log.Printf("[ws-proxy] upstream dial error: %v", err)
		_ = browserConn.Close(websocket.StatusBadGateway, "failed to connect to OpenClaw gateway")
		return
	}
	defer func() { _ = upstreamConn.CloseNow() }()
	upstreamConn.SetReadLimit(wsReadLimit)

	log.Printf("[ws-proxy] connected: browser <-> %s", upstreamURL)

	// Bidirectional proxy
	proxyWebSocket(ctx, cancel, browserConn, upstreamConn)

	log.Printf("[ws-proxy] disconnected")
}

// proxyWebSocket bridges two WebSocket connections bidirectionally.
func proxyWebSocket(ctx context.Context, cancel context.CancelFunc, browser, upstream *websocket.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// browser → upstream
	go func() {
		defer wg.Done()
		defer cancel()
		if err := pipeWS(ctx, upstream, browser); err != nil {
			log.Printf("[ws-proxy] browser→upstream: %v", err)
		}
	}()

	// upstream → browser
	go func() {
		defer wg.Done()
		defer cancel()
		if err := pipeWS(ctx, browser, upstream); err != nil {
			log.Printf("[ws-proxy] upstream→browser: %v", err)
		}
	}()

	wg.Wait()

	// Close both connections gracefully
	_ = browser.Close(websocket.StatusNormalClosure, "proxy closed")
	_ = upstream.Close(websocket.StatusNormalClosure, "proxy closed")
}

// pipeWS copies messages from src to dst until error or context cancellation.
func pipeWS(ctx context.Context, dst, src *websocket.Conn) error {
	for {
		msgType, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, msgType, data); err != nil {
			return err
		}
	}
}

// isAllowedWSOrigin checks if the WebSocket Origin header is allowed.
func (g *Gateway) isAllowedWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")

	// No origin header (non-browser client like wscat) — allow
	if origin == "" {
		return true
	}

	// Same-origin (the Origin host matches the request Host) — always allow.
	// This covers the reverse-proxied OpenClaw UI iframe which lives on the
	// same server as Solon itself.
	if host := r.Host; host != "" {
		if strings.HasSuffix(origin, "://"+host) {
			return true
		}
	}

	// Localhost origins always allowed
	if strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1") ||
		strings.HasPrefix(origin, "https://localhost") ||
		strings.HasPrefix(origin, "https://127.0.0.1") {
		return true
	}

	// Dashboard URL
	if strings.HasPrefix(origin, "https://app.getsolon.dev") {
		return true
	}

	// Relay/tunnel URLs if configured
	if g.relay != nil && g.relay.RemoteURL() != "" {
		if strings.HasPrefix(origin, g.relay.RemoteURL()) {
			return true
		}
	}
	if g.tunnel != nil {
		if status, err := g.tunnel.Status(r.Context()); err == nil && status.URL != "" {
			if strings.HasPrefix(origin, status.URL) {
				return true
			}
		}
	}

	return false
}
