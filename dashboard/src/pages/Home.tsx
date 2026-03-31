import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useInstanceContext } from '../contexts/InstanceContext'
import { useServerStore } from '../store/server'
import { useAuthStore } from '../store/auth'
import { useModeStore } from '../store/mode'
import Card from '../components/Card'
import { providerAPI } from '../api/local'
import { fetchJSON } from '../api/client'
import type { UsageStats, ModelInfo } from '../api/types'

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

function StatCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <Card className="p-5">
      <p className="text-xs font-medium uppercase tracking-wider text-[var(--text-tertiary)]">{label}</p>
      <p className="mt-1 text-2xl font-bold text-[var(--text)]">{value}</p>
      {sub && <p className="mt-0.5 text-xs text-[var(--text-tertiary)]">{sub}</p>}
    </Card>
  )
}

function ActionButton({ label, description, onClick, muted }: { label: string; description: string; onClick: () => void; muted?: boolean }) {
  return (
    <button
      onClick={onClick}
      className={`flex-1 text-left px-5 py-4 rounded-xl border transition-colors ${
        muted
          ? 'border-[var(--border)] bg-[var(--bg-card)] text-[var(--text-tertiary)] cursor-default'
          : 'border-[var(--border)] bg-[var(--bg-card)] hover:border-brand-light/30 hover:bg-[var(--card-hover)] text-[var(--text)]'
      }`}
    >
      <span className="text-sm font-semibold">{label}</span>
      <p className="mt-0.5 text-xs text-[var(--text-tertiary)]">{description}</p>
    </button>
  )
}

function OpenClawCard() {
  const [launching, setLaunching] = useState(false)
  const [status, setStatus] = useState<{ running: boolean } | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    fetchJSON<{ available: boolean; running: boolean }>('/api/v1/openclaw/status')
      .then(setStatus)
      .catch(() => {})
  }, [])

  const handleLaunch = async () => {
    setLaunching(true)
    setError('')
    try {
      await fetchJSON('/api/v1/openclaw/start', { method: 'POST' })
      const s = await fetchJSON<{ available: boolean; running: boolean }>('/api/v1/openclaw/status')
      setStatus(s)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLaunching(false)
    }
  }

  const isRunning = status?.running

  return (
    <Card className="p-5">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold text-[var(--text)]">OpenClaw Agent</h3>
          <p className="text-xs text-[var(--text-tertiary)]">
            {isRunning ? 'Running in secure sandbox' : 'AI agent with tools and web access'}
          </p>
        </div>
        <div className="flex items-center gap-3">
          {isRunning && (
            <span className="flex items-center gap-1.5 text-xs text-green-400">
              <span className="w-2 h-2 rounded-full bg-green-400 animate-pulse" />
              Running
            </span>
          )}
          <button
            onClick={handleLaunch}
            disabled={launching}
            className={`px-4 py-2 rounded-lg text-sm font-medium transition-opacity disabled:opacity-50 ${
              isRunning
                ? 'bg-[var(--bg-hover)] text-[var(--text-secondary)]'
                : 'bg-brand text-white hover:opacity-90'
            }`}
          >
            {launching ? 'Starting...' : isRunning ? 'Restart' : 'Launch'}
          </button>
        </div>
      </div>
      {error && <p className="mt-2 text-xs text-red-400">{error}</p>}
    </Card>
  )
}

export default function Home() {
  const navigate = useNavigate()
  const mode = useModeStore(s => s.mode)
  const user = useAuthStore(s => s.user)
  const status = useServerStore(s => s.status)
  const version = useServerStore(s => s.version)

  const [stats, setStats] = useState<UsageStats | null>(null)
  const [models, setModels] = useState<ModelInfo[]>([])
  const [hasProviders, setHasProviders] = useState<boolean | null>(null)
  const [loading, setLoading] = useState(true)

  const isLocal = mode === 'local' || mode === 'hybrid'
  const isOnline = status === 'online'
  const name = user?.name?.split(' ')[0] || (isLocal ? '' : null)

  useEffect(() => {
    if (!isLocal) {
      setLoading(false)
      return
    }
    // Only try to fetch when we have a local instance context
    Promise.all([
      fetch('/api/v1/analytics/usage').then(r => r.ok ? r.json() : null).then(setStats).catch(() => {}),
      fetch('/api/v1/models').then(r => r.ok ? r.json() : null).then(d => setModels(d?.models || [])).catch(() => {}),
      providerAPI.list().then(p => setHasProviders(p.length > 0)).catch(() => setHasProviders(false)),
    ]).finally(() => setLoading(false))
  }, [isLocal])

  const hasActivity = stats && stats.total_requests > 0

  return (
    <main className="max-w-4xl mx-auto px-4 sm:px-6 py-8 space-y-8">
      {/* Greeting */}
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-[var(--text)]">
          {getGreeting()}{name ? `, ${name}` : ''}.
        </h1>
        {isLocal && (
          <div className="mt-1 flex items-center gap-2">
            <span className={`w-2 h-2 rounded-full ${isOnline ? 'bg-green-500' : 'bg-red-500'}`} />
            <span className="text-sm text-[var(--text-tertiary)]">
              {isOnline ? 'Solon is running' : 'Solon is offline'}
              {version ? ` \u00b7 v${version}` : ''}
            </span>
          </div>
        )}
      </div>

      {/* Three action buttons */}
      <div className="flex gap-3">
        <ActionButton
          label="+ Create Agent"
          description="OpenClaw in a secure sandbox"
          onClick={() => navigate('/sandboxes')}
        />
        <ActionButton
          label="Run Model Locally"
          description="Pull and run open-source models"
          onClick={() => navigate('/models')}
        />
        <ActionButton
          label="Run Model in Cloud"
          description="Managed GPU hosting"
          onClick={() => {}}
          muted
        />
      </div>

      {/* OpenClaw card — if providers configured */}
      {isLocal && hasProviders && <OpenClawCard />}

      {/* Stats — if has activity */}
      {isLocal && hasActivity && stats && (
        <>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <StatCard label="Requests Today" value={formatNumber(stats.requests_today)} />
            <StatCard
              label="Total Tokens"
              value={formatNumber(stats.total_tokens_in + stats.total_tokens_out)}
              sub={`${formatNumber(stats.total_tokens_in)} in / ${formatNumber(stats.total_tokens_out)} out`}
            />
            <StatCard
              label="Avg Latency"
              value={stats.avg_latency_ms > 0 ? `${Math.round(stats.avg_latency_ms)}ms` : '--'}
            />
            <StatCard label="Most Used" value={stats.most_used_model || '--'} />
          </div>
          <div className="grid grid-cols-2 sm:grid-cols-3 gap-4">
            <StatCard label="Total Requests" value={formatNumber(stats.total_requests)} />
            <StatCard label="API Keys Active" value={String(stats.unique_keys_used)} />
            <StatCard label="Models Installed" value={String(models.length)} />
          </div>
        </>
      )}

      {/* Empty state for new users */}
      {isLocal && !loading && !hasActivity && !hasProviders && (
        <Card className="p-6 text-center">
          <p className="text-sm text-[var(--text-secondary)]">
            Pick one of the options above to get started. Add a provider key, pull a model, or create an agent.
          </p>
        </Card>
      )}

      {/* Cloud empty state */}
      {!isLocal && (
        <Card className="p-6 text-center">
          <p className="text-sm text-[var(--text-secondary)]">
            Connect a Solon instance or deploy a managed server to get started.
          </p>
        </Card>
      )}
    </main>
  )
}
