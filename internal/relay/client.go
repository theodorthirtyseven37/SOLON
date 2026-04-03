package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	defaultRelayHost = "relay.getsolon.dev"
	reconnectMin     = 1 * time.Second
	reconnectMax     = 30 * time.Second
	pingInterval     = 25 * time.Second
)

// Client connects to the Solon relay and proxies requests to the local HTTP server.
type Client struct {
	instanceID string
	relayHost  string
	localPort  int
	version    string
	remoteURL  string // the public URL assigned by relay
	connected  bool
	mu         sync.RWMutex
	cancel     context.CancelFunc
}

// NewClient creates a new relay client.
func NewClient(instanceID string, localPort int, version string) *Client {
	return &Client{
		instanceID: instanceID,
		relayHost:  defaultRelayHost,
		localPort:  localPort,
		version:    version,
	}
}

// RemoteURL returns the public URL for this instance (empty if not connected).
func (c *Client) RemoteURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteURL
}

// Connected returns true if the relay connection is active.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Start connects to the relay and begins proxying. Reconnects automatically.
// Blocks until ctx is cancelled.
func (c *Client) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	defer cancel()

	backoff := reconnectMin

	for {
		err := c.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()

		if err != nil {
			slog.Warn("relay disconnected, reconnecting", "error", err, "backoff", backoff.String())
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		// Exponential backoff
		backoff = backoff * 2
		if backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// Stop disconnects from the relay.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Client) connect(ctx context.Context) error {
	url := fmt.Sprintf("wss://%s/api/connect/%s", c.relayHost, c.instanceID)
	slog.Info("relay connecting", "url", url)

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("connecting to relay: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()

	// Send init message
	init := InitMsg{
		Type:       "init",
		InstanceID: c.instanceID,
		Version:    c.version,
	}
	initData, _ := json.Marshal(init)
	if err := conn.Write(ctx, websocket.MessageText, initData); err != nil {
		return fmt.Errorf("sending init: %w", err)
	}

	// Read init_ok
	_, data, err := conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("reading init response: %w", err)
	}

	var initOK InitOKMsg
	if err := json.Unmarshal(data, &initOK); err == nil && initOK.URL != "" {
		c.mu.Lock()
		c.remoteURL = initOK.URL
		c.connected = true
		c.mu.Unlock()
		slog.Info("relay connected", "remote_url", initOK.URL)
	} else {
		c.mu.Lock()
		c.remoteURL = fmt.Sprintf("https://%s/%s", c.relayHost, c.instanceID)
		c.connected = true
		c.mu.Unlock()
	}

	// Reset backoff on successful connection
	// Start ping goroutine
	go c.pingLoop(ctx, conn)

	// Read messages
	for {
		_, msgData, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading message: %w", err)
		}

		var generic GenericMsg
		if err := json.Unmarshal(msgData, &generic); err != nil {
			continue
		}

		switch generic.Type {
		case "request":
			var req RequestMsg
			if err := json.Unmarshal(msgData, &req); err != nil {
				continue
			}
			go c.handleRequest(ctx, conn, &req)

		case "ping":
			pong, _ := json.Marshal(map[string]string{"type": "pong"})
			_ = conn.Write(ctx, websocket.MessageText, pong)
		}
	}
}

func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ping, _ := json.Marshal(map[string]string{"type": "ping"})
			if err := conn.Write(ctx, websocket.MessageText, ping); err != nil {
				return
			}
		}
	}
}

func (c *Client) handleRequest(ctx context.Context, conn *websocket.Conn, req *RequestMsg) {
	// Build local HTTP request
	localURL := fmt.Sprintf("http://localhost:%d%s", c.localPort, req.Path)

	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = strings.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, localURL, bodyReader)
	if err != nil {
		c.sendError(ctx, conn, req.ID, 500, "failed to build request")
		return
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	// Mark as relay request so auth middleware can detect it
	httpReq.Header.Set("X-Relay-Request", "true")

	// Check if this is a streaming request
	isStream := req.Headers["accept"] == "text/event-stream" ||
		strings.Contains(req.Body, `"stream":true`) ||
		strings.Contains(req.Body, `"stream": true`)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		c.sendError(ctx, conn, req.ID, 502, "local server error: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Build response headers
	headers := make(map[string]string)
	for _, key := range []string{"Content-Type", "X-Request-ID"} {
		if v := resp.Header.Get(key); v != "" {
			headers[strings.ToLower(key)] = v
		}
	}

	if isStream && resp.Header.Get("Content-Type") == "text/event-stream" {
		c.streamResponse(ctx, conn, req.ID, resp, headers)
		return
	}

	// Non-streaming: read full body, send as single response
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB limit
	if err != nil {
		c.sendError(ctx, conn, req.ID, 500, "failed to read response")
		return
	}

	msg := ResponseMsg{
		Type:    "response",
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    string(body),
	}
	data, _ := json.Marshal(msg)
	_ = conn.Write(ctx, websocket.MessageText, data)
}

func (c *Client) streamResponse(ctx context.Context, conn *websocket.Conn, reqID string, resp *http.Response, headers map[string]string) {
	// Send stream start
	start := StreamStartMsg{
		Type:    "response_start",
		ID:      reqID,
		Status:  resp.StatusCode,
		Headers: headers,
	}
	data, _ := json.Marshal(start)
	_ = conn.Write(ctx, websocket.MessageText, data)

	// Read and forward chunks
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := StreamChunkMsg{
				Type: "response_chunk",
				ID:   reqID,
				Data: string(buf[:n]),
			}
			chunkData, _ := json.Marshal(chunk)
			_ = conn.Write(ctx, websocket.MessageText, chunkData)
		}
		if err != nil {
			break
		}
	}

	// Send stream end
	end := StreamEndMsg{
		Type: "response_end",
		ID:   reqID,
	}
	endData, _ := json.Marshal(end)
	_ = conn.Write(ctx, websocket.MessageText, endData)
}

func (c *Client) sendError(ctx context.Context, conn *websocket.Conn, reqID string, status int, message string) {
	errBody, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": message},
	})
	msg := ResponseMsg{
		Type:    "response",
		ID:      reqID,
		Status:  status,
		Headers: map[string]string{"content-type": "application/json"},
		Body:    string(errBody),
	}
	data, _ := json.Marshal(msg)
	_ = conn.Write(ctx, websocket.MessageText, data)
}

// Status returns the current relay connection status.
func (c *Client) Status() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]any{
		"enabled":     c.connected,
		"url":         c.remoteURL,
		"instance_id": c.instanceID,
		"provider":    "solon-relay",
	}
}

// EnsureRegistered loads or creates an instance ID.
func EnsureRegistered() (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}

	if cfg != nil && cfg.InstanceID != "" {
		return cfg.InstanceID, nil
	}

	// First time — generate instance ID
	id, err := GenerateInstanceID()
	if err != nil {
		return "", fmt.Errorf("generating instance ID: %w", err)
	}

	cfg = &InstanceConfig{
		InstanceID: id,
		RelayURL:   fmt.Sprintf("https://%s/%s", defaultRelayHost, id),
	}
	if err := SaveConfig(cfg); err != nil {
		return "", fmt.Errorf("saving relay config: %w", err)
	}

	return id, nil
}

