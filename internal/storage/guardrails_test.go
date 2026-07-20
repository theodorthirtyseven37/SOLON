package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogGuardrailEvent(t *testing.T) {
	db := testDB(t)

	err := db.LogGuardrailEvent("req-1", "key-1", "llama3.2:8b", "gate", "pass", "", 0.0)
	require.NoError(t, err)

	events, err := db.GetGuardrailEvents(10)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Equal(t, "req-1", events[0].RequestID)
	assert.Equal(t, "key-1", events[0].KeyID)
	assert.Equal(t, "llama3.2:8b", events[0].Model)
	assert.Equal(t, "gate", events[0].Stage)
	assert.Equal(t, "pass", events[0].Action)
}

func TestGetGuardrailEventsLimit(t *testing.T) {
	db := testDB(t)

	for i := 0; i < 5; i++ {
		err := db.LogGuardrailEvent("req-x", "", "", "gate", "pass", "", 0.0)
		require.NoError(t, err)
	}

	events, err := db.GetGuardrailEvents(3)
	require.NoError(t, err)
	assert.Len(t, events, 3)

	events, err = db.GetGuardrailEvents(100)
	require.NoError(t, err)
	assert.Len(t, events, 5)
}

func TestGetGuardrailEventsEmpty(t *testing.T) {
	db := testDB(t)

	events, err := db.GetGuardrailEvents(10)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestGetGuardrailStats(t *testing.T) {
	db := testDB(t)

	// Empty stats
	stats, err := db.GetGuardrailStats()
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats["total_events"])

	// Log events with different actions
	_ = db.LogGuardrailEvent("req-1", "", "", "gate", "pass", "", 0.0)
	_ = db.LogGuardrailEvent("req-2", "", "", "shield", "block", "toxic content", 0.95)
	_ = db.LogGuardrailEvent("req-3", "", "", "policy", "flag", "sensitive topic", 0.6)
	_ = db.LogGuardrailEvent("req-4", "", "", "gate", "pass", "", 0.0)

	stats, err = db.GetGuardrailStats()
	require.NoError(t, err)
	assert.Equal(t, int64(4), stats["total_events"])
	assert.Equal(t, int64(1), stats["blocked"])
	assert.Equal(t, int64(1), stats["flagged"])
}
