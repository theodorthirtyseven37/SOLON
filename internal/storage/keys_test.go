package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCreateKey(t *testing.T) {
	tests := []struct {
		name  string
		kname string
		scope string
	}{
		{"basic creation", "test-key", "user"},
		{"admin scope", "admin-key", "admin"},
		{"empty scope defaults", "default-key", "user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			key, err := db.CreateKey(tt.kname, tt.scope)
			require.NoError(t, err)

			assert.NotEmpty(t, key.ID)
			assert.Equal(t, tt.kname, key.Name)
			assert.Equal(t, tt.scope, key.Scope)
			assert.Equal(t, 60, key.RateLimit)
			assert.NotEmpty(t, key.Raw)
			assert.True(t, len(key.Raw) > 16, "raw key should be longer than 16 chars")
			assert.Contains(t, key.Raw, "sol_sk_live_")
			assert.Equal(t, key.Raw[:16], key.Prefix)
		})
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*DB) string // returns raw key
		rawKey    func(string) string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid key",
			setup: func(db *DB) string {
				key, _ := db.CreateKey("test", "user")
				return key.Raw
			},
			rawKey:  func(raw string) string { return raw },
			wantErr: false,
		},
		{
			name: "invalid key",
			setup: func(db *DB) string {
				_, _ = db.CreateKey("test", "user")
				return ""
			},
			rawKey:  func(_ string) string { return "sol_sk_live_invalid_key_value" },
			wantErr: true,
		},
		{
			name: "too short",
			setup: func(db *DB) string {
				return ""
			},
			rawKey:  func(_ string) string { return "short" },
			wantErr: true,
		},
		{
			name: "revoked key",
			setup: func(db *DB) string {
				key, _ := db.CreateKey("revoked", "user")
				_ = db.RevokeKey(key.ID)
				return key.Raw
			},
			rawKey:  func(raw string) string { return raw },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			raw := tt.setup(db)
			testKey := tt.rawKey(raw)

			result, err := db.ValidateKey(testKey)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "test", result.Name)
				assert.False(t, result.Revoked)
			}
		})
	}
}

func TestRevokeKey(t *testing.T) {
	tests := []struct {
		name       string
		identifier func(*DB) string
		wantErr    bool
	}{
		{
			name: "revoke by ID",
			identifier: func(db *DB) string {
				key, _ := db.CreateKey("to-revoke", "user")
				return key.ID
			},
			wantErr: false,
		},
		{
			name: "revoke by raw key",
			identifier: func(db *DB) string {
				key, _ := db.CreateKey("to-revoke", "user")
				return key.Raw
			},
			wantErr: false,
		},
		{
			name: "revoke nonexistent",
			identifier: func(db *DB) string {
				return "nonexistent-id"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			id := tt.identifier(db)

			err := db.RevokeKey(id)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestListKeys(t *testing.T) {
	db := testDB(t)

	// Empty list
	keys, err := db.ListKeys()
	require.NoError(t, err)
	assert.Empty(t, keys)

	// Create some keys
	_, _ = db.CreateKey("key-1", "user")
	_, _ = db.CreateKey("key-2", "admin")

	keys, err = db.ListKeys()
	require.NoError(t, err)
	assert.Len(t, keys, 2)

	// Revoke one — shouldn't show in list
	_ = db.RevokeKey(keys[0].ID)
	keys, err = db.ListKeys()
	require.NoError(t, err)
	assert.Len(t, keys, 1)
}

func TestKeyBcrypt(t *testing.T) {
	db := testDB(t)

	key, err := db.CreateKey("bcrypt-test", "user")
	require.NoError(t, err)

	// Raw key should not be stored in the database
	rows, err := db.db.Query(`SELECT hash FROM api_keys WHERE id = ?`, key.ID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var hash string
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&hash))

	// Hash should not equal the raw key
	assert.NotEqual(t, key.Raw, hash)
	// Hash should start with bcrypt prefix
	assert.Contains(t, hash, "$2a$")
}

func TestHasKeys(t *testing.T) {
	db := testDB(t)

	// Empty DB — no keys
	has, err := db.HasKeys()
	require.NoError(t, err)
	assert.False(t, has)

	// Create a key — now has keys
	key, err := db.CreateKey("test", "user")
	require.NoError(t, err)
	has, err = db.HasKeys()
	require.NoError(t, err)
	assert.True(t, has)

	// Revoke all keys — back to no keys
	err = db.RevokeKey(key.ID)
	require.NoError(t, err)
	has, err = db.HasKeys()
	require.NoError(t, err)
	assert.False(t, has)
}

func TestGetUsageByKey(t *testing.T) {
	db := testDB(t)

	// Empty usage
	usage, err := db.GetUsageByKey()
	require.NoError(t, err)
	assert.Empty(t, usage)

	// Create keys and log requests
	key1, _ := db.CreateKey("key-1", "user")
	key2, _ := db.CreateKey("key-2", "user")

	_ = db.LogRequest(key1.ID, "POST", "/v1/chat/completions", "llama3.2:8b", 100, 50, 200, 200, "")
	_ = db.LogRequest(key1.ID, "POST", "/v1/chat/completions", "llama3.2:8b", 200, 100, 300, 200, "")
	_ = db.LogRequest(key2.ID, "POST", "/v1/embeddings", "nomic-embed-text", 50, 0, 50, 200, "")

	usage, err = db.GetUsageByKey()
	require.NoError(t, err)
	assert.Len(t, usage, 2)

	assert.Equal(t, int64(2), usage[key1.ID].RequestCount)
	assert.Equal(t, int64(450), usage[key1.ID].TotalTokens) // (100+50) + (200+100)
	assert.Equal(t, int64(1), usage[key2.ID].RequestCount)
	assert.Equal(t, int64(50), usage[key2.ID].TotalTokens)
}

func TestOpenDefaultPath(t *testing.T) {
	// Override HOME to temp dir to avoid touching real home
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", dir)
	defer func() { _ = os.Setenv("HOME", origHome) }()

	db, err := Open("")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Should have created ~/.solon/solon.db
	_, err = os.Stat(filepath.Join(dir, ".solon", "solon.db"))
	assert.NoError(t, err)
}
