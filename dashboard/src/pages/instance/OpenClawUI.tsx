import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { fetchJSON, getLocalApiKey, isNonLocalhost } from '../../api/client'

export default function OpenClawUI() {
  const navigate = useNavigate()
  const [uiUrl, setUiUrl] = useState<string | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    fetchJSON<{ available: boolean; running: boolean }>('/api/v1/openclaw/status')
      .then(s => {
        if (s.running) {
          // Proxy the OpenClaw control UI through Solon's gateway.
          // On non-localhost we pass the API key as ?token= so the proxy
          // can authenticate and set a cookie for subsequent asset requests.
          const apiKey = isNonLocalhost() ? getLocalApiKey() : null
          const url = apiKey
            ? `/api/v1/openclaw/ui?token=${encodeURIComponent(apiKey)}`
            : '/api/v1/openclaw/ui'
          setUiUrl(url)
        } else {
          setError('OpenClaw agent is not running. Start it from the Dashboard first.')
        }
      })
      .catch(() => setError('Cannot reach Solon server'))
  }, [])

  if (error) {
    return (
      <div className="flex items-center justify-center h-[calc(100vh-3.5rem)]">
        <div className="text-center max-w-sm">
          <p className="text-4xl mb-4">🦞</p>
          <p className="text-sm text-[var(--text-secondary)]">{error}</p>
          <button onClick={() => navigate('/')}
            className="mt-4 px-4 py-2 rounded-lg text-sm bg-[var(--accent)] text-white hover:opacity-90">
            Go to Dashboard
          </button>
        </div>
      </div>
    )
  }

  if (!uiUrl) {
    return (
      <div className="flex items-center justify-center h-[calc(100vh-3.5rem)]">
        <p className="text-sm text-[var(--text-tertiary)] animate-pulse">Connecting to OpenClaw...</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-[calc(100vh-3.5rem)]">
      <div className="flex items-center justify-between px-4 py-2 border-b border-[var(--border)] bg-[var(--bg-card)]">
        <div className="flex items-center gap-3">
          <h1 className="text-sm font-semibold text-[var(--text)]">🦞 OpenClaw Control Panel</h1>
          <span className="flex items-center gap-1.5 text-xs text-green-400">
            <span className="w-1.5 h-1.5 rounded-full bg-green-400" />
            Connected
          </span>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={() => window.open(uiUrl, '_blank')} className="text-xs text-[var(--accent)] hover:underline">
            Open in new tab
          </button>
          <button onClick={() => navigate('/')} className="text-xs text-[var(--text-tertiary)] hover:text-[var(--text)]">
            ← Dashboard
          </button>
        </div>
      </div>
      <iframe
        src={uiUrl}
        className="flex-1 w-full border-none"
        title="OpenClaw Control Panel"
        sandbox="allow-same-origin allow-scripts allow-popups allow-forms"
      />
    </div>
  )
}
