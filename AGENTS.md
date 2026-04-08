# Solon Inference — Agent Onboarding

## What You're Building

**Solon Inference** is the managed GPU hosting product. Customers rent GPU servers with Solon pre-installed. Run open-source models (Llama, Mistral, Qwen, Phi) on dedicated hardware. Self-hosted inference without the DevOps.

**Tagline:** "Your models. Your hardware. Zero setup."

**Revenue model:** $299+/mo for GPU servers. Hourly billing for high-end GPUs (A100, H100). Customer owns the server, we manage the stack. Thin margins on hardware (8-21%), value is in the software layer.

## Current State

Solon already supports local model inference (llama.cpp backend). The managed hosting pipeline exists (Stripe → Provisioner → Hetzner). What's missing: GPU-specific tiers, vLLM integration, model pre-loading, and performance monitoring.

### What Works
- Model catalog with 10+ models (`internal/models/catalog.json`)
- Model pulling from HuggingFace (GGUF format)
- llama.cpp inference (native CGO, Metal on macOS, CUDA on Linux)
- Ollama backend as fallback
- Proxy backend for cloud APIs (Anthropic, OpenAI)
- OpenAI-compatible API (`/v1/chat/completions`, `/v1/embeddings`, `/v1/models`)
- Streaming SSE responses
- Provisioner: Stripe → Hetzner server creation → cloud-init → Solon install
- CI/CD: cross-compile Linux/macOS, GitHub releases

### What Needs Work

#### P0 — GPU Tier Support

1. **GPU server provisioning**
   - Provisioner currently maps: starter→cx22, pro→cx42, gpu→gx11
   - Need: expand GPU tiers to match Hetzner's actual GPU offerings
   - Hetzner GPUs: GEX44 (RTX 4000 SFF, 20GB), GEX66 (RTX 5000, 32GB), GEX88 (2x RTX 5000, 64GB)
   - For larger GPUs (A100, H100): need DataCrunch or Lambda Labs API integration
   - Files: `provisioner/src/types.ts`, `provisioner/src/cloud-init.ts`

2. **NVIDIA driver installation via cloud-init**
   - Current cloud-init script installs Docker + Solon but no GPU drivers
   - Need: NVIDIA Container Toolkit, CUDA drivers
   - Ansible role exists (`infra/ansible/roles/gpu-inference/`) — port to cloud-init
   - File: `provisioner/src/cloud-init.ts`

3. **Model pre-loading**
   - When customer selects a GPU tier, pre-load the right model
   - E.g., GPU L4 (24GB) → auto-pull Llama 3.1 70B Q4
   - Need: tier-to-model mapping, pull on first boot
   - Files: `provisioner/src/cloud-init.ts`, `internal/models/catalog.json`

#### P1 — vLLM Backend

4. **vLLM integration**
   - llama.cpp is great for CPU/single-GPU but vLLM is faster for multi-GPU
   - Add vLLM as a backend: `internal/inference/backends/vllm.go`
   - vLLM runs as a separate process, Solon proxies to it
   - OpenAI-compatible API from vLLM means minimal gateway changes
   - Implement `Backend` interface: `Available()`, `Load()`, `ChatCompletion()`, etc.

5. **Backend auto-selection**
   - If GPU detected → use vLLM (or llama.cpp with CUDA)
   - If Apple Silicon → use MLX (stub exists at `backends/mlx.go`)
   - If CPU only → use llama.cpp CPU or Ollama
   - File: `internal/inference/engine.go` (backend selection logic)

#### P2 — Performance & Monitoring

6. **GPU monitoring dashboard**
   - Show GPU utilization, VRAM usage, temperature
   - Need: nvidia-smi data collection, new API endpoint, dashboard widget
   - Files: new `internal/inference/gpu.go`, new dashboard component

7. **Model benchmarking**
   - "This model does X tokens/sec on your hardware"
   - Run standard prompts, measure throughput
   - Display in Models page

8. **Cost calculator**
   - "Running Llama 70B on GEX44 costs $X/month, processes ~Y tokens/day"
   - Help customers pick the right tier
   - Add to website pricing page and dashboard

#### P3 — Advanced Features

