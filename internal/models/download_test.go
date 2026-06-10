package models

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectGGUFByQuant(t *testing.T) {
	// The full quant set published by Qwen/Qwen3-Embedding-8B-GGUF, which
	// previously caused every file to download because the library's filter
	// was skipped. Each quant must resolve to exactly its own file.
	qwen := []string{
		".gitattributes",
		"README.md",
		"Qwen3-Embedding-8B-Q4_K_M.gguf",
		"Qwen3-Embedding-8B-Q5_0.gguf",
		"Qwen3-Embedding-8B-Q5_K_M.gguf",
		"Qwen3-Embedding-8B-Q6_K.gguf",
		"Qwen3-Embedding-8B-Q8_0.gguf",
		"Qwen3-Embedding-8B-f16.gguf",
	}

	tests := []struct {
		name  string
		paths []string
		quant string
		want  string
		ok    bool
	}{
		{name: "Q8_0 picks only Q8_0", paths: qwen, quant: "Q8_0", want: "Qwen3-Embedding-8B-Q8_0.gguf", ok: true},
		{name: "Q5_0 not confused with Q5_K_M", paths: qwen, quant: "Q5_0", want: "Qwen3-Embedding-8B-Q5_0.gguf", ok: true},
		{name: "Q5_K_M exact", paths: qwen, quant: "Q5_K_M", want: "Qwen3-Embedding-8B-Q5_K_M.gguf", ok: true},
		{name: "case-insensitive", paths: qwen, quant: "q6_k", want: "Qwen3-Embedding-8B-Q6_K.gguf", ok: true},
		{name: "f16 lowercase suffix", paths: qwen, quant: "f16", want: "Qwen3-Embedding-8B-f16.gguf", ok: true},
		{name: "resolves in subdir", paths: []string{"gguf/model-Q8_0.gguf"}, quant: "Q8_0", want: "gguf/model-Q8_0.gguf", ok: true},
		{name: "no match returns false", paths: qwen, quant: "Q3_K_S", want: "", ok: false},
		{name: "non-gguf ignored", paths: []string{"config-Q8_0.json"}, quant: "Q8_0", want: "", ok: false},
		{name: "ambiguous split falls back", paths: []string{"m-Q8_0-00001-of-00002.gguf", "m-Q8_0-00002-of-00002.gguf"}, quant: "Q8_0", want: "", ok: false},
		{name: "empty list", paths: nil, quant: "Q8_0", want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := selectGGUFByQuant(tt.paths, tt.quant)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveGGUFURL(t *testing.T) {
	const repo = "Qwen/Qwen3-Embedding-8B-GGUF"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/models/"+repo+"/tree/main", r.URL.Path)
		_, _ = fmt.Fprint(w, `[
			{"type":"file","path":".gitattributes"},
			{"type":"directory","path":"sub"},
			{"type":"file","path":"Qwen3-Embedding-8B-Q4_K_M.gguf"},
			{"type":"file","path":"Qwen3-Embedding-8B-Q8_0.gguf"}
		]`)
	}))
	defer srv.Close()

	orig := hfBaseURL
	hfBaseURL = srv.URL
	t.Cleanup(func() { hfBaseURL = orig })

	url, err := resolveGGUFURL(context.Background(), repo, "Q8_0")
	require.NoError(t, err)
	assert.Equal(t, srv.URL+"/"+repo+"/resolve/main/Qwen3-Embedding-8B-Q8_0.gguf", url)

	// Ambiguous quant present in two distinct files -> error, caller falls back.
	_, err = resolveGGUFURL(context.Background(), repo, "Q")
	assert.Error(t, err)
}
