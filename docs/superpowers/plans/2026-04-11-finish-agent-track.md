# Finish Agent Track Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the remaining agent-track issues (#28, #29, #30, #31) — agent settings page, Telegram connect, conversation sidebar, and chat header controls.

**Architecture:** All new features follow the existing pattern: thin Go HTTP handlers in `internal/gateway/` that call `ExecOpenClawCommand()` on the sandbox manager, with React pages in `dashboard/src/pages/instance/` consuming those endpoints via `fetchJSON()`. No new database tables needed — all config lives inside the OpenClaw container.

**Tech Stack:** Go (chi router), React + TypeScript, Docker exec via OpenClaw CLI

**Worktree:** `/Users/maximiliandaub/Code/solon-agents` (branch `product/solon-agents`)

---

## File Map

### Backend (Go)
- **Modify:** `internal/gateway/gateway.go` — add new route registrations
- **Modify:** `internal/gateway/sandbox_handlers.go` — add handler functions for channels, agents, skills, workspace
- **Modify:** `internal/sandbox/manager.go` — add `WriteToContainer()` helper

### Frontend (React/TypeScript)
- **Create:** `dashboard/src/pages/instance/AgentSettings.tsx` — agent settings page (model, channels, workspace, skills, access)
- **Modify:** `dashboard/src/pages/instance/Chat.tsx` — add conversation sidebar + header controls
- **Modify:** `dashboard/src/components/Sidebar.tsx` — add Agent Settings nav item
- **Modify:** `dashboard/src/App.tsx` — add `/agent-settings` route

---

## Task 1: Backend API — OpenClaw proxy endpoints

**Closes:** Foundation for #28, #29, #30, #31

**Files:**
- Modify: `internal/gateway/gateway.go:171-176` (route block)
- Modify: `internal/gateway/sandbox_handlers.go` (append handlers)
- Modify: `internal/sandbox/manager.go` (add WriteToContainer)

- [ ] **Step 1: Add WriteToContainer to sandbox manager**

Append to `internal/sandbox/manager.go` after `ExecOpenClawCommand`:

```go
// WriteToContainer writes content to a file inside the running OpenClaw container.
func (m *Manager) WriteToContainer(ctx context.Context, filePath string, content string) error {
	containers, err := m.docker.containerList(ctx, LabelManaged+"=true")
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	var containerID string
	for _, c := range containers {
		if c.Labels[LabelPolicy] == "openclaw-gateway" && c.State == "running" {
			containerID = c.ID
			break
		}
	}
	if containerID == "" {
		return fmt.Errorf("no running OpenClaw container found")
	}

	// Use sh -c with heredoc to write file content safely
	cmd := []string{"sh", "-c", fmt.Sprintf("cat > %s << 'SOLON_EOF'\n%s\nSOLON_EOF", filePath, content)}
	_, err = m.docker.containerExec(ctx, containerID, cmd, nil)
	if err != nil {
		return fmt.Errorf("writing to %s: %w", filePath, err)
	}
	return nil
}
```

- [ ] **Step 2: Add handler functions to sandbox_handlers.go**

Append to `internal/gateway/sandbox_handlers.go`:

```go
func (g *Gateway) handleOpenClawChannels(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"channels": []any{}})
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"channels", "list", "--json"})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"channels": []any{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}

func (g *Gateway) handleOpenClawAddChannel(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	var req struct {
		Channel  string `json:"channel"`
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Channel == "" {
		writeError(w, http.StatusBadRequest, "channel is required")
		return
	}

	args := []string{"channels", "add", "--channel", req.Channel}
	if req.BotToken != "" {
		args = append(args, "--bot-token", req.BotToken)
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), args)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to add channel: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": output})
}

func (g *Gateway) handleOpenClawRemoveChannel(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	name := chi.URLParam(r, "name")
	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"channels", "remove", "--channel", name})
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to remove channel: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": output})
}

func (g *Gateway) handleOpenClawAgents(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"agents": []any{}})
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"agents", "list", "--json"})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"agents": []any{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}

func (g *Gateway) handleOpenClawSkills(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}})
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"skills", "list", "--json"})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}

func (g *Gateway) handleOpenClawReadFile(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	filename := chi.URLParam(r, "filename")
	allowed := map[string]bool{"SOUL.md": true, "IDENTITY.md": true, "USER.md": true, "AGENTS.md": true}
	if !allowed[filename] {
		writeError(w, http.StatusBadRequest, "file not allowed")
		return
	}

	output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), []string{"sh", "-c", "cat /root/.openclaw/workspace/" + filename + " 2>/dev/null || echo ''"})
	if err != nil {
		// File doesn't exist yet — return empty
		writeJSON(w, http.StatusOK, map[string]string{"filename": filename, "content": ""})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"filename": filename, "content": output})
}

func (g *Gateway) handleOpenClawWriteFile(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	filename := chi.URLParam(r, "filename")
	allowed := map[string]bool{"SOUL.md": true, "IDENTITY.md": true, "USER.md": true, "AGENTS.md": true}
	if !allowed[filename] {
		writeError(w, http.StatusBadRequest, "file not allowed")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	filePath := "/root/.openclaw/workspace/" + filename
	if err := g.sandboxes.WriteToContainer(r.Context(), filePath, req.Content); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to write file: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}
```

