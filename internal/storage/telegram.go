package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TelegramIntegration represents a Telegram bot linked to a sandbox.
type TelegramIntegration struct {
	ID          string    `json:"id"`
	SandboxID   string    `json:"sandbox_id"`
	BotUsername string    `json:"bot_username,omitempty"`
	Status      string    `json:"status"`
	ErrorMsg    string    `json:"error_msg,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateTelegramIntegration stores an encrypted bot token for a sandbox.
func (d *DB) CreateTelegramIntegration(sandboxID, botToken string) (*TelegramIntegration, error) {
	encrypted, err := encryptValue(botToken)
	if err != nil {
		return nil, fmt.Errorf("encrypting bot token: %w", err)
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	_, err = d.db.Exec(`INSERT INTO telegram_integrations (id, sandbox_id, bot_token, status, created_at, updated_at)
		VALUES (?, ?, ?, 'disconnected', ?, ?)`, id, sandboxID, encrypted, now, now)
	if err != nil {
		return nil, fmt.Errorf("creating telegram integration: %w", err)
	}

	return &TelegramIntegration{
		ID:        id,
		SandboxID: sandboxID,
		Status:    "disconnected",
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetTelegramIntegration returns the Telegram integration for a sandbox (without token).
func (d *DB) GetTelegramIntegration(sandboxID string) (*TelegramIntegration, error) {
	var ti TelegramIntegration
	var errorMsg sql.NullString
	var botUsername sql.NullString

	err := d.db.QueryRow(`SELECT id, sandbox_id, bot_username, status, error_msg, created_at, updated_at
		FROM telegram_integrations WHERE sandbox_id = ?`, sandboxID).
		Scan(&ti.ID, &ti.SandboxID, &botUsername, &ti.Status, &errorMsg, &ti.CreatedAt, &ti.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting telegram integration: %w", err)
	}

	if errorMsg.Valid {
		ti.ErrorMsg = errorMsg.String
	}
	if botUsername.Valid {
		ti.BotUsername = botUsername.String
	}

	return &ti, nil
}

// GetTelegramBotToken returns the decrypted bot token for a sandbox.
func (d *DB) GetTelegramBotToken(sandboxID string) (string, error) {
	var encrypted string
	err := d.db.QueryRow(`SELECT bot_token FROM telegram_integrations WHERE sandbox_id = ?`, sandboxID).
		Scan(&encrypted)
	if err != nil {
		return "", fmt.Errorf("getting telegram bot token: %w", err)
	}

	return decryptValue(encrypted)
}

// UpdateTelegramStatus updates the status and optional error/username for an integration.
func (d *DB) UpdateTelegramStatus(sandboxID, status, errorMsg, botUsername string) error {
	_, err := d.db.Exec(`UPDATE telegram_integrations
		SET status = ?, error_msg = ?, bot_username = ?, updated_at = ?
		WHERE sandbox_id = ?`, status, errorMsg, botUsername, time.Now().UTC(), sandboxID)
	if err != nil {
		return fmt.Errorf("updating telegram status: %w", err)
	}
	return nil
}

// DeleteTelegramIntegration removes the integration for a sandbox.
func (d *DB) DeleteTelegramIntegration(sandboxID string) error {
	_, err := d.db.Exec(`DELETE FROM telegram_integrations WHERE sandbox_id = ?`, sandboxID)
	if err != nil {
		return fmt.Errorf("deleting telegram integration: %w", err)
	}
	return nil
}

// ListTelegramIntegrations returns all active telegram integrations.
func (d *DB) ListTelegramIntegrations() ([]TelegramIntegration, error) {
	rows, err := d.db.Query(`SELECT id, sandbox_id, bot_username, status, error_msg, created_at, updated_at
		FROM telegram_integrations`)
	if err != nil {
		return nil, fmt.Errorf("listing telegram integrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []TelegramIntegration
	for rows.Next() {
		var ti TelegramIntegration
		var errorMsg, botUsername sql.NullString
		if err := rows.Scan(&ti.ID, &ti.SandboxID, &botUsername, &ti.Status, &errorMsg, &ti.CreatedAt, &ti.UpdatedAt); err != nil {
			continue
		}
		if errorMsg.Valid {
			ti.ErrorMsg = errorMsg.String
		}
		if botUsername.Valid {
			ti.BotUsername = botUsername.String
		}
		result = append(result, ti)
	}

	return result, nil
}
