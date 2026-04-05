import { fetchJSON } from './client'
import type { InstanceAPI, HealthStatus, SystemInfo, ModelInfo, APIKey, RequestLogEntry, UsageStats, KeyUsage, TunnelStatus, RemoteStatus, CatalogModel, DownloadProgress, CreateKeyOptions, ProviderConfig, SandboxInfo, SandboxPreset, SandboxStats, SandboxTier, TelegramIntegration } from './types'

// Local instance API — same-origin calls, no auth headers needed
// Go's LocalhostOrAuth middleware handles authentication for localhost

export const localAPI: InstanceAPI = {
  health: () => fetchJSON<HealthStatus>('/api/v1/health'),

  system: () => fetchJSON<SystemInfo>('/api/v1/system'),

  models: () =>
    fetchJSON<{ models: ModelInfo[] }>('/api/v1/models').then(r => r.models || []),

  deleteModel: (name: string) =>
    fetchJSON<{ status: string }>(`/api/v1/models/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  keys: {
    list: () =>
      fetchJSON<{ keys: APIKey[] }>('/api/v1/keys').then(r => r.keys || []),
    create: (opts: CreateKeyOptions) =>
      fetchJSON<{ key: string; name: string; id: string }>('/api/v1/keys', {
        method: 'POST',
        body: JSON.stringify(opts),
      }),
    revoke: (id: string) =>
      fetchJSON<{ status: string }>(`/api/v1/keys/${id}`, { method: 'DELETE' }),
  },

  analytics: {
    requests: () =>
      fetchJSON<{ requests: RequestLogEntry[] }>('/api/v1/analytics/requests').then(r => r.requests || []),
    usage: () => fetchJSON<UsageStats>('/api/v1/analytics/usage'),
    usageByKey: () =>
      fetchJSON<{ usage: Record<string, KeyUsage> }>('/api/v1/analytics/usage/keys').then(r => r.usage || {}),
  },

  tunnel: {
    status: () => fetchJSON<TunnelStatus>('/api/v1/tunnel/status'),
    enable: () => fetchJSON<TunnelStatus>('/api/v1/tunnel/enable', { method: 'POST' }),
    disable: () => fetchJSON<{ status: string }>('/api/v1/tunnel/disable', { method: 'POST' }),
  },

  remote: {
    status: () => fetchJSON<RemoteStatus>('/api/v1/remote/status'),
  },

  catalog: () =>
    fetchJSON<{ models: CatalogModel[] }>('/api/v1/models/catalog').then(r => r.models || []),
}

// --- Provider API (not part of InstanceAPI interface, standalone functions) ---

export const providerAPI = {
  list: () =>
    fetchJSON<{ providers: ProviderConfig[] }>('/api/v1/providers').then(r => r.providers || []),

  add: (name: string, apiKey: string, baseUrl?: string) =>
    fetchJSON<ProviderConfig>('/api/v1/providers', {
      method: 'POST',
      body: JSON.stringify({ name, api_key: apiKey, base_url: baseUrl }),
    }),

  remove: (name: string) =>
    fetchJSON<{ status: string }>(`/api/v1/providers/${encodeURIComponent(name)}`, { method: 'DELETE' }),
}

// --- Sandbox API ---

export const sandboxAPI = {
  list: () =>
    fetchJSON<{ sandboxes: SandboxInfo[]; available: boolean }>('/api/v1/sandboxes'),

  create: (name: string, tier: number, env?: Record<string, string>) =>
    fetchJSON<SandboxInfo>('/api/v1/sandboxes', {
      method: 'POST',
      body: JSON.stringify({ name, tier, env }),
    }),

  get: (id: string) =>
    fetchJSON<SandboxInfo>(`/api/v1/sandboxes/${id}`),

  start: (id: string) =>
    fetchJSON<{ status: string }>(`/api/v1/sandboxes/${id}/start`, { method: 'POST' }),

  stop: (id: string) =>
    fetchJSON<{ status: string }>(`/api/v1/sandboxes/${id}/stop`, { method: 'POST' }),

  remove: (id: string) =>
    fetchJSON<{ status: string }>(`/api/v1/sandboxes/${id}`, { method: 'DELETE' }),

  presets: () =>
    fetchJSON<{ presets: SandboxPreset[] }>('/api/v1/sandboxes/presets').then(r => r.presets || []),

  tiers: () =>
    fetchJSON<{ tiers: SandboxTier[] }>('/api/v1/sandboxes/tiers').then(r => r.tiers || []),

  stats: (id: string) =>
    fetchJSON<SandboxStats>(`/api/v1/sandboxes/${id}/stats`),

  telegram: {
    get: (id: string) =>
      fetchJSON<{ integration: TelegramIntegration | null }>(`/api/v1/sandboxes/${id}/integrations/telegram`).then(r => r.integration),

    create: (id: string, botToken: string) =>
      fetchJSON<{ integration: TelegramIntegration }>(`/api/v1/sandboxes/${id}/integrations/telegram`, {
        method: 'POST',
        body: JSON.stringify({ bot_token: botToken }),
      }).then(r => r.integration),

    remove: (id: string) =>
      fetchJSON<{ status: string }>(`/api/v1/sandboxes/${id}/integrations/telegram`, { method: 'DELETE' }),

    connect: (id: string) =>
      fetchJSON<{ status: string }>(`/api/v1/sandboxes/${id}/integrations/telegram/connect`, { method: 'POST' }),

    disconnect: (id: string) =>
      fetchJSON<{ status: string }>(`/api/v1/sandboxes/${id}/integrations/telegram/disconnect`, { method: 'POST' }),
  },
}

export interface PullModelCallbacks {
  onProgress: (progress: DownloadProgress) => void
  onDone: () => void
  onError: (error: string) => void
}

/**
 * Pulls a model via SSE. Returns an AbortController for cancellation.
 */
export function pullModel(name: string, callbacks: PullModelCallbacks): AbortController {
  const controller = new AbortController()

  ;(async () => {
    try {
      const res = await fetch('/api/v1/models/pull', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, stream: true }),
        signal: controller.signal,
      })

      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: { message: res.statusText } }))
        callbacks.onError(err.error?.message || res.statusText)
        return
      }

      const reader = res.body?.getReader()
      if (!reader) {
        callbacks.onError('No response body')
        return
      }

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (!trimmed || trimmed.startsWith(':')) continue

          if (trimmed.startsWith('data: ')) {
            try {
              const data: DownloadProgress = JSON.parse(trimmed.slice(6))
              if (data.event === 'done') {
                callbacks.onDone()
                return
              }
              if (data.event === 'error') {
                callbacks.onError(data.message || 'Pull failed')
                return
              }
              callbacks.onProgress(data)
            } catch {
              // skip malformed chunks
            }
          }
        }
      }

      callbacks.onDone()
    } catch (err) {
      if ((err as Error).name === 'AbortError') return
      callbacks.onError((err as Error).message)
    }
  })()

  return controller
}
