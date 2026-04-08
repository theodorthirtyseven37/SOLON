import { useState } from 'react'
import { setLocalApiKey } from '../api/client'

interface Props {
  onAuthenticated: () => void
}

export default function ApiKeyLogin({ onAuthenticated }: Props) {
  const [key, setKey] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!key.trim()) return

    setLoading(true)
    setError('')

    try {
      const res = await fetch('/api/v1/health', {
        headers: { Authorization: `Bearer ${key.trim()}` },
      })
      if (res.ok) {
        setLocalApiKey(key.trim())
        onAuthenticated()
      } else {
        setError('Invalid API key')
      }
    } catch {
      setError('Cannot reach Solon server')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--bg)] px-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <svg className="mx-auto mb-4" width="40" height="40" viewBox="0 0 28 28" fill="none">
            <circle cx="14" cy="14" r="11" fill="var(--text)" />
          </svg>
          <h1 className="text-xl font-semibold text-[var(--text)]">Solon</h1>
          <p className="text-sm text-[var(--text-tertiary)] mt-1">Enter your API key to access the dashboard</p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <input
            type="password"
            value={key}
            onChange={e => setKey(e.target.value)}
            placeholder="sol_sk_live_..."
            autoFocus
            className="w-full px-4 py-3 text-sm rounded-lg bg-[var(--bg-input)] border border-[var(--border)] text-[var(--text)] placeholder:text-[var(--text-tertiary)] focus:outline-none focus:border-[var(--text-tertiary)]"
          />
          {error && <p className="text-sm text-[var(--red)]">{error}</p>}
          <button
            type="submit"
            disabled={loading || !key.trim()}
            className="w-full px-4 py-3 text-sm font-medium rounded-lg bg-[var(--text)] text-[var(--bg)] hover:opacity-90 disabled:opacity-40 transition-opacity"
          >
            {loading ? 'Verifying...' : 'Sign in'}
          </button>
        </form>

        <p className="text-xs text-[var(--text-tertiary)] text-center mt-6">
          Generate a key with: <code className="bg-[var(--bg-hover)] px-1.5 py-0.5 rounded font-mono">solon keys create</code>
        </p>
      </div>
    </div>
  )
}
