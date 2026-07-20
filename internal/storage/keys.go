package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// APIKey represents an API key stored in the database.
type APIKey struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Prefix        string     `json:"prefix"`
	Hash          string     `json:"-"` // Never serialize the hash
	Scope         string     `json:"scope"`
	RateLimit     int        `json:"rate_limit"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsed      *time.Time `json:"last_used,omitempty"`
	Revoked       bool       `json:"revoked"`
	Raw           string     `json:"key,omitempty"`            // Only set on creation
	TunnelAccess  bool       `json:"tunnel_access"`            // Whether key can be used via tunnel
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`     // Optional TTL
	AllowedModels []string   `json:"allowed_models,omitempty"` // nil = all models allowed
}

// CreateKeyOptions holds optional parameters for key creation.
type CreateKeyOptions struct {
	Name          string
	Scope         string
	RateLimit     int      // 0 = use default (60)
	TTL           time.Duration
	AllowedModels []string
	TunnelAccess  *bool // nil = default (true)
}

// CreateKey generates a new API key, bcrypt-hashes it, and stores it.
// The raw key is returned in the APIKey.Raw field and is never stored.
func (d *DB) CreateKey(name, scope string) (*APIKey, error) {
	return d.CreateKeyWithOptions(CreateKeyOptions{
		Name:  name,
		Scope: scope,
	})
}

