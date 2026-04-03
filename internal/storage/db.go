package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps a SQLite database connection with Solon-specific operations.
type DB struct {
	db *sql.DB
}

// Open opens or creates the Solon database. If path is empty, uses ~/.solon/solon.db.
func Open(path string) (*DB, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("getting home directory: %w", err)
		}
		dir := filepath.Join(home, ".solon")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("creating data directory: %w", err)
		}
		path = filepath.Join(dir, "solon.db")
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Enable WAL mode for concurrent reads
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	store := &DB{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return store, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// SchemaVersion is the current schema version. Bump this when adding new migrations.
const SchemaVersion = 5

func (d *DB) migrate() error {
	// Create schema_version table first (must exist before anything else)
	if _, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER NOT NULL,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		comment    TEXT
	)`); err != nil {
		return fmt.Errorf("creating schema_version table: %w", err)
	}

	currentVersion := d.currentSchemaVersion()

	// Versioned migrations: index is the version number (1-based)
	type migration struct {
		version int
		comment string
		sql     []string
	}

	migrations := []migration{
		{
			version: 1,
			comment: "initial schema: api_keys, requests, models, guardrail_events",
			sql: []string{
				`CREATE TABLE IF NOT EXISTS api_keys (
					id          TEXT PRIMARY KEY,
					name        TEXT NOT NULL,
					prefix      TEXT NOT NULL,
					hash        TEXT NOT NULL,
					scope       TEXT DEFAULT 'user',
					rate_limit  INTEGER DEFAULT 60,
					created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
					last_used   DATETIME,
					revoked     BOOLEAN DEFAULT FALSE
				)`,
				`CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(prefix)`,
				`CREATE TABLE IF NOT EXISTS requests (
					id          INTEGER PRIMARY KEY AUTOINCREMENT,
					key_id      TEXT REFERENCES api_keys(id),
					method      TEXT NOT NULL,
					path        TEXT NOT NULL,
					model       TEXT,
					tokens_in   INTEGER,
					tokens_out  INTEGER,
					latency_ms  INTEGER,
					status_code INTEGER,
					created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
				)`,
				`CREATE INDEX IF NOT EXISTS idx_requests_created ON requests(created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_requests_key ON requests(key_id)`,
				`CREATE TABLE IF NOT EXISTS models (
					name         TEXT PRIMARY KEY,
					size_bytes   INTEGER,
					format       TEXT,
					family       TEXT,
					params       TEXT,
					quantization TEXT,
					pulled_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
					last_used    DATETIME
				)`,
				`CREATE TABLE IF NOT EXISTS guardrail_events (
					id          INTEGER PRIMARY KEY AUTOINCREMENT,
					request_id  TEXT NOT NULL,
					key_id      TEXT,
					model       TEXT,
					stage       TEXT NOT NULL,
					action      TEXT NOT NULL,
					reason      TEXT,
					score       REAL,
					created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
				)`,
				`CREATE INDEX IF NOT EXISTS idx_guardrail_events_request ON guardrail_events(request_id)`,
				`CREATE INDEX IF NOT EXISTS idx_guardrail_events_action ON guardrail_events(action)`,
			},
		},
		{
			version: 2,
			comment: "per-key tunnel access control",
			sql: []string{
				`ALTER TABLE api_keys ADD COLUMN tunnel_access BOOLEAN DEFAULT TRUE`,
			},
		},
		{
			version: 3,
			comment: "key expiry and model restrictions",
			sql: []string{
				`ALTER TABLE api_keys ADD COLUMN expires_at DATETIME`,
				`ALTER TABLE api_keys ADD COLUMN allowed_models TEXT`,
			},
		},
		{
			version: 4,
			comment: "external API providers + request provider tracking",
			sql: []string{
				`CREATE TABLE IF NOT EXISTS providers (
					id         TEXT PRIMARY KEY,
					name       TEXT NOT NULL UNIQUE,
					base_url   TEXT NOT NULL,
					api_key    TEXT NOT NULL,
					enabled    BOOLEAN DEFAULT TRUE,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				)`,
				`ALTER TABLE requests ADD COLUMN provider TEXT`,
			},
		},
		{
			version: 5,
			comment: "sandbox management with tiered security",
			sql: []string{
				`CREATE TABLE IF NOT EXISTS sandboxes (
					id           TEXT PRIMARY KEY,
					name         TEXT NOT NULL UNIQUE,
					container_id TEXT,
					status       TEXT NOT NULL DEFAULT 'created',
					policy       TEXT NOT NULL DEFAULT 'api-only',
					api_key_id   TEXT,
					config       TEXT,
					created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
					started_at   DATETIME,
					stopped_at   DATETIME
				)`,
				`CREATE INDEX IF NOT EXISTS idx_sandboxes_name ON sandboxes(name)`,
				`ALTER TABLE sandboxes ADD COLUMN tier INTEGER DEFAULT 2`,
			},
		},
	}

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}
		for _, stmt := range m.sql {
			if _, err := d.db.Exec(stmt); err != nil {
				if isAlterTableDuplicate(stmt, err) {
					continue
				}
				return fmt.Errorf("migration v%d failed: %w", m.version, err)
			}
		}
		if _, err := d.db.Exec(
			`INSERT INTO schema_version (version, comment) VALUES (?, ?)`,
			m.version, m.comment,
		); err != nil {
			return fmt.Errorf("recording migration v%d: %w", m.version, err)
		}
	}

	return nil
}

// currentSchemaVersion returns the highest applied schema version, or 0 if none.
func (d *DB) currentSchemaVersion() int {
	var version int
	err := d.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version)
	if err != nil {
		return 0
	}
	return version
}

// GetSchemaVersion returns the current schema version and migration history.
func (d *DB) GetSchemaVersion() (int, error) {
	return d.currentSchemaVersion(), nil
}

// isAlterTableDuplicate returns true if the error is from adding a column that already exists.
func isAlterTableDuplicate(sql string, err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return (len(s) > 0 && (containsStr(s, "duplicate column") || containsStr(s, "already exists")))
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && strings.Contains(s, substr)
}
