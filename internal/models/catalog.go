package models

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

//go:embed catalog.json
var embeddedCatalog []byte

// CatalogModel represents a model in the catalog.
type CatalogModel struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Creator      string             `json:"creator"`
	Sizes        []string           `json:"sizes"`
	Category     string             `json:"category"`
	Capabilities []string           `json:"capabilities"`
	Context      int                `json:"context"`
	VRAM         map[string]float64 `json:"vram"`
	Sources      map[string]ModelSource `json:"sources"`
}

var (
	catalogOnce sync.Once
	catalog     []CatalogModel
)

// GetCatalog returns the model catalog, loading from embedded JSON on first call.
func GetCatalog() []CatalogModel {
	catalogOnce.Do(func() {
		if err := json.Unmarshal(embeddedCatalog, &catalog); err != nil {
			slog.Warn("failed to parse embedded catalog", "error", err)
			catalog = []CatalogModel{}
		}
	})
	return catalog
}

// DefaultModelsFromCatalog builds the DefaultModels map from the catalog.
// Uses the first (smallest) size for each model as the default mapping.
func DefaultModelsFromCatalog() map[string]ModelSource {
	models := make(map[string]ModelSource)
	for _, m := range GetCatalog() {
		for _, size := range m.Sizes {
			key := fmt.Sprintf("%s:%s", m.Name, size)
			if source, ok := m.Sources[size]; ok {
				models[key] = source
			}
		}
		// For embedding models, also add without size suffix
		if m.Category == "embedding" && len(m.Sizes) == 1 {
			if source, ok := m.Sources[m.Sizes[0]]; ok {
				models[m.Name] = source
			}
		}
	}
	return models
}

// RefreshCatalogFromRemote optionally fetches an updated catalog from a remote URL.
// Falls back to embedded catalog on any error.
func RefreshCatalogFromRemote(url string) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		slog.Warn("catalog refresh skipped", "reason", "network error", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("catalog refresh skipped", "reason", "bad status", "status", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		slog.Warn("catalog refresh skipped", "reason", "read error", "error", err)
		return
	}

	var remote []CatalogModel
	if err := json.Unmarshal(body, &remote); err != nil {
		slog.Warn("catalog refresh skipped", "reason", "parse error", "error", err)
		return
	}

	if len(remote) > 0 {
		catalog = remote
		slog.Info("catalog refreshed", "model_count", len(remote))
	}
}