// CreateKeyWithOptions generates a new API key with optional parameters.
func (d *DB) CreateKeyWithOptions(opts CreateKeyOptions) (*APIKey, error) {
	// Generate 28 bytes of cryptographic randomness
	randomBytes := make([]byte, 21) // 21 bytes → 28 base64 chars
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, fmt.Errorf("generating random bytes: %w", err)
	}

	env := "live"
	rawKey := fmt.Sprintf("sol_sk_%s_%s", env, base64.RawURLEncoding.EncodeToString(randomBytes))

	// bcrypt-hash the key for storage
	hash, err := bcrypt.GenerateFromPassword([]byte(rawKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing key: %w", err)
	}

	id := uuid.New().String()
	prefix := rawKey[:16] // sol_sk_live_xxxx — enough for lookup

	rateLimit := opts.RateLimit
	if rateLimit <= 0 {
		rateLimit = 60
	}

	tunnelAccess := true
	if opts.TunnelAccess != nil {
		tunnelAccess = *opts.TunnelAccess
	}

	scope := opts.Scope
	if scope == "" {
		scope = "user"
	}

	var expiresAt *time.Time
	if opts.TTL > 0 {
		t := time.Now().Add(opts.TTL)
		expiresAt = &t
	}

	var allowedModelsJSON *string
	if len(opts.AllowedModels) > 0 {
		data, err := json.Marshal(opts.AllowedModels)
		if err != nil {
			return nil, fmt.Errorf("marshaling allowed models: %w", err)
		}
		s := string(data)
		allowedModelsJSON = &s
	}

	_, err = d.db.Exec(
		`INSERT INTO api_keys (id, name, prefix, hash, scope, rate_limit, tunnel_access, expires_at, allowed_models) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, opts.Name, prefix, string(hash), scope, rateLimit, tunnelAccess, expiresAt, allowedModelsJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting key: %w", err)
	}

	return &APIKey{
		ID:            id,
		Name:          opts.Name,
		Prefix:        prefix,
		Scope:         scope,
		RateLimit:     rateLimit,
		CreatedAt:     time.Now(),
		Raw:           rawKey,
		TunnelAccess:  tunnelAccess,
		ExpiresAt:     expiresAt,
		AllowedModels: opts.AllowedModels,
	}, nil
}

// ValidateKey checks if a raw API key is valid, not revoked, and not expired.
// Returns the key info if valid.
func (d *DB) ValidateKey(rawKey string) (*APIKey, error) {
	if len(rawKey) < 16 {
		return nil, fmt.Errorf("invalid key format")
	}

	prefix := rawKey[:16]

	rows, err := d.db.Query(
		`SELECT id, name, prefix, hash, scope, rate_limit, created_at, revoked, tunnel_access, expires_at, allowed_models FROM api_keys WHERE prefix = ?`,
		prefix,
	)
	if err != nil {
		return nil, fmt.Errorf("querying keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var key APIKey
		var tunnelAccess sql.NullBool
		var expiresAt sql.NullTime
		var allowedModelsJSON sql.NullString

		if err := rows.Scan(&key.ID, &key.Name, &key.Prefix, &key.Hash, &key.Scope, &key.RateLimit, &key.CreatedAt, &key.Revoked, &tunnelAccess, &expiresAt, &allowedModelsJSON); err != nil {
			continue
		}

		if key.Revoked {
			continue
		}

		// Check expiry
		if expiresAt.Valid && time.Now().After(expiresAt.Time) {
			continue // expired key
		}

		// bcrypt-compare the raw key against the stored hash
		if err := bcrypt.CompareHashAndPassword([]byte(key.Hash), []byte(rawKey)); err == nil {
			// Update last_used timestamp
			_, _ = d.db.Exec(`UPDATE api_keys SET last_used = CURRENT_TIMESTAMP WHERE id = ?`, key.ID)

			key.TunnelAccess = !tunnelAccess.Valid || tunnelAccess.Bool // default true
			if expiresAt.Valid {
				key.ExpiresAt = &expiresAt.Time
			}
			if allowedModelsJSON.Valid && allowedModelsJSON.String != "" {
				var models []string
				if err := json.Unmarshal([]byte(allowedModelsJSON.String), &models); err == nil {
					key.AllowedModels = models
				}
			}

			return &key, nil
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating keys for validation: %w", err)
	}

	return nil, fmt.Errorf("invalid API key")
}

// ListKeys returns all API keys (without hashes).
func (d *DB) ListKeys() ([]APIKey, error) {
	rows, err := d.db.Query(
		`SELECT id, name, prefix, scope, rate_limit, created_at, last_used, revoked, tunnel_access, expires_at, allowed_models FROM api_keys WHERE revoked = FALSE ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []APIKey
	for rows.Next() {
		var key APIKey
		var tunnelAccess sql.NullBool
		var expiresAt sql.NullTime
		var allowedModelsJSON sql.NullString

		if err := rows.Scan(&key.ID, &key.Name, &key.Prefix, &key.Scope, &key.RateLimit, &key.CreatedAt, &key.LastUsed, &key.Revoked, &tunnelAccess, &expiresAt, &allowedModelsJSON); err != nil {
			return nil, fmt.Errorf("scanning key: %w", err)
		}

		key.TunnelAccess = !tunnelAccess.Valid || tunnelAccess.Bool
		if expiresAt.Valid {
			key.ExpiresAt = &expiresAt.Time
		}
		if allowedModelsJSON.Valid && allowedModelsJSON.String != "" {
			var models []string
			if err := json.Unmarshal([]byte(allowedModelsJSON.String), &models); err == nil {
				key.AllowedModels = models
			}
		}

		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating keys: %w", err)
	}

	return keys, nil
}

// HasKeys returns true if the database contains any non-revoked API keys.
func (d *DB) HasKeys() (bool, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE revoked = FALSE`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking for keys: %w", err)
	}
	return count > 0, nil
}

// RevokeKey marks an API key as revoked. Accepts either a key ID or raw key prefix.
func (d *DB) RevokeKey(identifier string) error {
	// Try by ID first
	result, err := d.db.Exec(`UPDATE api_keys SET revoked = TRUE WHERE id = ?`, identifier)
	if err != nil {
		return fmt.Errorf("revoking key: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		return nil
	}

	// Try by prefix match
	if len(identifier) >= 16 {
		prefix := identifier[:16]
		result, err = d.db.Exec(`UPDATE api_keys SET revoked = TRUE WHERE prefix = ?`, prefix)
		if err != nil {
			return fmt.Errorf("revoking key: %w", err)
		}
		rows, _ = result.RowsAffected()
		if rows > 0 {
			return nil
		}
	}

	return fmt.Errorf("key not found")
}

// GetUsageByKey returns request count and total tokens for each API key.
func (d *DB) GetUsageByKey() (map[string]KeyUsage, error) {
	rows, err := d.db.Query(
		`SELECT key_id, COUNT(*) as req_count, COALESCE(SUM(tokens_in + tokens_out), 0) as total_tokens
		 FROM requests WHERE key_id != '' GROUP BY key_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying usage by key: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]KeyUsage)
	for rows.Next() {
		var keyID string
		var usage KeyUsage
		if err := rows.Scan(&keyID, &usage.RequestCount, &usage.TotalTokens); err != nil {
			return nil, fmt.Errorf("scanning usage by key: %w", err)
		}
		result[keyID] = usage
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating usage by key: %w", err)
	}

	return result, nil
}

// KeyUsage holds aggregated usage data for a single API key.
type KeyUsage struct {
	RequestCount int64 `json:"request_count"`
	TotalTokens  int64 `json:"total_tokens"`
}
