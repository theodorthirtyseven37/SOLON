# Solon Local — Agent Onboarding

## What You're Building

**Solon Local** is the free, open-source core product. A single Go binary that lets anyone run AI models locally with an OpenAI-compatible API. Think "Ollama but with auth and a dashboard."

**Tagline:** "Your AI. Your rules."

**Revenue model:** Free and open source. Upsell to cloud dashboard ($19/mo) for teams, remote access, and monitoring.

## Current State

The binary works. You can `brew install solon` or `curl -fsSL https://getsolon.dev | sh`, run `solon serve`, and get:
- OpenAI-compatible API on port 8420
- Web dashboard (embedded React SPA)
- API key auth (mandatory)
- Model management (pull from HuggingFace catalog)
- Cloudflare tunnel for remote access

### What Works
- `solon serve` — starts the server
- `solon models pull/list/remove/info/search` — model management
- `solon keys create/list/revoke` — API key management
- `solon tunnel setup/enable/disable` — Cloudflare tunnel
- Dashboard: Home, Models, Keys, Activity, Settings pages
- llama.cpp backend (native, CGO) + Ollama fallback
- Proxy backend for cloud providers (Anthropic, OpenAI)
- Provider management from dashboard
- CI/CD: cross-compile 4 platforms, GitHub releases, Homebrew formula

### What Needs Work

#### P0 — Fix Before Anyone Sees It

1. **Dashboard auth through reverse proxy** (Issue #20)
   - When accessed through Caddy/nginx, all API calls return 401
   - Dashboard never sends auth headers (assumes localhost)
   - Fix: detect non-localhost, show API key login, store in localStorage, send as Bearer
   - Files: `dashboard/src/api/client.ts`, `dashboard/src/api/local.ts`, new `ApiKeyLogin.tsx`

2. **R2 Model Mirror** (partially done)
   - Model downloads from HuggingFace are slow/unreliable
   - R2 bucket exists (`solon-models`), download code exists in `internal/models/registry.go`
   - Only 1 model uploaded. Need all 10 catalog models uploaded.
   - Need R2 bucket public access enabled
   - Files: `scripts/upload-model-r2.py`, `internal/models/catalog.json`

3. **Catalog parse error**
   - Solon logs: `catalog refresh skipped (parse error): invalid character '#'`
   - The embedded catalog.json likely has comments. Fix the JSON.
   - File: `internal/models/catalog.json`

#### P1 — Make It Polished

4. **Toast notifications** (Issue #23)
   - Store exists (`store/ui.ts`) but no `<ToastContainer>` rendered
   - No code calls `addToast()` anywhere
   - Add container to `App.tsx`, wire up key CRUD, model pull, provider actions
   - Files: new `components/ToastContainer.tsx`, `App.tsx`, various pages

5. **MLX Backend** (Apple Silicon)
   - Stub exists at `internal/inference/backends/mlx.go`
   - Would make Solon the best local inference tool on Mac
   - Implement via `mlx-lm` Python bridge or native bindings

6. **Dark mode completion**
   - Theme toggle exists, CSS variables defined, but dark values incomplete
   - File: `dashboard/src/index.css` or wherever CSS vars are defined

7. **Version tag**
   - Dashboard shows "vdev" — should show actual version or nothing
   - Need build-time version injection via `-ldflags`

## Architecture

```
solon binary (single Go executable)
├── cmd/solon/main.go        — CLI (Cobra)
├── internal/gateway/        — HTTP server (chi router, auth, rate limiting)
├── internal/inference/      — Model loading, backend orchestration
│   └── backends/            — llama.cpp, Ollama, Proxy, MLX (stub)
├── internal/models/         — Model registry, catalog, HuggingFace download
├── internal/storage/        — SQLite (keys, analytics, providers, sandboxes)
├── internal/tunnel/         — Cloudflare tunnel management
├── internal/dashboard/      — go:embed static files from dashboard/dist/
└── dashboard/               — React SPA (Vite + TypeScript + Tailwind)
```

## Key Patterns
- Return errors, don't panic. Wrap with `fmt.Errorf("context: %w", err)`
- Table-driven tests, `testify/assert`
- API key prefix: `sol_sk_live_` or `sol_sk_test_`
- HTTP routes: `/v1/` = OpenAI-compatible, `/api/v1/` = management
- Dashboard: Zustand stores, fetchJSON utility, CSS variables for theming

## Build & Test
```bash
make build              # Build binary (includes dashboard)
make build-dashboard    # Build React dashboard only
make test               # Run all tests
make lint               # Run golangci-lint
make dev                # Build + run in dev mode
```

## Git Workflow
- Branch: `product/solon-local`
- PR to master when ready
- Conventional commits (feat:, fix:, etc.)
- Never push directly to master