9. **Multi-model serving**
   - Run multiple models simultaneously (small for classification, large for generation)
   - Memory budget already exists in engine.go (80% of system RAM)
   - Need: extend to VRAM budgeting

10. **Model fine-tuning**
    - Upload custom LoRA adapters
    - Apply during inference
    - Dashboard UI for adapter management

11. **Batch inference API**
    - `/v1/batch` endpoint for bulk processing
    - Queue-based, returns results async
    - Useful for document processing, data labeling

## Architecture

```
Solon Binary
├── internal/inference/engine.go     — Backend orchestration, model loading, caching
│   └── backends/
│       ├── llamacpp.go              — Native llama.cpp (CGO, CUDA/Metal)
│       ├── llamacpp_nocgo.go        — Stub when CGO disabled
│       ├── ollama.go                — Ollama HTTP client (fallback)
│       ├── proxy.go                 — Cloud API proxy (Anthropic/OpenAI)
│       ├── mlx.go                   — Apple Silicon (STUB)
│       └── vllm.go                  — vLLM (TO BUILD)
├── internal/models/
│   ├── registry.go                  — Model registry, pull logic
│   ├── catalog.json                 — Model definitions (name, sizes, URLs)
│   └── download.go                  — HuggingFace + R2 download
├── internal/gateway/
│   ├── inference_handlers.go        — /v1/chat/completions, /v1/embeddings
│   └── model_handlers.go           — /api/v1/models (pull, list, delete)
```

### Backend Interface (`backends/backend.go`)
Any new backend must implement:
```go
type Backend interface {
    Name() string
    Available() bool
    Load(model string, opts LoadOptions) error
    Unload(model string) error
    ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
    ChatCompletionStream(ctx context.Context, req CompletionRequest) (<-chan CompletionChunk, error)
    Embeddings(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)
    LoadedModels() []string
}
```

### Provisioner Pipeline
```
Stripe Checkout (cloud API)
  → POST /webhook/provision (provisioner worker)
    → Hetzner Cloud API: create server with cloud-init
      → cloud-init: install Docker, Solon, NVIDIA drivers
        → Solon starts, generates API key
          → Callback to cloud API with IP + key
            → Dashboard shows "Running" with connect button
```

### GPU Server Tiers (Hetzner)

| Tier | Server | GPU | VRAM | Models That Fit | Price |
|------|--------|-----|------|----------------|-------|
| GPU Starter | GEX44 | RTX 4000 SFF | 20 GB | 7-14B params | ~$275/mo cost |
| GPU Pro | GEX66 | RTX 5000 Ada | 32 GB | Up to 70B Q4 | ~$440/mo cost |
| GPU Max | GEX88 | 2x RTX 5000 | 64 GB | 70B FP16 | ~$825/mo cost |

For larger models (Kimi K2.5, Llama 405B): need 4x A100 — beyond Hetzner, requires DataCrunch/Lambda.

## Key Files for GPU Work

```
provisioner/src/cloud-init.ts          — Server bootstrap script (add NVIDIA drivers)
provisioner/src/types.ts               — Tier-to-server mapping (add GPU tiers)
provisioner/src/index.ts               — Provisioner webhook handler
internal/inference/engine.go           — Backend selection, memory budgeting
internal/inference/backends/            — Add vllm.go here
internal/models/catalog.json           — Model definitions (add GPU-optimized entries)
infra/ansible/roles/gpu-inference/     — Existing Ansible role for GPU setup (reference)
website/src/pages/pricing.astro        — GPU pricing display
dashboard/src/pages/cloud/Billing.tsx  — GPU tier checkout UI
cloud/src/lib/stripe.ts                — Stripe price IDs for GPU tiers
```

## Build & Test
```bash
# Build with CGO (required for llama.cpp)
CGO_ENABLED=1 go build -o solon ./cmd/solon/

# Test inference
make test

# Test with GPU (requires NVIDIA GPU + drivers)
solon serve --preload llama3.2:8b
curl http://localhost:8420/v1/chat/completions \
  -H "Authorization: Bearer sol_sk_live_xxx" \
  -d '{"model":"llama3.2:8b","messages":[{"role":"user","content":"hi"}]}'
```

## Git Workflow
- Branch: `product/solon-inference`
- PR to master when ready
- Conventional commits (feat:, fix:, etc.)
- Never push directly to master