Note: `handleOpenClawReadFile` shells out with `sh -c cat ...` rather than using `ExecOpenClawCommand` which prepends "openclaw". We need to use `ExecOpenClawCommand` with the raw file-reading approach. Actually, we need a different helper. Let's instead use docker exec directly through the existing sandbox manager pattern. Change `handleOpenClawReadFile` to use a raw exec:

```go
func (g *Gateway) handleOpenClawReadFile(w http.ResponseWriter, r *http.Request) {
	if g.sandboxes == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox management not available")
		return
	}

	filename := chi.URLParam(r, "filename")
	allowed := map[string]bool{"SOUL.md": true, "IDENTITY.md": true, "USER.md": true, "AGENTS.md": true}
	if !allowed[filename] {
		writeError(w, http.StatusBadRequest, "file not allowed")
		return
	}

	filePath := "/root/.openclaw/workspace/" + filename
	output, err := g.sandboxes.ExecInContainer(r.Context(), []string{"cat", filePath})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"filename": filename, "content": ""})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"filename": filename, "content": output})
}
```

And add `ExecInContainer` to `internal/sandbox/manager.go`:

```go
// ExecInContainer runs a raw command inside the OpenClaw container (no "openclaw" prefix).
func (m *Manager) ExecInContainer(ctx context.Context, cmd []string) (string, error) {
	containers, err := m.docker.containerList(ctx, LabelManaged+"=true")
	if err != nil {
		return "", fmt.Errorf("listing containers: %w", err)
	}

	var containerID string
	for _, c := range containers {
		if c.Labels[LabelPolicy] == "openclaw-gateway" && c.State == "running" {
			containerID = c.ID
			break
		}
	}
	if containerID == "" {
		return "", fmt.Errorf("no running OpenClaw container found")
	}

	output, err := m.docker.containerExec(ctx, containerID, cmd, nil)
	if err != nil {
		return "", fmt.Errorf("exec %v: %w", cmd, err)
	}
	return output, nil
}
```

- [ ] **Step 3: Register routes in gateway.go**

Add these lines after line 176 (`r.Get("/api/v1/openclaw/ui", g.handleOpenClawUI)`):

```go
		r.Get("/api/v1/openclaw/channels", g.handleOpenClawChannels)
		r.Post("/api/v1/openclaw/channels", g.handleOpenClawAddChannel)
		r.Delete("/api/v1/openclaw/channels/{name}", g.handleOpenClawRemoveChannel)
		r.Get("/api/v1/openclaw/agents", g.handleOpenClawAgents)
		r.Get("/api/v1/openclaw/skills", g.handleOpenClawSkills)
		r.Get("/api/v1/openclaw/workspace/{filename}", g.handleOpenClawReadFile)
		r.Put("/api/v1/openclaw/workspace/{filename}", g.handleOpenClawWriteFile)
```

- [ ] **Step 4: Verify it compiles**

Run: `cd /Users/maximiliandaub/Code/solon-agents && go build ./...`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/sandbox_handlers.go internal/sandbox/manager.go
git commit -m "feat: add openclaw API endpoints for channels, agents, skills, workspace"
```

---

## Task 2: Agent Settings page — model + access sections

**Closes:** #28 (partial — model + access sections)

**Files:**
- Create: `dashboard/src/pages/instance/AgentSettings.tsx`
- Modify: `dashboard/src/App.tsx` — add route
- Modify: `dashboard/src/components/Sidebar.tsx` — add nav item

- [ ] **Step 1: Create AgentSettings.tsx with model + access sections**

Create `dashboard/src/pages/instance/AgentSettings.tsx`:

```tsx
import { useState, useEffect } from 'react'
import { fetchJSON, getLocalApiKey, isNonLocalhost } from '../../api/client'
import Card from '../../components/Card'

