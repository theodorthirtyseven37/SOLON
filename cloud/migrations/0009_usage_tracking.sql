-- Usage tracking for metered billing (API requests, security scans, tunnel bandwidth)
CREATE TABLE IF NOT EXISTS usage_records (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  dimension TEXT NOT NULL,            -- api_requests, security_scans, tunnel_bandwidth
  quantity INTEGER NOT NULL DEFAULT 0,
  period_start TEXT NOT NULL,         -- ISO date of billing period start
  period_end TEXT NOT NULL,           -- ISO date of billing period end
  reported_to_stripe INTEGER NOT NULL DEFAULT 0, -- boolean: already sent to Stripe
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_usage_user_dimension ON usage_records(user_id, dimension, period_start);
CREATE INDEX IF NOT EXISTS idx_usage_unreported ON usage_records(reported_to_stripe, period_end);

-- Add Stripe customer ID and subscription fields to users table
ALTER TABLE subscriptions ADD COLUMN cancel_at_period_end INTEGER NOT NULL DEFAULT 0;

-- Daily usage summary for quick lookups
CREATE TABLE IF NOT EXISTS usage_daily (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  dimension TEXT NOT NULL,
  day TEXT NOT NULL,                   -- ISO date (YYYY-MM-DD)
  quantity INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, dimension, day)
);
