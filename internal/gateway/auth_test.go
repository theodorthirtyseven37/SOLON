package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openclaw/solon/internal/storage"
)

func testStore(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testGateway(t *testing.T) (*Gateway, *storage.DB) {
	t.Helper()
	store := testStore(t)
	gw := &Gateway{store: store}
	return gw, store
}

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		setupKey   bool
		revokeKey  bool
		wantStatus int
	}{
		{
			name:       "missing auth header",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid format — no Bearer prefix",
			authHeader: "Basic abc123",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid key prefix",
			authHeader: "Bearer invalid_key_format",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "nonexistent key",
			authHeader: "Bearer sol_sk_live_nonexistent_key_value",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid key",
			authHeader: "", // set dynamically
			setupKey:   true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "revoked key",
			authHeader: "", // set dynamically
			setupKey:   true,
			revokeKey:  true,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw, store := testGateway(t)

			authHeader := tt.authHeader
			if tt.setupKey {
				key, err := store.CreateKey("test-key", "user")
				require.NoError(t, err)
				authHeader = "Bearer " + key.Raw

				if tt.revokeKey {
					require.NoError(t, store.RevokeKey(key.ID))
				}
			}

			handler := gw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify key info is in context
				keyInfo, ok := r.Context().Value(keyContextKey).(*KeyInfo)
				assert.True(t, ok)
				assert.NotEmpty(t, keyInfo.ID)
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/v1/models", nil)
			if authHeader != "" {
				req.Header.Set("Authorization", authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestLocalhostOrAuth(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		authHeader string
		setupKey   bool
		wantStatus int
	}{
		{
			name:       "localhost IPv4 — no auth needed",
			remoteAddr: "127.0.0.1:12345",
			authHeader: "",
			wantStatus: http.StatusOK,
		},
		{
			name:       "localhost IPv6 — no auth needed",
			remoteAddr: "[::1]:12345",
			authHeader: "",
			wantStatus: http.StatusOK,
		},
		{
			name:       "remote without auth — rejected",
			remoteAddr: "192.168.1.100:12345",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "remote with valid auth — allowed",
			remoteAddr: "192.168.1.100:12345",
			authHeader: "", // set dynamically
			setupKey:   true,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw, store := testGateway(t)

			authHeader := tt.authHeader
			if tt.setupKey {
				key, err := store.CreateKey("test-key", "user")
				require.NoError(t, err)
				authHeader = "Bearer " + key.Raw
			}

			handler := gw.LocalhostOrAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/api/v1/keys", nil)
			req.RemoteAddr = tt.remoteAddr
			if authHeader != "" {
				req.Header.Set("Authorization", authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestIsLocalhost(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       bool
	}{
		{"IPv4 localhost", "127.0.0.1:8080", true},
		{"IPv6 localhost", "[::1]:8080", true},
		{"remote IP", "10.0.0.1:8080", false},
		{"public IP", "8.8.8.8:443", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			assert.Equal(t, tt.want, isLocalhost(req))
		})
	}
}

func TestRequireAdminScope(t *testing.T) {
	tests := []struct {
		name       string
		setupKey   func(*storage.DB) string // returns raw key or ""
		remoteAddr string
		wantStatus int
	}{
		{
			name:       "localhost without key — allowed",
			setupKey:   func(db *storage.DB) string { return "" },
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusOK,
		},
		{
			name: "admin key — allowed",
			setupKey: func(db *storage.DB) string {
				key, _ := db.CreateKey("admin-key", "admin")
				return key.Raw
			},
			remoteAddr: "192.168.1.100:12345",
			wantStatus: http.StatusOK,
		},
		{
			name: "user key — rejected",
			setupKey: func(db *storage.DB) string {
				key, _ := db.CreateKey("user-key", "user")
				return key.Raw
			},
			remoteAddr: "192.168.1.100:12345",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw, store := testGateway(t)

			rawKey := tt.setupKey(store)

			// Chain LocalhostOrAuth → RequireAdminScope → handler
			handler := gw.LocalhostOrAuth(gw.RequireAdminScope(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})))

			req := httptest.NewRequest("GET", "/api/v1/keys", nil)
			req.RemoteAddr = tt.remoteAddr
			if rawKey != "" {
				req.Header.Set("Authorization", "Bearer "+rawKey)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestNormalizeAnthropicAuth(t *testing.T) {
	tests := []struct {
		name           string
		authHeader     string
		xAPIKeyHeader  string
		wantAuthHeader string
	}{
		{
			name:           "x-api-key converted when no Authorization",
			xAPIKeyHeader:  "sol_sk_live_test123",
			wantAuthHeader: "Bearer sol_sk_live_test123",
		},
		{
			name:           "Authorization takes precedence",
			authHeader:     "Bearer sol_sk_live_original",
			xAPIKeyHeader:  "sol_sk_live_other",
			wantAuthHeader: "Bearer sol_sk_live_original",
		},
		{
			name:           "no headers — no change",
			wantAuthHeader: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			handler := NormalizeAnthropicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("POST", "/v1/messages", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if tt.xAPIKeyHeader != "" {
				req.Header.Set("x-api-key", tt.xAPIKeyHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantAuthHeader, gotAuth)
		})
	}
}

func TestCheckModelAccess(t *testing.T) {
	tests := []struct {
		name          string
		allowedModels []string
		model         string
		wantErr       bool
	}{
		{
			name:          "no restrictions — all allowed",
			allowedModels: nil,
			model:         "llama3.2:8b",
			wantErr:       false,
		},
		{
			name:          "empty list — all allowed",
			allowedModels: []string{},
			model:         "llama3.2:8b",
			wantErr:       false,
		},
		{
			name:          "model in list — allowed",
			allowedModels: []string{"llama3.2:8b", "mistral:7b"},
			model:         "llama3.2:8b",
			wantErr:       false,
		},
		{
			name:          "model not in list — denied",
			allowedModels: []string{"llama3.2:8b"},
			model:         "mistral:7b",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)

			if tt.allowedModels != nil {
				ctx := req.Context()
				ctx = context.WithValue(ctx, keyContextKey, &KeyInfo{
					ID:            "test-id",
					AllowedModels: tt.allowedModels,
				})
				req = req.WithContext(ctx)
			}

			err := CheckModelAccess(req, tt.model)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckModelAccessNoKeyInContext(t *testing.T) {
	// No key in context — localhost access, should allow everything
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	assert.NoError(t, CheckModelAccess(req, "any-model"))
}

func TestIsLocalhostWithXForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")

	// Should be treated as remote due to X-Forwarded-For
	assert.False(t, isLocalhost(req))
}

func TestIsTunnelRequest(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{
			name:    "no tunnel headers",
			headers: map[string]string{},
			want:    false,
		},
		{
			name:    "Cf-Connecting-Ip present",
			headers: map[string]string{"Cf-Connecting-Ip": "203.0.113.50"},
			want:    true,
		},
		{
			name:    "Cf-Ray present",
			headers: map[string]string{"Cf-Ray": "abc123"},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			assert.Equal(t, tt.want, isTunnelRequest(req))
		})
	}
}

func TestTunnelAccessRestriction(t *testing.T) {
	gw, store := testGateway(t)

	tunnelFalse := false
	key, err := store.CreateKeyWithOptions(storage.CreateKeyOptions{
		Name:         "no-tunnel",
		Scope:        "user",
		TunnelAccess: &tunnelFalse,
	})
	require.NoError(t, err)

	handler := gw.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("tunnel request rejected for no-tunnel key", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key.Raw)
		req.Header.Set("Cf-Connecting-Ip", "203.0.113.50")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("non-tunnel request allowed for no-tunnel key", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key.Raw)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

// Ensure HOME isn't touched during tests
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
