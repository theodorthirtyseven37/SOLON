package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTelegramIntegrationCRUD(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Create a sandbox first (integration has FK)
	_, err = db.db.Exec(`INSERT INTO sandboxes (id, name, status, policy) VALUES ('sb-1', 'test-sandbox', 'running', 'api-only')`)
	require.NoError(t, err)

	// Create integration
	ti, err := db.CreateTelegramIntegration("sb-1", "123456:ABC-DEF")
	require.NoError(t, err)
	assert.Equal(t, "sb-1", ti.SandboxID)
	assert.Equal(t, "disconnected", ti.Status)
	assert.NotEmpty(t, ti.ID)

	// Get integration
	got, err := db.GetTelegramIntegration("sb-1")
	require.NoError(t, err)
	assert.Equal(t, ti.ID, got.ID)
	assert.Equal(t, "disconnected", got.Status)

	// Get token (should be decryptable)
	token, err := db.GetTelegramBotToken("sb-1")
	require.NoError(t, err)
	assert.Equal(t, "123456:ABC-DEF", token)

	// Update status
	err = db.UpdateTelegramStatus("sb-1", "connected", "", "test_bot")
	require.NoError(t, err)

	got, err = db.GetTelegramIntegration("sb-1")
	require.NoError(t, err)
	assert.Equal(t, "connected", got.Status)
	assert.Equal(t, "test_bot", got.BotUsername)

	// List
	all, err := db.ListTelegramIntegrations()
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, "connected", all[0].Status)

	// Delete
	err = db.DeleteTelegramIntegration("sb-1")
	require.NoError(t, err)

	_, err = db.GetTelegramIntegration("sb-1")
	assert.Error(t, err)
}

func TestTelegramTokenEncryption(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Secret key is initialized (may be from this or a prior test's Open call)
	assert.NotNil(t, secretKey, "secret key should be initialized")

	_, err = db.db.Exec(`INSERT INTO sandboxes (id, name, status, policy) VALUES ('sb-enc', 'enc-test', 'running', 'api-only')`)
	require.NoError(t, err)

	_, err = db.CreateTelegramIntegration("sb-enc", "sensitive-token-value")
	require.NoError(t, err)

	// Verify stored value is encrypted (not plaintext)
	var raw string
	err = db.db.QueryRow(`SELECT bot_token FROM telegram_integrations WHERE sandbox_id = 'sb-enc'`).Scan(&raw)
	require.NoError(t, err)
	assert.True(t, len(raw) > 0)
	assert.NotEqual(t, "sensitive-token-value", raw, "token should not be stored as plaintext")
	assert.Contains(t, raw, "enc:", "token should be encrypted with enc: prefix")

	// Verify decryption works
	token, err := db.GetTelegramBotToken("sb-enc")
	require.NoError(t, err)
	assert.Equal(t, "sensitive-token-value", token)
}
