import { useState, useEffect, useCallback } from 'react'
import type { SandboxInfo, SandboxStats, SandboxTier, TelegramIntegration } from '../../api/types'
import { sandboxAPI } from '../../api/local'

const STATUS_COLORS: Record<string, string> = {
  running: 'bg-green-400',
  created: 'bg-yellow-400',
  stopped: 'bg-gray-400',
  failed: 'bg-red-400',
}

const TIER_LABELS: Record<number, string> = {
  1: 'Locked',
  2: 'Standard',
  3: 'Advanced',
  4: 'Maximum',
}

const TIER_COLORS: Record<number, string> = {
  1: 'bg-gray-500/10 text-gray-400',
  2: 'bg-blue-500/10 text-blue-400',
  3: 'bg-purple-500/10 text-purple-400',
  4: 'bg-red-500/10 text-red-400',
}

export default function Sandboxes() {
  const [sandboxes, setSandboxes] = useState<SandboxInfo[]>([])
  const [available, setAvailable] = useState(true)
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [error, setError] = useState('')
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [stats, setStats] = useState<Record<string, SandboxStats>>({})
  const [telegramState, setTelegramState] = useState<Record<string, TelegramIntegration | null>>({})
  const [telegramToken, setTelegramToken] = useState('')
  const [telegramLoading, setTelegramLoading] = useState<string | null>(null)

  const refreshStats = useCallback((id: string) => {
    sandboxAPI.stats(id).then(s => setStats(prev => ({ ...prev, [id]: s }))).catch(() => {})
  }, [])

  const loadTelegram = useCallback((id: string) => {
    sandboxAPI.telegram.get(id).then(ti => setTelegramState(prev => ({ ...prev, [id]: ti }))).catch(() => {})
  }, [])

  const load = () => {
    setLoading(true)
    sandboxAPI.list()
      .then(r => {
        setSandboxes(r.sandboxes || [])
        setAvailable(r.available)
      })
      .catch(e => setError(e.message))
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [])

  const handleAction = async (id: string, action: 'start' | 'stop' | 'remove') => {
    setActionLoading(id)
    try {
      if (action === 'start') await sandboxAPI.start(id)
      else if (action === 'stop') await sandboxAPI.stop(id)
      else if (action === 'remove') await sandboxAPI.remove(id)
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setActionLoading(null)
    }
  }

  if (!available && !loading) {
    return (
      <div className="space-y-6">
        <h1 className="text-lg font-semibold text-[var(--text)]">Sandboxes</h1>
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-card)] p-8 text-center">
          <p className="text-[var(--text-secondary)] mb-2">Docker not detected</p>
          <p className="text-sm text-[var(--text-tertiary)]">
            Sandbox management requires Docker. Install Docker and restart Solon to enable sandboxes.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold text-[var(--text)]">Sandboxes</h1>
          <p className="text-sm text-[var(--text-tertiary)] mt-0.5">
            Run OpenClaw agents in isolated containers with network policies.
          </p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="px-4 py-2 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity"
        >
          Create Sandbox
        </button>
      </div>

      {error && (
        <div className="px-4 py-3 rounded-lg bg-red-500/10 text-red-400 text-sm">
          {error}
          <button onClick={() => setError('')} className="ml-2 underline">dismiss</button>
        </div>
      )}

      {loading ? (
        <div className="text-sm text-[var(--text-tertiary)]">Loading...</div>
      ) : sandboxes.length === 0 ? (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-card)] p-8 text-center">
          <p className="text-[var(--text-secondary)] mb-2">No sandboxes yet</p>
          <p className="text-sm text-[var(--text-tertiary)] mb-4">
            Create a sandbox to run OpenClaw agents safely in an isolated environment.
          </p>
          <button
            onClick={() => setShowCreate(true)}
            className="px-4 py-2 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity"
          >
            Create Your First Sandbox
          </button>
        </div>
      ) : (
        <div className="grid gap-4">
          {sandboxes.map(sb => {
            const isExpanded = expandedId === sb.id
            const sbStats = stats[sb.id]

            return (
              <div key={sb.id} className="rounded-xl border border-[var(--border)] bg-[var(--bg-card)] p-4">
                <div className="flex items-center justify-between">
                  <div
                    className="flex items-center gap-3 cursor-pointer"
                    onClick={() => {
                      if (isExpanded) {
                        setExpandedId(null)
                      } else {
                        setExpandedId(sb.id)
                        if (sb.status === 'running') refreshStats(sb.id)
                        loadTelegram(sb.id)
                      }
                    }}
                  >
                    <span className={`w-2.5 h-2.5 rounded-full ${STATUS_COLORS[sb.status] || 'bg-gray-400'}`} />
                    <div>
                      <span className="font-medium text-[var(--text)]">{sb.name}</span>
                      <span className="text-xs text-[var(--text-tertiary)] ml-2">{sb.status}</span>
                    </div>
                    <svg
                      width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
                      className={`text-[var(--text-tertiary)] transition-transform ${isExpanded ? 'rotate-180' : ''}`}
                    >
                      <polyline points="6 9 12 15 18 9" />
                    </svg>
                  </div>

                  <div className="flex items-center gap-2">
                    <span className={`px-2 py-0.5 rounded text-xs ${TIER_COLORS[sb.tier] || 'bg-[var(--bg-hover)] text-[var(--text-secondary)]'}`}>
                      Tier {sb.tier} — {TIER_LABELS[sb.tier] || 'Standard'}
                    </span>

                    {sb.status === 'created' || sb.status === 'stopped' ? (
                      <button
                        onClick={() => handleAction(sb.id, 'start')}
                        disabled={actionLoading === sb.id}
                        className="px-3 py-1 rounded-lg text-xs font-medium bg-green-500/10 text-green-400 hover:bg-green-500/20 transition-colors disabled:opacity-50"
                      >
                        Start
                      </button>
                    ) : sb.status === 'running' ? (
                      <button
                        onClick={() => handleAction(sb.id, 'stop')}
                        disabled={actionLoading === sb.id}
                        className="px-3 py-1 rounded-lg text-xs font-medium bg-yellow-500/10 text-yellow-400 hover:bg-yellow-500/20 transition-colors disabled:opacity-50"
                      >
                        Stop
                      </button>
                    ) : null}

                    <button
                      onClick={() => handleAction(sb.id, 'remove')}
                      disabled={actionLoading === sb.id}
                      className="px-3 py-1 rounded-lg text-xs text-red-400 hover:bg-red-500/10 transition-colors disabled:opacity-50"
                    >
                      Remove
                    </button>
                  </div>
                </div>

                <div className="mt-3 flex gap-4 text-xs text-[var(--text-tertiary)]">
                  <span>Image: {sb.config?.image || 'node:22-slim'}</span>
                  <span>Created: {new Date(sb.created_at).toLocaleDateString()}</span>
                  {sb.started_at && <span>Started: {new Date(sb.started_at).toLocaleString()}</span>}
                </div>

                {/* Monitoring panel */}
                {isExpanded && sb.status === 'running' && (
                  <div className="mt-4 pt-4 border-t border-[var(--border)]">
                    <div className="flex items-center justify-between mb-3">
                      <p className="text-xs font-medium text-[var(--text-secondary)]">Resource Usage</p>
                      <button
                        onClick={() => refreshStats(sb.id)}
                        className="text-xs text-[var(--accent)] hover:underline"
                      >
                        Refresh
                      </button>
                    </div>
                    {sbStats ? (
                      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                        <MiniStat label="CPU" value={`${sbStats.cpu_percent.toFixed(1)}%`} />
                        <MiniStat label="Memory" value={`${sbStats.mem_usage_mb.toFixed(0)} MB`} sub={`${sbStats.mem_percent.toFixed(1)}% of ${(sbStats.mem_limit_mb / 1024).toFixed(1)} GB`} />
                        <MiniStat label="Net RX" value={`${sbStats.net_rx_mb.toFixed(2)} MB`} />
                        <MiniStat label="Net TX" value={`${sbStats.net_tx_mb.toFixed(2)} MB`} />
                      </div>
                    ) : (
                      <p className="text-xs text-[var(--text-tertiary)]">Loading stats...</p>
                    )}
                  </div>
                )}

                {isExpanded && sb.status !== 'running' && (
                  <div className="mt-4 pt-4 border-t border-[var(--border)]">
                    <p className="text-xs text-[var(--text-tertiary)]">Start the sandbox to see resource usage.</p>
                  </div>
                )}

                {/* Telegram integration */}
                {isExpanded && (
                  <div className="mt-4 pt-4 border-t border-[var(--border)]">
                    <p className="text-xs font-medium text-[var(--text-secondary)] mb-3">Integrations</p>
                    <TelegramSection
                      sandboxId={sb.id}
                      sandboxStatus={sb.status}
                      integration={telegramState[sb.id] ?? undefined}
                      token={expandedId === sb.id ? telegramToken : ''}
                      onTokenChange={setTelegramToken}
                      loading={telegramLoading === sb.id}
                      onSave={async (botToken) => {
                        setTelegramLoading(sb.id)
                        try {
                          const ti = await sandboxAPI.telegram.create(sb.id, botToken)
                          setTelegramState(prev => ({ ...prev, [sb.id]: ti }))
                          setTelegramToken('')
                        } catch (e) {
                          setError((e as Error).message)
                        } finally {
                          setTelegramLoading(null)
                        }
                      }}
                      onConnect={async () => {
                        setTelegramLoading(sb.id)
                        try {
                          await sandboxAPI.telegram.connect(sb.id)
                          loadTelegram(sb.id)
                        } catch (e) {
                          setError((e as Error).message)
                        } finally {
                          setTelegramLoading(null)
                        }
                      }}
                      onDisconnect={async () => {
                        setTelegramLoading(sb.id)
                        try {
                          await sandboxAPI.telegram.disconnect(sb.id)
                          loadTelegram(sb.id)
                        } catch (e) {
                          setError((e as Error).message)
                        } finally {
                          setTelegramLoading(null)
                        }
                      }}
                      onRemove={async () => {
                        setTelegramLoading(sb.id)
                        try {
                          await sandboxAPI.telegram.remove(sb.id)
                          setTelegramState(prev => ({ ...prev, [sb.id]: null }))
                        } catch (e) {
                          setError((e as Error).message)
                        } finally {
                          setTelegramLoading(null)
                        }
                      }}
                    />
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {showCreate && (
        <CreateSandboxModal
          onClose={() => setShowCreate(false)}
          onCreated={() => { setShowCreate(false); load() }}
        />
      )}
    </div>
  )
}

function CreateSandboxModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [tier, setTier] = useState(2)
  const [tiers, setTiers] = useState<SandboxTier[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    sandboxAPI.tiers().then(setTiers).catch(() => {})
  }, [])

  const handleSubmit = async () => {
    if (!name.trim()) {
      setError('Name is required')
      return
    }
    if (!/^[a-z0-9][a-z0-9-]*[a-z0-9]$/.test(name) || name.length < 2) {
      setError('Name must be 2+ lowercase chars, numbers, hyphens')
      return
    }

    setSubmitting(true)
    setError('')
    try {
      await sandboxAPI.create(name.trim(), tier)
      onCreated()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  const tierIcons: Record<number, string> = { 1: '\u{1F512}', 2: '\u{1F510}', 3: '\u{1F513}', 4: '\u{1F680}' }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm" onClick={onClose}>
      <div className="bg-[var(--bg-card)] border border-[var(--border)] rounded-xl shadow-xl w-full max-w-md mx-4 p-6" onClick={e => e.stopPropagation()}>
        <h2 className="text-lg font-semibold text-[var(--text)] mb-4">Create Sandbox</h2>

        <div className="space-y-4">
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1.5">Name</label>
            <input
              type="text"
              value={name}
              onChange={e => setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))}
              placeholder="my-agent"
              className="w-full px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-[var(--text)] text-sm font-mono"
            />
          </div>

          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1.5">Security Tier</label>
            <div className="space-y-2">
              {tiers.map(t => (
                <label
                  key={t.level}
                  className={`flex items-start gap-3 p-3 rounded-lg border cursor-pointer transition-colors ${
                    tier === t.level
                      ? 'border-[var(--accent)] bg-[var(--accent)]/5'
                      : 'border-[var(--border)] hover:border-[var(--text-tertiary)]'
                  }`}
                >
                  <input
                    type="radio"
                    name="tier"
                    value={t.level}
                    checked={tier === t.level}
                    onChange={() => setTier(t.level)}
                    className="mt-0.5"
                  />
                  <div>
                    <div className="text-sm font-medium text-[var(--text)]">
                      {tierIcons[t.level] || ''} Tier {t.level} — {t.name}
                    </div>
                    <div className="text-xs text-[var(--text-tertiary)] mt-0.5">
                      {t.description}
                    </div>
                    <div className="flex flex-wrap gap-1.5 mt-1.5">
                      {t.allow_browser && <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/10 text-green-400">Browser</span>}
                      {t.allow_exec && <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400">Shell</span>}
                      {t.persistent && <span className="text-[10px] px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-400">Persistent</span>}
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--bg-hover)] text-[var(--text-tertiary)]">
                        {t.memory_mb > 0 ? `${t.memory_mb} MB` : 'No limit'}
                      </span>
                    </div>
                  </div>
                </label>
              ))}
            </div>
          </div>

          {error && (
            <div className="text-sm text-red-400">{error}</div>
          )}

          <div className="flex gap-3 pt-2">
            <button
              onClick={onClose}
              className="flex-1 px-4 py-2 rounded-lg text-sm border border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] transition-colors"
            >
              Cancel
            </button>
            <button
              onClick={handleSubmit}
              disabled={submitting}
              className="flex-1 px-4 py-2 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {submitting ? 'Creating...' : 'Create Sandbox'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

function MiniStat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-lg bg-[var(--bg)] border border-[var(--border)] p-2.5">
      <p className="text-[10px] uppercase tracking-wider text-[var(--text-tertiary)]">{label}</p>
      <p className="text-sm font-semibold text-[var(--text)] mt-0.5">{value}</p>
      {sub && <p className="text-[10px] text-[var(--text-tertiary)]">{sub}</p>}
    </div>
  )
}

const TG_STATUS_COLORS: Record<string, string> = {
  connected: 'text-green-400',
  disconnected: 'text-gray-400',
  error: 'text-red-400',
}

function TelegramSection({
  sandboxId: _sandboxId,
  sandboxStatus,
  integration,
  token,
  onTokenChange,
  loading,
  onSave,
  onConnect,
  onDisconnect,
  onRemove,
}: {
  sandboxId: string
  sandboxStatus: string
  integration?: TelegramIntegration
  token: string
  onTokenChange: (t: string) => void
  loading: boolean
  onSave: (botToken: string) => void
  onConnect: () => void
  onDisconnect: () => void
  onRemove: () => void
}) {
  if (integration) {
    return (
      <div className="flex items-center gap-3 rounded-lg bg-[var(--bg)] border border-[var(--border)] p-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-[var(--text)]">Telegram</span>
            {integration.bot_username && (
              <span className="text-xs text-[var(--text-tertiary)]">@{integration.bot_username}</span>
            )}
            <span className={`text-xs font-medium ${TG_STATUS_COLORS[integration.status] || 'text-gray-400'}`}>
              {integration.status}
            </span>
          </div>
          {integration.error_msg && (
            <p className="text-xs text-red-400 mt-1 truncate">{integration.error_msg}</p>
          )}
        </div>
        <div className="flex items-center gap-2">
          {integration.status === 'connected' ? (
            <button
              onClick={onDisconnect}
              disabled={loading}
              className="px-2.5 py-1 rounded text-xs font-medium bg-yellow-500/10 text-yellow-400 hover:bg-yellow-500/20 transition-colors disabled:opacity-50"
            >
              Disconnect
            </button>
          ) : sandboxStatus === 'running' ? (
            <button
              onClick={onConnect}
              disabled={loading}
              className="px-2.5 py-1 rounded text-xs font-medium bg-green-500/10 text-green-400 hover:bg-green-500/20 transition-colors disabled:opacity-50"
            >
              Connect
            </button>
          ) : null}
          <button
            onClick={onRemove}
            disabled={loading}
            className="px-2.5 py-1 rounded text-xs text-red-400 hover:bg-red-500/10 transition-colors disabled:opacity-50"
          >
            Remove
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="rounded-lg bg-[var(--bg)] border border-[var(--border)] p-3">
      <p className="text-xs text-[var(--text-secondary)] mb-2">
        Connect a Telegram bot to chat with this sandbox agent.
      </p>
      <div className="flex gap-2">
        <input
          type="password"
          value={token}
          onChange={e => onTokenChange(e.target.value)}
          placeholder="Paste bot token from @BotFather"
          className="flex-1 px-3 py-1.5 rounded-lg border border-[var(--border)] bg-[var(--bg-card)] text-[var(--text)] text-xs font-mono"
        />
        <button
          onClick={() => onSave(token)}
          disabled={loading || !token.trim()}
          className="px-3 py-1.5 rounded-lg text-xs font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {loading ? 'Saving...' : 'Connect'}
        </button>
      </div>
    </div>
  )
}
