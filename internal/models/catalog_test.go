package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCatalog(t *testing.T) {
	catalog := GetCatalog()
	require.NotEmpty(t, catalog, "catalog should not be empty")

	// Verify basic structure of each model
	for _, m := range catalog {
		assert.NotEmpty(t, m.Name, "model name should not be empty")
		assert.NotEmpty(t, m.Description, "description should not be empty for %s", m.Name)
		assert.NotEmpty(t, m.Creator, "creator should not be empty for %s", m.Name)
		assert.NotEmpty(t, m.Sizes, "sizes should not be empty for %s", m.Name)
		assert.NotEmpty(t, m.Category, "category should not be empty for %s", m.Name)
		assert.NotEmpty(t, m.Sources, "sources should not be empty for %s", m.Name)
		assert.Greater(t, m.Context, 0, "context should be positive for %s", m.Name)

		// Every listed size should have a corresponding source
		for _, size := range m.Sizes {
			source, ok := m.Sources[size]
			assert.True(t, ok, "model %s should have source for size %s", m.Name, size)
			if ok {
				assert.NotEmpty(t, source.Repo, "repo should not be empty for %s:%s", m.Name, size)
				assert.NotEmpty(t, source.File, "file filter should not be empty for %s:%s", m.Name, size)
				assert.NotEmpty(t, source.R2URL, "R2 URL should not be empty for %s:%s", m.Name, size)
			}
		}

		// Every listed size should have VRAM info
		for _, size := range m.Sizes {
			vram, ok := m.VRAM[size]
			assert.True(t, ok, "model %s should have VRAM info for size %s", m.Name, size)
			if ok {
				assert.Greater(t, vram, 0.0, "VRAM should be positive for %s:%s", m.Name, size)
			}
		}
	}
}

func TestGetCatalogKnownModels(t *testing.T) {
	catalog := GetCatalog()

	// Verify some expected models exist
	names := make(map[string]bool)
	for _, m := range catalog {
		names[m.Name] = true
	}

	expectedModels := []string{"llama3.2", "gemma3", "qwen2.5", "mistral", "deepseek-r1"}
	for _, name := range expectedModels {
		assert.True(t, names[name], "catalog should contain %s", name)
	}
}

func TestGetCatalogCategories(t *testing.T) {
	catalog := GetCatalog()

	validCategories := map[string]bool{"chat": true, "code": true, "embedding": true}
	for _, m := range catalog {
		assert.True(t, validCategories[m.Category], "model %s has invalid category %q", m.Name, m.Category)
	}
}

func TestLookupCatalogModel(t *testing.T) {
	// Lookup with size
	m, size := LookupCatalogModel("llama3.2:3b")
	require.NotNil(t, m)
	assert.Equal(t, "llama3.2", m.Name)
	assert.Equal(t, "3b", size)

	// Lookup without size defaults to first size
	m, size = LookupCatalogModel("qwen2.5")
	require.NotNil(t, m)
	assert.Equal(t, "qwen2.5", m.Name)
	assert.NotEmpty(t, size)

	// Unknown model
	m, _ = LookupCatalogModel("nonexistent:7b")
	assert.Nil(t, m)
}

func TestRefreshCatalogFromRemote(t *testing.T) {
	t.Run("valid remote catalog replaces embedded", func(t *testing.T) {
		remoteCatalog := []CatalogModel{
			{
				Name:        "test-model",
				Description: "A test model",
				Creator:     "Test",
				Sizes:       []string{"7b"},
				Category:    "chat",
				Context:     4096,
				VRAM:        map[string]float64{"7b": 4.0},
				Sources: map[string]ModelSource{
					"7b": {Repo: "test/model", File: "Q4_K_M", R2URL: "https://example.com/test.gguf"},
				},
			},
		}
		data, err := json.Marshal(remoteCatalog)
		require.NoError(t, err)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		}))
		defer srv.Close()

		RefreshCatalogFromRemote(srv.URL)

		// Catalog should now contain our test model
		found := false
		for _, m := range catalog {
			if m.Name == "test-model" {
				found = true
				break
			}
		}
		assert.True(t, found, "remote catalog should have been applied")

		// Restore embedded catalog for other tests
		catalogOnce.Do(func() {}) // no-op, already done
		_ = json.Unmarshal(embeddedCatalog, &catalog)
	})

	t.Run("invalid JSON falls back gracefully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("#!/bin/sh\n# this is not JSON"))
		}))
		defer srv.Close()

		before := len(catalog)
		RefreshCatalogFromRemote(srv.URL)
		assert.Equal(t, before, len(catalog), "catalog should not change on invalid JSON")
	})

	t.Run("HTTP error falls back gracefully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		before := len(catalog)
		RefreshCatalogFromRemote(srv.URL)
		assert.Equal(t, before, len(catalog), "catalog should not change on HTTP error")
	})

	t.Run("unreachable server falls back gracefully", func(t *testing.T) {
		before := len(catalog)
		RefreshCatalogFromRemote("http://127.0.0.1:1") // port 1 — won't connect
		assert.Equal(t, before, len(catalog), "catalog should not change on network error")
	})
}

func TestDefaultModelsFromCatalog(t *testing.T) {
	models := DefaultModelsFromCatalog()
	require.NotEmpty(t, models, "default models should not be empty")

	// Check that model:size keys are generated
	assert.Contains(t, models, "llama3.2:3b")
	assert.Contains(t, models, "qwen2.5:7b")
	assert.Contains(t, models, "deepseek-r1:14b")

	// Embedding models should be accessible without size suffix
	assert.Contains(t, models, "nomic-embed-text")
	assert.Contains(t, models, "mxbai-embed-large")

	// Verify a specific source
	llama := models["llama3.2:3b"]
	assert.NotEmpty(t, llama.Repo)
	assert.NotEmpty(t, llama.File)
	assert.NotEmpty(t, llama.R2URL)
}
