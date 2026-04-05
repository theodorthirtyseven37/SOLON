package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// SandboxResolver returns the container IP for a sandbox. Returns an error
// if the sandbox is not running.
type SandboxResolver func(ctx context.Context, sandboxID string) (string, error)

// Store abstracts the storage operations needed by the bridge.
type Store interface {
	GetTelegramBotToken(sandboxID string) (string, error)
	UpdateTelegramStatus(sandboxID, status, errorMsg, botUsername string) error
}

// Bridge manages Telegram bot goroutines — one per connected sandbox.
type Bridge struct {
	resolver SandboxResolver
	store    Store
	client   *http.Client

	mu   sync.Mutex
	bots map[string]context.CancelFunc // sandboxID -> cancel
}

// New creates a new Bridge.
func New(resolver SandboxResolver, store Store) *Bridge {
	return &Bridge{
		resolver: resolver,
		store:    store,
		client:   &http.Client{Timeout: 35 * time.Second}, // slightly above Telegram long-poll timeout
		bots:     make(map[string]context.CancelFunc),
	}
}

// Connect starts a polling goroutine for the given sandbox.
// If already connected, it disconnects first.
func (b *Bridge) Connect(sandboxID string) error {
	b.Disconnect(sandboxID)

	token, err := b.store.GetTelegramBotToken(sandboxID)
	if err != nil {
		return fmt.Errorf("loading bot token: %w", err)
	}

	// Validate token via getMe
	username, err := b.getMe(token)
	if err != nil {
		_ = b.store.UpdateTelegramStatus(sandboxID, "error", err.Error(), "")
		return fmt.Errorf("validating bot token: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	b.mu.Lock()
	b.bots[sandboxID] = cancel
	b.mu.Unlock()

	_ = b.store.UpdateTelegramStatus(sandboxID, "connected", "", username)
	log.Printf("[telegram] connected bot @%s for sandbox %s", username, sandboxID)

	go b.poll(ctx, sandboxID, token)
	return nil
}

// Disconnect stops the polling goroutine for a sandbox.
func (b *Bridge) Disconnect(sandboxID string) {
	b.mu.Lock()
	cancel, ok := b.bots[sandboxID]
	if ok {
		delete(b.bots, sandboxID)
	}
	b.mu.Unlock()

	if ok {
		cancel()
		_ = b.store.UpdateTelegramStatus(sandboxID, "disconnected", "", "")
		log.Printf("[telegram] disconnected bot for sandbox %s", sandboxID)
	}
}

// IsConnected returns true if a bot is currently polling for the sandbox.
func (b *Bridge) IsConnected(sandboxID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.bots[sandboxID]
	return ok
}

// Shutdown stops all polling goroutines.
func (b *Bridge) Shutdown() {
	b.mu.Lock()
	bots := make(map[string]context.CancelFunc, len(b.bots))
	for k, v := range b.bots {
		bots[k] = v
	}
	b.bots = make(map[string]context.CancelFunc)
	b.mu.Unlock()

	for id, cancel := range bots {
		cancel()
		_ = b.store.UpdateTelegramStatus(id, "disconnected", "", "")
	}
	log.Printf("[telegram] shutdown: stopped %d bots", len(bots))
}

// poll runs the Telegram long-poll loop.
func (b *Bridge) poll(ctx context.Context, sandboxID, token string) {
	var offset int64
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := b.getUpdates(ctx, token, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[telegram] poll error for sandbox %s: %v", sandboxID, err)
			_ = b.store.UpdateTelegramStatus(sandboxID, "error", err.Error(), "")

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second
		if len(updates) > 0 {
			_ = b.store.UpdateTelegramStatus(sandboxID, "connected", "", "")
		}

		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message == nil || u.Message.Text == "" {
				continue
			}

			reply, err := b.forwardToSandbox(ctx, sandboxID, u.Message.Text)
			if err != nil {
				log.Printf("[telegram] forward error sandbox=%s: %v", sandboxID, err)
				reply = "Sorry, I'm having trouble right now. Please try again."
			}

			if err := b.sendMessage(ctx, token, u.Message.Chat.ID, reply); err != nil {
				log.Printf("[telegram] send error sandbox=%s chat=%d: %v", sandboxID, u.Message.Chat.ID, err)
			}
		}
	}
}

// forwardToSandbox sends a message to the sandbox's agent API and returns the response text.
func (b *Bridge) forwardToSandbox(ctx context.Context, sandboxID, text string) (string, error) {
	ip, err := b.resolver(ctx, sandboxID)
	if err != nil {
		return "", fmt.Errorf("resolving sandbox IP: %w", err)
	}

	payload, err := json.Marshal(map[string]string{"message": text})
	if err != nil {
		return "", err
	}

	agentURL := fmt.Sprintf("http://%s:18790/send", ip)
	req, err := http.NewRequestWithContext(ctx, "POST", agentURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("agent request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading agent response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("agent returned %d: %s", resp.StatusCode, string(body))
	}

	// Try to parse JSON response with "response" or "message" field
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err == nil {
		for _, key := range []string{"response", "message", "text", "content"} {
			if raw, ok := parsed[key]; ok {
				var s string
				if json.Unmarshal(raw, &s) == nil && s != "" {
					return s, nil
				}
			}
		}
	}

	// Fall back to raw body
	return string(body), nil
}

// --- Telegram Bot API ---

const telegramAPI = "https://api.telegram.org/bot"

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message,omitempty"`
}

type tgMessage struct {
	MessageID int64  `json:"message_id"`
	Chat      tgChat `json:"chat"`
	Text      string `json:"text"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

func (b *Bridge) getMe(token string) (string, error) {
	resp, err := b.client.Get(telegramAPI + token + "/getMe")
	if err != nil {
		return "", fmt.Errorf("getMe request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding getMe: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("invalid bot token")
	}
	return result.Result.Username, nil
}

func (b *Bridge) getUpdates(ctx context.Context, token string, offset int64) ([]tgUpdate, error) {
	url := fmt.Sprintf("%s%s/getUpdates?timeout=30&offset=%d&allowed_updates=[\"message\"]", telegramAPI, token, offset)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getUpdates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		OK     bool       `json:"ok"`
		Result []tgUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding updates: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("Telegram API error on getUpdates")
	}
	return result.Result, nil
}

func (b *Bridge) sendMessage(ctx context.Context, token string, chatID int64, text string) error {
	// Telegram has a 4096-character limit per message — split if needed
	const maxLen = 4096
	for len(text) > 0 {
		chunk := text
		if len(chunk) > maxLen {
			chunk = text[:maxLen]
			text = text[maxLen:]
		} else {
			text = ""
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"chat_id": chatID,
			"text":    chunk,
		})

		req, err := http.NewRequestWithContext(ctx, "POST", telegramAPI+token+"/sendMessage", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := b.client.Do(req)
		if err != nil {
			return fmt.Errorf("sendMessage: %w", err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("sendMessage returned %d", resp.StatusCode)
		}
	}
	return nil
}