function authHeaders(): Record<string, string> {
  const apiKey = isNonLocalhost() ? getLocalApiKey() : null
  return apiKey ? { Authorization: `Bearer ${apiKey}` } : {}
}

interface AgentInfo {
  name: string
  model?: string
  modelProvider?: string
}

interface ChannelInfo {
  name: string
  type: string
  status: string
  username?: string
}

interface SkillInfo {
  name: string
  description?: string
  enabled: boolean
}

type SettingsTab = 'model' | 'channels' | 'workspace' | 'skills' | 'access'

export default function AgentSettings() {
  const [tab, setTab] = useState<SettingsTab>('model')
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [skills, setSkills] = useState<SkillInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    setLoading(true)
    Promise.all([
      fetchJSON<{ agents?: AgentInfo[] }>('/api/v1/openclaw/agents')
        .then(d => setAgents(d.agents || []))
        .catch(() => {}),
      fetchJSON<{ channels?: ChannelInfo[] }>('/api/v1/openclaw/channels')
        .then(d => setChannels(d.channels || []))
        .catch(() => {}),
      fetchJSON<{ skills?: SkillInfo[] }>('/api/v1/openclaw/skills')
        .then(d => setSkills(d.skills || []))
        .catch(() => {}),
    ]).finally(() => setLoading(false))
  }, [])

  const tabs: { key: SettingsTab; label: string }[] = [
    { key: 'model', label: 'Model' },
    { key: 'channels', label: 'Channels' },
    { key: 'workspace', label: 'Workspace' },
    { key: 'skills', label: 'Skills' },
    { key: 'access', label: 'Access' },
  ]

  return (
    <main className="max-w-4xl mx-auto px-4 sm:px-6 py-8 space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-[var(--text)]">Agent Settings</h1>
        <p className="text-sm text-[var(--text-tertiary)] mt-1">Configure your OpenClaw agent</p>
      </div>

      {error && (
        <div className="px-4 py-2 rounded-lg bg-red-500/10 text-red-400 text-xs flex items-center justify-between">
          <span>{error}</span>
          <button onClick={() => setError('')} className="underline">dismiss</button>
        </div>
      )}

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-[var(--border)]">
        {tabs.map(t => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
              tab === t.key
                ? 'border-[var(--accent)] text-[var(--text)]'
                : 'border-transparent text-[var(--text-tertiary)] hover:text-[var(--text-secondary)]'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {loading ? (
        <p className="text-sm text-[var(--text-tertiary)] animate-pulse">Loading agent configuration...</p>
      ) : (
        <>
          {tab === 'model' && <ModelSection agents={agents} onError={setError} />}
          {tab === 'channels' && <ChannelsSection channels={channels} setChannels={setChannels} onError={setError} />}
          {tab === 'workspace' && <WorkspaceSection onError={setError} />}
          {tab === 'skills' && <SkillsSection skills={skills} />}
          {tab === 'access' && <AccessSection />}
        </>
      )}
    </main>
  )
}

/* ── Model Section ─────────────────────────────────────────────── */

function ModelSection({ agents, onError }: { agents: AgentInfo[]; onError: (e: string) => void }) {
  const agent = agents[0]

  return (
    <Card className="p-5 space-y-4">
      <h3 className="text-sm font-semibold text-[var(--text)]">Model Configuration</h3>
      <div className="space-y-3">
        <div>
          <label className="block text-xs text-[var(--text-tertiary)] mb-1">Current Model</label>
          <p className="text-sm text-[var(--text)]">{agent?.model || 'claude-sonnet-4-6'}</p>
        </div>
        <div>
          <label className="block text-xs text-[var(--text-tertiary)] mb-1">Provider</label>
          <p className="text-sm text-[var(--text)]">{agent?.modelProvider || 'anthropic'}</p>
        </div>
      </div>
      <p className="text-xs text-[var(--text-tertiary)]">
        Model configuration is managed via the OpenClaw workspace files. Edit AGENTS.md in the Workspace tab to change the model.
      </p>
    </Card>
  )
}

/* ── Channels Section ──────────────────────────────────────────── */

function ChannelsSection({ channels, setChannels, onError }: {
  channels: ChannelInfo[]
  setChannels: (c: ChannelInfo[]) => void
  onError: (e: string) => void
}) {
  const [showTelegram, setShowTelegram] = useState(false)
  const [botToken, setBotToken] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [removing, setRemoving] = useState<string | null>(null)

  async function handleConnectTelegram() {
    const token = botToken.trim()
    if (!token) return
    setConnecting(true)
    onError('')
    try {
      await fetch('/api/v1/openclaw/channels', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ channel: 'telegram', bot_token: token }),
      }).then(async r => {
        if (!r.ok) {
          const err = await r.json().catch(() => ({ error: r.statusText })) as { error?: string }
          throw new Error(err.error || `HTTP ${r.status}`)
        }
      })
      setBotToken('')
      setShowTelegram(false)
      // Reload channels
      const d = await fetchJSON<{ channels?: ChannelInfo[] }>('/api/v1/openclaw/channels')
      setChannels(d.channels || [])
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setConnecting(false)
    }
  }

  async function handleRemove(name: string) {
    setRemoving(name)
    try {
      await fetch(`/api/v1/openclaw/channels/${encodeURIComponent(name)}`, {
        method: 'DELETE',
        headers: authHeaders(),
      })
      setChannels(channels.filter(c => c.name !== name))
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setRemoving(null)
    }
  }

  return (
    <div className="space-y-4">
      <Card className="p-5 space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-[var(--text)]">Connected Channels</h3>
        </div>

        {channels.length === 0 ? (
          <p className="text-sm text-[var(--text-tertiary)]">No channels connected yet.</p>
        ) : (
          <div className="space-y-2">
            {channels.map(ch => (
              <div key={ch.name} className="flex items-center justify-between px-3 py-2 rounded-lg bg-[var(--bg)]">
                <div className="flex items-center gap-3">
                  <span className="text-lg">{ch.type === 'telegram' ? '📱' : ch.type === 'whatsapp' ? '💬' : ch.type === 'discord' ? '🎮' : '🔌'}</span>
                  <div>
                    <p className="text-sm text-[var(--text)]">{ch.name}</p>
                    <p className="text-xs text-[var(--text-tertiary)]">{ch.type}{ch.username ? ` · @${ch.username}` : ''}</p>
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <span className={`flex items-center gap-1.5 text-xs ${ch.status === 'connected' ? 'text-green-400' : 'text-yellow-400'}`}>
                    <span className={`w-1.5 h-1.5 rounded-full ${ch.status === 'connected' ? 'bg-green-400' : 'bg-yellow-400'}`} />
                    {ch.status}
                  </span>
                  <button onClick={() => handleRemove(ch.name)} disabled={removing === ch.name}
                    className="text-xs text-red-400 hover:text-red-300 disabled:opacity-50">
                    {removing === ch.name ? '...' : 'Disconnect'}
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* Connect Telegram */}
      <Card className="p-5 space-y-4">
        <h3 className="text-sm font-semibold text-[var(--text)]">Connect a Channel</h3>

        {!showTelegram ? (
          <div className="flex gap-3">
            <button onClick={() => setShowTelegram(true)}
              className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--border)] hover:border-blue-500/30 text-sm text-[var(--text)] transition-colors">
              📱 Telegram
            </button>
            <button disabled
              className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--border)] text-sm text-[var(--text-tertiary)] opacity-50 cursor-not-allowed">
              💬 WhatsApp (soon)
            </button>
            <button disabled
              className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--border)] text-sm text-[var(--text-tertiary)] opacity-50 cursor-not-allowed">
              🎮 Discord (soon)
            </button>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-xs text-[var(--text-secondary)]">
              1. Message <code className="text-[var(--accent)]">@BotFather</code> on Telegram<br />
              2. Send <code className="text-[var(--accent)]">/newbot</code> and follow the instructions<br />
              3. Paste the bot token below
            </p>
            <div className="flex gap-2">
              <input
                value={botToken}
                onChange={e => setBotToken(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleConnectTelegram()}
                placeholder="123456789:ABCdefGHI..."
                className="flex-1 px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-[var(--text)] text-sm font-mono focus:outline-none focus:border-[var(--accent)]"
                disabled={connecting}
              />
              <button onClick={handleConnectTelegram} disabled={!botToken.trim() || connecting}
                className="px-4 py-2 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 disabled:opacity-30">
                {connecting ? 'Connecting...' : 'Connect'}
              </button>
              <button onClick={() => { setShowTelegram(false); setBotToken('') }}
                className="px-3 py-2 rounded-lg text-sm text-[var(--text-tertiary)] hover:text-[var(--text)]">
                Cancel
              </button>
            </div>
          </div>
        )}
      </Card>
    </div>
  )
}

