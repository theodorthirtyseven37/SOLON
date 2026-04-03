package storage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey is a deterministic 32-byte key for testing.
var testKey = bytes.Repeat([]byte{0x42}, 32)

func withTestKey(t *testing.T) {
	t.Helper()
	secretKey = testKey
	t.Cleanup(func() { secretKey = nil })
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
	}{
		{"simple string", "hello world"},
		{"empty string", ""},
		{"api key", "sk-abc123xyz"},
		{"unicode", "こんにちは"},
		{"special chars", "p@$$w0rd!#&=+"},
	}

	withTestKey(t)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encrypted, err := encryptValue(tc.plaintext)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(encrypted, encryptedPrefix), "encrypted value must have enc: prefix")

			decrypted, err := decryptValue(encrypted)
			require.NoError(t, err)
			assert.Equal(t, tc.plaintext, decrypted)
		})
	}
}

func TestDecryptLegacyPlaintext(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"plain api key", "sk-abc123"},
		{"empty string", ""},
		{"no prefix", "some-plain-value"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := decryptValue(tc.value)
			require.NoError(t, err)
			assert.Equal(t, tc.value, result, "legacy plaintext should be returned unchanged")
		})
	}
}

func TestEncryptWithNilKeyReturnsPlaintext(t *testing.T) {
	secretKey = nil

	plaintext := "my-secret-key"
	result, err := encryptValue(plaintext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, result, "nil key should return plaintext unchanged")
}

func TestDecryptWithNilKeyAndEncryptedInputReturnsError(t *testing.T) {
	// First encrypt with a real key.
	withTestKey(t)
	encrypted, err := encryptValue("secret")
	require.NoError(t, err)

	// Now clear the key and attempt to decrypt.
	secretKey = nil
	_, err = decryptValue(encrypted)
	assert.Error(t, err, "decrypting with nil key should return an error")
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	withTestKey(t)

	encrypted, err := encryptValue("original plaintext")
	require.NoError(t, err)

	// Flip a byte in the base64 payload (after the "enc:" prefix).
	raw := []byte(encrypted)
	raw[len(encryptedPrefix)+5] ^= 0xFF
	tampered := string(raw)

	_, err = decryptValue(tampered)
	assert.Error(t, err, "tampered ciphertext should fail decryption")
}

func TestDecryptTruncatedCiphertext(t *testing.T) {
	withTestKey(t)

	// Build an enc: value with too few bytes to hold a nonce.
	import64 := encryptedPrefix + "dA==" // base64("t") — 1 byte, far less than nonce size
	_, err := decryptValue(import64)
	assert.Error(t, err, "truncated ciphertext should fail decryption")
}

func TestEncryptSamePlaintextProducesDifferentCiphertexts(t *testing.T) {
	withTestKey(t)

	plaintext := "same-plaintext"
	first, err := encryptValue(plaintext)
	require.NoError(t, err)

	second, err := encryptValue(plaintext)
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "random nonce must produce different ciphertexts for same plaintext")
}
