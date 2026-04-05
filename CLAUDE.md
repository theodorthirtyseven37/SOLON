# Solon — AI Agent Context

## Project Overview

Solon is a self-hosted AI runtime with a secure web API. Single Go binary that runs AI models locally and exposes them securely via authenticated API with tunnel support.

## Quick Reference

- **Language**: Go 1.22+
- **Module**: `github.com/openclaw/solon`
- **Binary**: `solon`
- **Default port**: 8420
- **Data dir**: `~/.solon/`
- **Database**: SQLite (WAL mode) at `~/.solon/solon.db`

## Repository Structure

```
cmd/solon/main.go           # CLI entrypoint (Cobra)
internal/gateway/            # HTTP API gateway (auth, rate limiting, routing)
internal/inference/          # Inference engine (model management, backends)
internal/inference/backends/ # Backend implementations (llama.cpp, MLX)
internal/tunnel/             # Tunnel management (Cloudflare, Relay)
internal/storage/            # SQLite storage (keys, analytics, models)
internal/dashboard/          # Embedded web dashboard (go:embed)
internal/openclaw/           # OpenClaw provider integration
dashboard/                   # React SPA source (built → dashboard/dist/)
docs/                        # Documentation
```

## Key Patterns

### Error Handling
- Return errors, don't panic
- Wrap errors with context: `fmt.Errorf("loading model %s: %w", name, err)`
- Log at the top level, not in library code

### Naming Conventions
- API key prefix: `sol_sk_live_` or `sol_sk_test_`
- CLI commands: `solon <noun> <verb>` (e.g., `solon models pull`)
- HTTP routes: `/v1/` for OpenAI-compatible, `/api/v1/` for management

### Testing
- Table-driven tests
- Test files next to source: `auth_test.go` alongside `auth.go`
- Use `testify/assert` for assertions
- Mock interfaces, not implementations

### Database
- All migrations in `internal/storage/db.go`
- Use prepared statements
- WAL mode enabled on init

## Build Commands

```bash
make build              # Build binary (includes dashboard)
make build-dashboard    # Build React dashboard only
make test               # Run all tests
make lint               # Run golangci-lint
make dev                # Build + run in dev mode
```

## Common Tasks

### Adding a new CLI command
1. Add command in `cmd/solon/main.go` using Cobra
2. Wire to appropriate internal package

### Adding a new API endpoint
1. Add route in `internal/gateway/gateway.go`
2. Add handler function
3. Document in `docs/api.md`

### Adding a new inference backend
1. Implement `Backend` interface in `internal/inference/backends/`
2. Register in `internal/inference/engine.go`

### Modifying the database schema
1. Add migration in `internal/storage/db.go`
2. Increment schema version

## Git Workflow (Mandatory)

- **Never push directly to master.** All changes go through feature branches and pull requests.
- Branch naming: `feat/`, `fix/`, `chore/`, `docs/`, `refactor/`, `test/` prefixes matching conventional commits.
- PRs must have a clear summary and test plan before merge.
- Master auto-deploys — treat it as production.

## Architecture Rules

1. **Gateway is the only TCP listener** — inference engine uses Unix socket only
2. **Auth is mandatory** — never add a way to disable authentication
3. **Single binary** — all assets embedded via go:embed
4. **OpenAI-compatible** — inference API must match OpenAI's schema exactly
5. **SQLite only** — no external database dependencies

## Dependencies (key packages)

- `github.com/spf13/cobra` — CLI framework
- `github.com/go-chi/chi/v5` — HTTP router
- `github.com/mattn/go-sqlite3` — SQLite driver
- `golang.org/x/crypto/bcrypt` — API key hashing
- `github.com/google/uuid` — Request IDs

## Related Projects

- **OpenClaw**: Agent orchestration framework (Solon is the inference backend)
- **Ollama**: Forked for inference engine and model management