/* ── Workspace Section ─────────────────────────────────────────── */

function WorkspaceSection({ onError }: { onError: (e: string) => void }) {
  const files = ['SOUL.md', 'IDENTITY.md', 'USER.md', 'AGENTS.md'] as const
  const [selectedFile, setSelectedFile] = useState<string>(files[0])
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    setLoading(true)
    setSaved(false)
    fetchJSON<{ content: string }>(`/api/v1/openclaw/workspace/${selectedFile}`)
      .then(d => setContent(d.content || ''))
      .catch(() => setContent(''))
      .finally(() => setLoading(false))
  }, [selectedFile])

  async function handleSave() {
    setSaving(true)
    onError('')
    try {
      await fetch(`/api/v1/openclaw/workspace/${selectedFile}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ content }),
      }).then(async r => {
        if (!r.ok) {
          const err = await r.json().catch(() => ({ error: r.statusText })) as { error?: string }
          throw new Error(err.error || `HTTP ${r.status}`)
        }
      })
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card className="p-5 space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-[var(--text)]">Workspace Files</h3>
        <div className="flex items-center gap-2">
          {saved && <span className="text-xs text-green-400">Saved</span>}
          <button onClick={handleSave} disabled={saving || loading}
            className="px-3 py-1.5 rounded-lg text-xs font-medium bg-[var(--accent)] text-white hover:opacity-90 disabled:opacity-30">
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>

      <div className="flex gap-2">
        {files.map(f => (
          <button
            key={f}
            onClick={() => setSelectedFile(f)}
            className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
              selectedFile === f
                ? 'bg-[var(--accent)] text-white'
                : 'bg-[var(--bg)] text-[var(--text-secondary)] hover:text-[var(--text)]'
            }`}
          >
            {f}
          </button>
        ))}
      </div>

      <p className="text-xs text-[var(--text-tertiary)]">
        {selectedFile === 'SOUL.md' && 'Core personality and behavior rules for your agent.'}
        {selectedFile === 'IDENTITY.md' && 'Who the agent is — name, role, background.'}
        {selectedFile === 'USER.md' && 'Information about you that helps the agent personalize responses.'}
        {selectedFile === 'AGENTS.md' && 'Multi-agent configuration — define sub-agents and their roles.'}
      </p>

      {loading ? (
        <div className="h-64 flex items-center justify-center">
          <p className="text-sm text-[var(--text-tertiary)] animate-pulse">Loading...</p>
        </div>
      ) : (
        <textarea
          value={content}
          onChange={e => { setContent(e.target.value); setSaved(false) }}
          className="w-full h-64 px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-[var(--text)] text-sm font-mono resize-y focus:outline-none focus:border-[var(--accent)]"
          placeholder={`# ${selectedFile}\n\nWrite your agent instructions here...`}
        />
      )}
    </Card>
  )
}

