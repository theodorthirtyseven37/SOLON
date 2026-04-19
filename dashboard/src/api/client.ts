import { isDesktopApp } from '../lib/mode'

const CLOUD_TOKEN_KEY = 'solon-cloud-token'
const LOCAL_API_KEY = 'solon-local-api-key'
const CLOUD_API_BASE = 'https://api.getsolon.dev'

// Local API key management (for accessing Solon behind reverse proxy)
export function getLocalApiKey(): string | null {
  return localStorage.getItem(LOCAL_API_KEY)
}

export function setLocalApiKey(key: string) {
  localStorage.setItem(LOCAL_API_KEY, key)
}

export function clearLocalApiKey() {
  localStorage.removeItem(LOCAL_API_KEY)
}

export function isNonLocalhost(): boolean {
  const host = window.location.hostname
  return host !== 'localhost' && host !== '127.0.0.1' && host !== '[::1]'
}

function cloudApiUrl(path: string): string {
  if (isDesktopApp()) return `${CLOUD_API_BASE}/api${path}`
  return `/api${path}`
}

export function getToken(): string | null {
  return localStorage.getItem(CLOUD_TOKEN_KEY)
}

export function setToken(token: string) {
  localStorage.setItem(CLOUD_TOKEN_KEY, token)
}

export function clearToken() {
  localStorage.removeItem(CLOUD_TOKEN_KEY)
}

function extractErrorMessage(body: unknown, fallback: string): string {
  if (body && typeof body === 'object') {
    const err = (body as { error?: unknown }).error
    if (typeof err === 'string') return err
    if (err && typeof err === 'object') {
      const msg = (err as { message?: unknown }).message
      if (typeof msg === 'string') return msg
      try { return JSON.stringify(err) } catch { /* fall through */ }
    }
    const topMsg = (body as { message?: unknown }).message
    if (typeof topMsg === 'string') return topMsg
  }
  if (typeof body === 'string' && body) return body
  return fallback
}

export async function fetchJSON<T>(url: string, opts?: RequestInit): Promise<T> {
  const apiKey = isNonLocalhost() ? getLocalApiKey() : null
  const res = await fetch(url, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}),
      ...opts?.headers,
    },
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(extractErrorMessage(body, `HTTP ${res.status} ${res.statusText}`.trim()))
  }
  return res.json()
}

let refreshing: Promise<string | null> | null = null

async function tryRefresh(): Promise<string | null> {
  try {
    const res = await fetch(cloudApiUrl('/auth/refresh'), {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) return null
    const data = await res.json() as { token?: string }
    if (data.token) {
      setToken(data.token)
      return data.token
    }
    return null
  } catch {
    return null
  }
}

export async function cloudFetch<T>(path: string, opts?: RequestInit): Promise<T> {
  const token = getToken()
  const url = cloudApiUrl(path)
  const res = await fetch(url, {
    ...opts,
    credentials: opts?.credentials || 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...opts?.headers,
    },
  })

  if (res.status === 401) {
    // Try refresh (deduplicate concurrent refresh attempts)
    if (!refreshing) refreshing = tryRefresh()
    const newToken = await refreshing
    refreshing = null

    if (newToken) {
      // Retry original request with new token
      const retry = await fetch(url, {
        ...opts,
        credentials: opts?.credentials || 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${newToken}`,
          ...opts?.headers,
        },
      })
      if (retry.ok) return retry.json()
    }

    // Refresh failed — redirect to login
    clearToken()
    window.location.href = '/login'
    throw new Error('Unauthorized')
  }

  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(extractErrorMessage(body, `HTTP ${res.status} ${res.statusText}`.trim()))
  }

  return res.json()
}
