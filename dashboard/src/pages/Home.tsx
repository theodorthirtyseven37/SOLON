import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useModeStore } from '../store/mode'
import { useServerStore } from '../store/server'
import { useAuthStore } from '../store/auth'
import Card from '../components/Card'
import { fetchJSON, getLocalApiKey, isNonLocalhost } from '../api/client'
import type { UsageStats } from '../api/types'

function authHeaders(): Record<string, string> {
  const apiKey = isNonLocalhost() ? getLocalApiKey() : null
  return apiKey ? { Authorization: `Bearer ${apiKey}` } : {}
}

function getGreeting(): string {
  const h = new Date().getHours()
  if (h < 12) return 'Good morning'
  if (h < 18) return 'Good afternoon'
  return 'Good evening'
}

function formatNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

interface AgentStatus {
  available: boolean
  running: boolean
  sandbox?: {
    name: string
    status: string
    tier: number
  }
}

interface SessionInfo {
  key: string
  sessionId: string
  model: string
  modelProvider: string
  totalTokens: number
  updatedAt: number
}

export default function Home() {
  const navigate = useNavigate()
  const mode = useModeStore(s => s.mode)
  const user = useAuthStore(s => s.user)
  const status = useServerStore(s => s.status)
  const version = useServerStore(s => s.version)

  const [agentStatus, setAgentStatus] = useState<AgentStatus | null>(null)
  const [stats, setStats] = useState<UsageStats | null>(null)
  const [sessions, setSessions] = useState<SessionInfo[]>([])
  const [launching, setLaunching] = useState(false)
  const [quickMsg, setQuickMsg] = useState('')
  const [quickSending, setQuickSending] = useState(false)
  const [quickReply, setQuickReply] = useState('')
  const [error, setError] = useState('')

  const isLocal = mode === 'local' || mode === 'hybrid'
  const isOnline = status === 'online'
  const name = user?.name?.split(' ')[0] || (isLocal ? '' : null)
  const isRunning = agentStatus?.running

  const loadStatus = useCallback(async () => {
    try {
      const s = await fetchJSON<AgentStatus>('/api/v1/openclaw/status')
      setAgentStatus(s)
    } catch { /* */ }
  }, [])

  useEffect(() => {
    if (!isLocal) return
    loadStatus()
    fetchJSON<UsageStats>('/api/v1/analytics/usage').then(setStats).catch(() => {})
    // Try to load sessions from OpenClaw
    fetchJSON<{ sessions?: SessionInfo[] }>('/api/v1/openclaw/sessions')
      .then(d => setSessions(d.sessions || []))
      .catch(() => {})
  }, [isLocal, loadStatus])

  async function handleLaunch() {
    setLaunching(true)
    setError('')
    try {
      await fetchJSON('/api/v1/openclaw/start', { method: 'POST' })
      await new Promise(r => setTimeout(r, 3000))
      await loadStatus()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLaunching(false)
    }
  }

  async function handleQuickSend() {
    const msg = quickMsg.trim()
    if (!msg || quickSending) return
    setQuickSending(true)
    setQuickReply('')
    try {
      const resp = await fetch('/api/v1/openclaw/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ message: msg }),
      })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const data = await resp.json() as Record<string, unknown>
      const result = data.result as Record<string, unknown> | undefined
      const payloads = result?.payloads as { text?: string }[] | undefined
      const reply = payloads?.[0]?.text || (data.reply as string) || JSON.stringify(data)
      setQuickReply(reply)
      setQuickMsg('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setQuickSending(false)
    }
  }

  function timeAgo(timestamp: number): string {
    const diff = Date.now() - timestamp
    const mins = Math.floor(diff / 60000)
    if (mins < 60) return `${mins}m ago`
    const hours = Math.floor(mins / 60)
    if (hours < 24) return `${hours}h ago`
    return `${Math.floor(hours / 24)}d ago`
  }

  // Cloud mode — redirect to instances
  if (!isLocal) {
    return (
      <main className="max-w-4xl mx-auto px-4 sm:px-6 py-8">
        <Card className="p-6 text-center">
          <p className="text-sm text-[var(--text-secondary)]">
            Connect a Solon instance or deploy a managed server to get started.
          </p>
        </Card>
      </main>
    )
  }

  return (
    <main className="max-w-4xl mx-auto px-4 sm:px-6 py-8 space-y-6">
      {/* Greeting + Status */}
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-[var(--text)]">
          {getGreeting()}{name ? `, ${name}` : ''}.
        </h1>
        <div className="mt-1 flex items-center gap-2">
          <span className={`w-2 h-2 rounded-full ${isOnline ? 'bg-green-500' : 'bg-red-500'}`} />
          <span className="text-sm text-[var(--text-tertiary)]">
            {isOnline ? 'Solon is running' : 'Solon is offline'}
            {version ? ` · v${version}` : ''}
          </span>
        </div>
      </div>

      {/* Agent Status Hero */}
      <Card className="p-6">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className={`w-10 h-10 rounded-full flex items-center justify-center text-xl ${
              isRunning ? 'bg-green-500/10' : 'bg-[var(--bg-hover)]'
            }`}>
              {isRunning ? '🦞' : '○'}
            </div>
            <div>
              <h2 className="text-lg font-semibold text-[var(--text)]">
                {isRunning ? 'Your Agent is running' : 'Start your Agent'}
              </h2>
              <p className="text-sm text-[var(--text-tertiary)]">
                {isRunning
                  ? `${sessions[0]?.model || 'claude-opus-4-6'} · ${sessions.length} session${sessions.length !== 1 ? 's' : ''}`
                  : 'AI agent with tools, browser, and code execution'
                }
              </p>
            </div>
          </div>
          <div className="flex items-center gap-3">
            {isRunning && (
              <span className="flex items-center gap-1.5 text-xs text-green-400">
                <span className="w-2 h-2 rounded-full bg-green-400 animate-pulse" />
                Running
              </span>
            )}
            {!isRunning && (
              <button onClick={handleLaunch} disabled={launching}
                className="px-5 py-2.5 rounded-lg text-sm font-medium bg-green-600 text-white hover:bg-green-500 transition-colors disabled:opacity-50">
                {launching ? 'Starting...' : 'Launch Agent'}
              </button>
            )}
          </div>
        </div>
        {error && <p className="mt-3 text-xs text-red-400">{error}</p>}
        {launching && (
          <div className="mt-4 flex items-center gap-2 text-sm text-yellow-400">
            <span className="w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />
            Pulling image and starting container... This may take a minute on first run.
          </div>
        )}
      </Card>

      {/* Action Cards — only when agent is running */}
      {isRunning && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <button onClick={() => navigate('/chat')}
            className="text-left p-5 rounded-xl border border-[var(--border)] bg-[var(--bg-card)] hover:border-green-500/30 transition-colors">
            <div className="text-2xl mb-2">💬</div>
            <h3 className="text-sm font-semibold text-[var(--text)]">Chat</h3>
            <p className="text-xs text-[var(--text-tertiary)] mt-1">Quick conversation with your agent</p>
          </button>
          <button onClick={() => window.open('/api/v1/openclaw/ui', '_blank')}
            className="text-left p-5 rounded-xl border border-[var(--border)] bg-[var(--bg-card)] hover:border-orange-500/30 transition-colors">
            <div className="text-2xl mb-2">🦞</div>
            <h3 className="text-sm font-semibold text-[var(--text)]">OpenClaw UI</h3>
            <p className="text-xs text-[var(--text-tertiary)] mt-1">Full control panel in a new tab</p>
          </button>
          <button onClick={() => navigate('/agent-settings')}
            className="text-left p-5 rounded-xl border border-[var(--border)] bg-[var(--bg-card)] hover:border-blue-500/30 transition-colors">
            <div className="text-2xl mb-2">⚙️</div>
            <h3 className="text-sm font-semibold text-[var(--text)]">Agent Settings</h3>
            <p className="text-xs text-[var(--text-tertiary)] mt-1">Model, channels, workspace, skills</p>
          </button>
        </div>
      )}

      {/* Quick Chat — inline */}
      {isRunning && (
        <Card className="p-5">
          <h3 className="text-xs font-medium uppercase tracking-wider text-[var(--text-tertiary)] mb-3">Quick Message</h3>
          <div className="flex gap-2">
            <input
              value={quickMsg}
              onChange={e => setQuickMsg(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleQuickSend()}
              placeholder="Ask your agent anything..."
              className="flex-1 px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-[var(--text)] text-sm focus:outline-none focus:border-[var(--accent)]"
              disabled={quickSending}
            />
            <button onClick={handleQuickSend} disabled={!quickMsg.trim() || quickSending}
              className="px-4 py-2 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity disabled:opacity-30">
              {quickSending ? '...' : 'Send'}
            </button>
          </div>
          {quickReply && (
            <div className="mt-3 p-3 rounded-lg bg-[var(--bg)] text-sm text-[var(--text)] whitespace-pre-wrap">
              {quickReply}
            </div>
          )}
          <p className="mt-2 text-xs text-[var(--text-tertiary)]">
            For full conversations, <button onClick={() => navigate('/chat')} className="text-[var(--accent)] hover:underline">open Chat</button>
          </p>
        </Card>
      )}

      {/* Recent Sessions */}
      {isRunning && sessions.length > 0 && (
        <Card className="p-5">
          <h3 className="text-xs font-medium uppercase tracking-wider text-[var(--text-tertiary)] mb-3">Recent Conversations</h3>
          <div className="space-y-2">
            {sessions.slice(0, 5).map(s => (
              <button key={s.sessionId} onClick={() => navigate('/chat')}
                className="w-full text-left flex items-center justify-between px-3 py-2 rounded-lg hover:bg-[var(--bg-hover)] transition-colors">
                <div>
                  <span className="text-sm text-[var(--text)]">{s.key.replace('agent:main:', '')}</span>
                  <span className="ml-2 text-xs text-[var(--text-tertiary)]">{s.model}</span>
                </div>
                <div className="text-xs text-[var(--text-tertiary)]">
                  {formatNumber(s.totalTokens)} tokens · {timeAgo(s.updatedAt)}
                </div>
              </button>
            ))}
          </div>
        </Card>
      )}

      {/* Stats */}
      {stats && stats.total_requests > 0 && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
          <Card className="p-4">
            <p className="text-xs text-[var(--text-tertiary)]">Requests Today</p>
            <p className="text-xl font-bold text-[var(--text)]">{formatNumber(stats.requests_today)}</p>
          </Card>
          <Card className="p-4">
            <p className="text-xs text-[var(--text-tertiary)]">Total Tokens</p>
            <p className="text-xl font-bold text-[var(--text)]">{formatNumber(stats.total_tokens_in + stats.total_tokens_out)}</p>
          </Card>
          <Card className="p-4">
            <p className="text-xs text-[var(--text-tertiary)]">Avg Latency</p>
            <p className="text-xl font-bold text-[var(--text)]">{stats.avg_latency_ms > 0 ? `${Math.round(stats.avg_latency_ms)}ms` : '--'}</p>
          </Card>
          <Card className="p-4">
            <p className="text-xs text-[var(--text-tertiary)]">Model</p>
            <p className="text-xl font-bold text-[var(--text)]">{stats.most_used_model || '--'}</p>
          </Card>
        </div>
      )}

      {/* Access Methods — always visible when running */}
      {isRunning && (
        <Card className="p-5">
          <h3 className="text-xs font-medium uppercase tracking-wider text-[var(--text-tertiary)] mb-3">Access Your Agent</h3>
          <div className="space-y-2 text-sm">
            <div className="flex items-center justify-between py-1">
              <span className="text-[var(--text-secondary)]">🌐 Web Chat</span>
              <button onClick={() => navigate('/chat')} className="text-[var(--accent)] hover:underline text-xs">Open</button>
            </div>
            <div className="flex items-center justify-between py-1">
              <span className="text-[var(--text-secondary)]">🦞 OpenClaw UI</span>
              <button onClick={() => window.open('/api/v1/openclaw/ui', '_blank')} className="text-[var(--accent)] hover:underline text-xs">Open in new tab</button>
            </div>
            <div className="flex items-center justify-between py-1">
              <span className="text-[var(--text-secondary)]">💻 Terminal</span>
              <code className="text-xs text-[var(--text-tertiary)] bg-[var(--bg-code)] px-2 py-0.5 rounded">
                ssh root@{window.location.hostname} → openclaw tui
              </code>
            </div>
            <div className="flex items-center justify-between py-1">
              <span className="text-[var(--text-secondary)]">🔌 API</span>
              <code className="text-xs text-[var(--text-tertiary)] bg-[var(--bg-code)] px-2 py-0.5 rounded">
                POST /api/v1/openclaw/send
              </code>
            </div>
          </div>
        </Card>
      )}

      {/* Empty state for new users without agent */}
      {!isRunning && !launching && agentStatus?.available === false && (
        <Card className="p-6 text-center">
          <p className="text-sm text-[var(--text-secondary)]">
            Install Docker to enable the full agent experience, or use the direct chat with a local or cloud model.
          </p>
          <button onClick={() => navigate('/chat')} className="mt-3 text-sm text-[var(--accent)] hover:underline">
            Go to Chat →
          </button>
        </Card>
      )}
    </main>
  )
}
