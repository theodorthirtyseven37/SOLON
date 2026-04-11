package storage

import (
	"fmt"
	"time"
)

// RequestLog represents a single API request log entry.
type RequestLog struct {
	ID         int64     `json:"id"`
	KeyID      string    `json:"key_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Model      string    `json:"model,omitempty"`
	TokensIn   int       `json:"tokens_in"`
	TokensOut  int       `json:"tokens_out"`
	LatencyMS  int       `json:"latency_ms"`
	StatusCode int       `json:"status_code"`
	Provider   string    `json:"provider,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// UsageStats represents aggregated usage statistics.
type UsageStats struct {
	TotalRequests    int64   `json:"total_requests"`
	TotalTokensIn    int64   `json:"total_tokens_in"`
	TotalTokensOut   int64   `json:"total_tokens_out"`
	AvgLatencyMS     float64 `json:"avg_latency_ms"`
	RequestsToday    int64   `json:"requests_today"`
	UniqueKeysUsed   int64   `json:"unique_keys_used"`
	MostUsedModel    string  `json:"most_used_model,omitempty"`
}

// LogRequest records an API request in the analytics database.
func (d *DB) LogRequest(keyID, method, path, model string, tokensIn, tokensOut, latencyMS, statusCode int, provider string) error {
	_, err := d.db.Exec(
		`INSERT INTO requests (key_id, method, path, model, tokens_in, tokens_out, latency_ms, status_code, provider) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		keyID, method, path, model, tokensIn, tokensOut, latencyMS, statusCode, provider,
	)
	if err != nil {
		return fmt.Errorf("logging request: %w", err)
	}
	return nil
}

// GetRequestLog returns the most recent API requests.
func (d *DB) GetRequestLog(limit int) ([]RequestLog, error) {
	rows, err := d.db.Query(
		`SELECT id, key_id, method, path, model, tokens_in, tokens_out, latency_ms, status_code, created_at, provider
		 FROM requests ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying request log: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []RequestLog
	for rows.Next() {
		var log RequestLog
		var model, provider *string
		if err := rows.Scan(&log.ID, &log.KeyID, &log.Method, &log.Path, &model, &log.TokensIn, &log.TokensOut, &log.LatencyMS, &log.StatusCode, &log.CreatedAt, &provider); err != nil {
			return nil, fmt.Errorf("scanning request log: %w", err)
		}
		if model != nil {
			log.Model = *model
		}
		if provider != nil {
			log.Provider = *provider
		}
		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating request log: %w", err)
	}

	return logs, nil
}

// GetUsageStats returns aggregated usage statistics.
func (d *DB) GetUsageStats() (*UsageStats, error) {
	stats := &UsageStats{}

	// Total requests and tokens
	err := d.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(AVG(latency_ms), 0) FROM requests`,
	).Scan(&stats.TotalRequests, &stats.TotalTokensIn, &stats.TotalTokensOut, &stats.AvgLatencyMS)
	if err != nil {
		return nil, fmt.Errorf("querying total stats: %w", err)
	}

	// Requests today
	_ = d.db.QueryRow(
		`SELECT COUNT(*) FROM requests WHERE created_at >= date('now')`,
	).Scan(&stats.RequestsToday)

	// Unique keys used
	_ = d.db.QueryRow(
		`SELECT COUNT(DISTINCT key_id) FROM requests`,
	).Scan(&stats.UniqueKeysUsed)

	// Most used model
	_ = d.db.QueryRow(
		`SELECT model FROM requests WHERE model IS NOT NULL AND model != '' GROUP BY model ORDER BY COUNT(*) DESC LIMIT 1`,
	).Scan(&stats.MostUsedModel)

	return stats, nil
}