/* ── Skills Section ────────────────────────────────────────────── */

function SkillsSection({ skills }: { skills: SkillInfo[] }) {
  return (
    <Card className="p-5 space-y-4">
      <h3 className="text-sm font-semibold text-[var(--text)]">Skills</h3>
      {skills.length === 0 ? (
        <p className="text-sm text-[var(--text-tertiary)]">No skills found. Skills are managed in the OpenClaw workspace.</p>
      ) : (
        <div className="space-y-2">
          {skills.map(s => (
            <div key={s.name} className="flex items-center justify-between px-3 py-2 rounded-lg bg-[var(--bg)]">
              <div>
                <p className="text-sm text-[var(--text)]">{s.name}</p>
                {s.description && <p className="text-xs text-[var(--text-tertiary)]">{s.description}</p>}
              </div>
              <span className={`text-xs ${s.enabled ? 'text-green-400' : 'text-[var(--text-tertiary)]'}`}>
                {s.enabled ? 'Active' : 'Inactive'}
              </span>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

/* ── Access Section ────────────────────────────────────────────── */

function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  function handleCopy() {
    navigator.clipboard.writeText(value).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <div className="flex items-center justify-between px-3 py-2 rounded-lg bg-[var(--bg)]">
      <div>
        <p className="text-xs text-[var(--text-tertiary)]">{label}</p>
        <code className="text-xs text-[var(--text)] font-mono">{value}</code>
      </div>
      <button onClick={handleCopy} className="text-xs text-[var(--accent)] hover:underline">
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

function AccessSection() {
  const host = window.location.hostname
  return (
    <Card className="p-5 space-y-3">
      <h3 className="text-sm font-semibold text-[var(--text)]">Access Methods</h3>
      <CopyField label="SSH" value={`ssh root@${host}`} />
      <CopyField label="TUI" value="docker exec -it openclaw-sandbox-openclaw openclaw tui" />
      <CopyField label="API" value={`POST ${window.location.origin}/api/v1/openclaw/send`} />
      <div className="flex items-center justify-between px-3 py-2 rounded-lg bg-[var(--bg)]">
        <div>
          <p className="text-xs text-[var(--text-tertiary)]">OpenClaw UI</p>
          <p className="text-xs text-[var(--text)]">Full control panel</p>
        </div>
        <button onClick={() => window.open('/api/v1/openclaw/ui', '_blank')}
          className="text-xs text-[var(--accent)] hover:underline">Open</button>
      </div>
    </Card>
  )
}
```

- [ ] **Step 2: Add route to App.tsx**

Add import after the OpenClawUI import:
```tsx
import AgentSettings from './pages/instance/AgentSettings'
```

Add route after the `/openclaw` route:
```tsx
<Route path="/agent-settings" element={<LocalRoute><AgentSettings /></LocalRoute>} />
```

- [ ] **Step 3: Add nav item in Sidebar.tsx**

Add to the `agentsSection` items array after the OpenClaw UI entry:
```tsx
    {
      to: '/agent-settings',
      label: 'Agent Settings',
      icon: <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg>,
    },
```

- [ ] **Step 4: Build dashboard and verify**

Run: `cd /Users/maximiliandaub/Code/solon-agents && make build-dashboard`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/pages/instance/AgentSettings.tsx dashboard/src/App.tsx dashboard/src/components/Sidebar.tsx
git commit -m "feat: agent settings page — model, channels, workspace, skills, access (#28)"
```

---

## Task 3: Conversation sidebar in Chat

**Closes:** #30

**Files:**
- Modify: `dashboard/src/pages/instance/Chat.tsx`

- [ ] **Step 1: Add session types and sidebar state to Chat.tsx**

Add below the existing `ChatMessage` interface:

```tsx
interface SessionInfo {
  key: string
  sessionId: string
  model: string
  modelProvider: string
  totalTokens: number
  updatedAt: number
}
```

Add to the state declarations in the `Chat` component:

```tsx
const [sessions, setSessions] = useState<SessionInfo[]>([])
const [sidebarOpen, setSidebarOpen] = useState(true)
const [activeSession, setActiveSession] = useState<string | null>(null)
```

- [ ] **Step 2: Load sessions on mount**

Add a `loadSessions` function and call it in the existing `useEffect`:

```tsx
const loadSessions = useCallback(async () => {
  try {
    const d = await fetchJSON<{ sessions?: SessionInfo[] }>('/api/v1/openclaw/sessions')
    setSessions(d.sessions || [])
  } catch { /* */ }
}, [])
```

Call `loadSessions()` inside the existing `useEffect(() => { connect() }, [])` — change it to:
```tsx
useEffect(() => {
  connect()
  loadSessions()
}, [])
```

- [ ] **Step 3: Add session helpers**

```tsx
function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

function timeAgo(timestamp: number): string {
  const diff = Date.now() - timestamp
  const mins = Math.floor(diff / 60000)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}
```

- [ ] **Step 4: Restructure the return JSX to include sidebar**

Replace the outer `<div>` with a flex layout:

```tsx
return (
  <div className="flex h-[calc(100vh-3.5rem)]">
    {/* Sidebar */}
    {sidebarOpen && mode === 'agent' && (
      <div className="w-64 border-r border-[var(--border)] bg-[var(--bg-card)] flex flex-col shrink-0">
        <div className="flex items-center justify-between px-3 py-2 border-b border-[var(--border)]">
          <span className="text-xs font-medium text-[var(--text-tertiary)] uppercase tracking-wider">Conversations</span>
          <button onClick={() => { setActiveSession(null); setMessages([]) }}
            className="text-xs text-[var(--accent)] hover:underline">New</button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {sessions.length === 0 ? (
            <p className="px-3 py-4 text-xs text-[var(--text-tertiary)]">No conversations yet</p>
          ) : (
            sessions.map(s => (
              <button
                key={s.sessionId}
                onClick={() => setActiveSession(s.sessionId)}
                className={`w-full text-left px-3 py-2 text-sm transition-colors ${
                  activeSession === s.sessionId
                    ? 'bg-[var(--bg-hover)] text-[var(--text)]'
                    : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'
                }`}
              >
                <p className="truncate text-xs">{s.key.replace('agent:main:', '') || 'Conversation'}</p>
                <p className="text-[10px] text-[var(--text-tertiary)] mt-0.5">
                  {s.model} · {formatTokens(s.totalTokens)} tokens · {timeAgo(s.updatedAt)}
                </p>
              </button>
            ))
          )}
        </div>
      </div>
    )}

    {/* Main chat area */}
    <div className="flex-1 flex flex-col min-w-0">
      {/* ... existing header, messages, input ... */}
    </div>
  </div>
)
```

- [ ] **Step 5: Add sidebar toggle button to header**

Add a toggle button at the start of the header's left side:

```tsx
{mode === 'agent' && (
  <button onClick={() => setSidebarOpen(!sidebarOpen)}
    className="p-1 rounded text-[var(--text-tertiary)] hover:text-[var(--text)] hover:bg-[var(--bg-hover)]">
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="18" height="18" rx="2" /><line x1="9" y1="3" x2="9" y2="21" />
    </svg>
  </button>
)}
```

- [ ] **Step 6: Build and verify**

Run: `cd /Users/maximiliandaub/Code/solon-agents && make build-dashboard`
Expected: No errors

- [ ] **Step 7: Commit**

```bash
git add dashboard/src/pages/instance/Chat.tsx
git commit -m "feat: conversation sidebar using OpenClaw sessions (#30)"
```

---

## Task 4: Agent selector + model switcher in Chat header

**Closes:** #31

**Files:**
- Modify: `dashboard/src/pages/instance/Chat.tsx`

- [ ] **Step 1: Add agent state to Chat component**

Add new state:

```tsx
const [agentList, setAgentList] = useState<{ name: string }[]>([])
const [selectedAgent, setSelectedAgent] = useState('main')
const [thinkingLevel, setThinkingLevel] = useState<'off' | 'low' | 'medium' | 'high'>('medium')
```

Load agents on mount (add to the existing useEffect):

```tsx
fetchJSON<{ agents?: { name: string }[] }>('/api/v1/openclaw/agents')
  .then(d => setAgentList(d.agents || []))
  .catch(() => {})
```

- [ ] **Step 2: Update the agent send to pass agent + thinking level**

Modify the `sendViaAgent` function's fetch body to include agent selection:

```tsx
body: JSON.stringify({ message: text, agent: selectedAgent, thinking: thinkingLevel }),
```

And update the backend handler `handleOpenClawSend` to accept these:

In `internal/gateway/sandbox_handlers.go`, update the request struct:

```go
var req struct {
    Message  string `json:"message"`
    Agent    string `json:"agent"`
    Thinking string `json:"thinking"`
}
```

And update the exec command:

```go
agent := req.Agent
if agent == "" {
    agent = "main"
}
args := []string{"agent", "--agent", agent, "--message", req.Message, "--json", "--timeout", "180"}
if req.Thinking != "" && req.Thinking != "off" {
    args = append(args, "--thinking", req.Thinking)
}
output, err := g.sandboxes.ExecOpenClawCommand(r.Context(), args)
```

Note: This replaces `ExecOpenClawAgent` with `ExecOpenClawCommand` since we need custom args.

- [ ] **Step 3: Update Chat header with agent selector + thinking toggle**

Replace the existing header left section with:

```tsx
<div className="flex items-center gap-3">
  {mode === 'agent' && (
    <button onClick={() => setSidebarOpen(!sidebarOpen)}
      className="p-1 rounded text-[var(--text-tertiary)] hover:text-[var(--text)] hover:bg-[var(--bg-hover)]">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3" y="3" width="18" height="18" rx="2" /><line x1="9" y1="3" x2="9" y2="21" />
      </svg>
    </button>
  )}
  <h1 className="text-sm font-semibold text-[var(--text)]">Chat</h1>
  <span className={`flex items-center gap-1.5 text-xs ${modeColor}`}>
    <span className={`w-1.5 h-1.5 rounded-full ${dotColor}`} />
    {modeLabel}
  </span>

  {/* Agent selector — only in agent mode with multiple agents */}
  {mode === 'agent' && agentList.length > 1 && (
    <select value={selectedAgent} onChange={e => setSelectedAgent(e.target.value)}
      className="text-xs px-2 py-1 rounded border border-[var(--border)] bg-[var(--bg)] text-[var(--text)]">
      {agentList.map(a => <option key={a.name} value={a.name}>{a.name}</option>)}
    </select>
  )}

  {/* Thinking level — only in agent mode */}
  {mode === 'agent' && (
    <select value={thinkingLevel} onChange={e => setThinkingLevel(e.target.value as typeof thinkingLevel)}
      className="text-xs px-2 py-1 rounded border border-[var(--border)] bg-[var(--bg)] text-[var(--text)]">
      <option value="off">Thinking: Off</option>
      <option value="low">Thinking: Low</option>
      <option value="medium">Thinking: Medium</option>
      <option value="high">Thinking: High</option>
    </select>
  )}

  {/* Model selector — only in direct mode */}
  {mode === 'direct' && models.length > 0 && (
    <select value={selectedModel} onChange={e => setSelectedModel(e.target.value)}
      className="text-xs px-2 py-1 rounded border border-[var(--border)] bg-[var(--bg)] text-[var(--text)]">
      {models.map(m => <option key={m.name} value={m.name}>{m.name}</option>)}
    </select>
  )}
</div>
```

- [ ] **Step 4: Build both Go backend and dashboard**

Run: `cd /Users/maximiliandaub/Code/solon-agents && go build ./... && make build-dashboard`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/pages/instance/Chat.tsx internal/gateway/sandbox_handlers.go
git commit -m "feat: agent selector + model switcher + thinking toggle in Chat header (#31)"
```

---

## Task 5: Update Home page to link to Agent Settings

**Files:**
- Modify: `dashboard/src/pages/Home.tsx`

- [ ] **Step 1: Replace settings link in action cards**

In Home.tsx, update the third action card (currently "Channels" linking to `/settings`) to link to `/agent-settings`:

```tsx
<button onClick={() => navigate('/agent-settings')}
  className="text-left p-5 rounded-xl border border-[var(--border)] bg-[var(--bg-card)] hover:border-blue-500/30 transition-colors">
  <div className="text-2xl mb-2">⚙️</div>
  <h3 className="text-sm font-semibold text-[var(--text)]">Agent Settings</h3>
  <p className="text-xs text-[var(--text-tertiary)] mt-1">Model, channels, workspace, skills</p>
</button>
```

- [ ] **Step 2: Build and verify**

Run: `cd /Users/maximiliandaub/Code/solon-agents && make build-dashboard`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/pages/Home.tsx
git commit -m "feat: link agent cockpit to agent settings page"
```

---

## Task 6: Final build + cleanup PR

- [ ] **Step 1: Full build**

Run: `cd /Users/maximiliandaub/Code/solon-agents && go build ./... && make build-dashboard`
Expected: No errors

- [ ] **Step 2: Create PR**

```bash
gh pr create --base master --head product/solon-agents \
  --title "feat: finish agent track — settings, channels, chat sidebar" \
  --body "## Summary
- Agent settings page with model, channels, workspace editor, skills, and access sections (#28)
- Telegram channel connect from dashboard (#29)
- Conversation sidebar using OpenClaw sessions (#30)
- Agent selector + model switcher + thinking toggle in Chat header (#31)

Closes #28, #29, #30, #31

## Test plan
- [ ] Navigate to Agent Settings page from sidebar
- [ ] Verify model section shows current model
- [ ] Connect a Telegram bot via Channels tab
- [ ] Edit and save a workspace file (SOUL.md)
- [ ] Open Chat — verify conversation sidebar loads sessions
- [ ] Send a message and verify thinking level toggle works
- [ ] Verify agent selector appears when multiple agents exist"
```
