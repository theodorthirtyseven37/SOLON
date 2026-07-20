package storage

import (
	"fmt"
	"time"
)

// GuardrailEvent represents a single guardrail decision.
type GuardrailEvent struct {
	ID        int64     `json:"id"`
	RequestID string    `json:"request_id"`
	KeyID     string    `json:"key_id,omitempty"`
	Model     string    `json:"model,omitempty"`
	Stage     string    `json:"stage"`  // "gate", "shield", "policy"
	Action    string    `json:"action"` // "pass", "block", "flag"
	Reason    string    `json:"reason,omitempty"`
	Score     float64   `json:"score,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// LogGuardrailEvent records a guardrail decision.
func (d *DB) LogGuardrailEvent(requestID, keyID, model, stage, action, reason string, score float64) error {
	_, err := d.db.Exec(
		`INSERT INTO guardrail_events (request_id, key_id, model, stage, action, reason, score) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		requestID, keyID, model, stage, action, reason, score,
	)
	if err != nil {
		return fmt.Errorf("logging guardrail event: %w", err)
	}
	return nil
}

// GetGuardrailEvents returns recent guardrail events.
func (d *DB) GetGuardrailEvents(limit int) ([]GuardrailEvent, error) {
	rows, err := d.db.Query(
		`SELECT id, request_id, key_id, model, stage, action, reason, score, created_at
		 FROM guardrail_events ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying guardrail events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []GuardrailEvent
	for rows.Next() {
		var e GuardrailEvent
		var keyID, model, reason *string
		if err := rows.Scan(&e.ID, &e.RequestID, &keyID, &model, &e.Stage, &e.Action, &reason, &e.Score, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning guardrail event: %w", err)
		}
		if keyID != nil {
			e.KeyID = *keyID
		}
		if model != nil {
			e.Model = *model
		}
		if reason != nil {
			e.Reason = *reason
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating guardrail events: %w", err)
	}

	return events, nil
}

// GetGuardrailStats returns aggregate guardrail statistics.
func (d *DB) GetGuardrailStats() (map[string]any, error) {
	stats := map[string]any{}

	var total, blocked, flagged int64
	err := d.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN action='block' THEN 1 ELSE 0 END), 0), COALESCE(SUM(CASE WHEN action='flag' THEN 1 ELSE 0 END), 0) FROM guardrail_events`,
	).Scan(&total, &blocked, &flagged)
	if err != nil {
		return nil, fmt.Errorf("querying guardrail stats: %w", err)
	}

	stats["total_events"] = total
	stats["blocked"] = blocked
	stats["flagged"] = flagged

	return stats, nil
}
