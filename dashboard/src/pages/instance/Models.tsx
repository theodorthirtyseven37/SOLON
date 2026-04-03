import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useInstanceContext } from '../../contexts/InstanceContext'
import { useServerStore } from '../../store/server'
import { pullModel } from '../../api/local'
import Badge from '../../components/Badge'
import type { ModelInfo, CatalogModel, DownloadProgress } from '../../api/types'
import staticCatalog from '../../data/catalog.json'

function formatSize(bytes: number): string {
  if (bytes === 0) return '—'
  const gb = bytes / (1024 * 1024 * 1024)
  if (gb >= 1) return `${gb.toFixed(1)} GB`
  const mb = bytes / (1024 * 1024)
  return `${mb.toFixed(0)} MB`
}

function formatSpeed(bytesPerSec: number): string {
  const mb = bytesPerSec / (1024 * 1024)
  if (mb >= 1) return `${mb.toFixed(1)} MB/s`
  const kb = bytesPerSec / 1024
  return `${kb.toFixed(0)} KB/s`
}

function formatContext(tokens: number): string {
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}K`
  return `${tokens}`
}

const categoryBadge: Record<string, 'blue' | 'green' | 'gray'> = {
  chat: 'blue',
  code: 'green',
  embedding: 'gray',
}

type Compat = 'runs' | 'tight' | 'heavy' | 'unknown'

function getCompat(vramGB: number, totalMemoryMB: number): Compat {
  if (totalMemoryMB === 0) return 'unknown'
  const totalGB = totalMemoryMB / 1024
  const needed = vramGB + 2 // overhead for KV cache, OS, etc.
  if (needed < totalGB * 0.6) return 'runs'
  if (needed < totalGB * 0.9) return 'tight'
  return 'heavy'
}

const compatConfig: Record<Compat, { label: string; color: string; dot: string }> = {
  runs:    { label: 'Runs great', color: 'text-green-500', dot: 'bg-green-500' },
  tight:   { label: 'Tight fit',  color: 'text-yellow-500', dot: 'bg-yellow-500' },
  heavy:   { label: 'Too heavy',  color: 'text-[var(--red)]', dot: 'bg-[var(--red)]' },
  unknown: { label: '',            color: '', dot: '' },
}

interface PullState {
  pulling: boolean
  modelName: string
  file: string
  downloaded: number
  total: number
  percent: number
  speed: number
  error: string | null
}

export default function Models() {
  const { api } = useInstanceContext()
  const totalMemoryMB = useServerStore(s => s.totalMemoryMB)
  const [models, setModels] = useState<ModelInfo[]>([])
  const [catalogModels, setCatalogModels] = useState<CatalogModel[]>([])
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [pullInput, setPullInput] = useState('')
  const [selectedSizes, setSelectedSizes] = useState<Record<string, string>>({})
  const [pull, setPull] = useState<PullState>({
    pulling: false, modelName: '', file: '', downloaded: 0, total: 0, percent: 0, speed: 0, error: null,
  })

  const abortRef = useRef<AbortController | null>(null)
  const speedRef = useRef({ lastBytes: 0, lastTime: 0, ema: 0 })

  const refreshModels = useCallback(() => {
    api.models().then(setModels).catch(() => {})
  }, [api])

  useEffect(() => {
    Promise.all([
      api.models().then(setModels).catch(() => {}),
      api.catalog().then(setCatalogModels).catch(() => {
        // Fallback to static catalog when API unavailable
        setCatalogModels(staticCatalog as unknown as CatalogModel[])
      }),
    ]).finally(() => setLoading(false))
  }, [api])

  // Initialize default selected sizes from catalog
  useEffect(() => {
    if (catalogModels.length === 0) return
    setSelectedSizes(prev => {
      const next = { ...prev }
      let changed = false
      catalogModels.forEach(m => {
        if (!next[m.name] && m.sizes.length > 0) {
          next[m.name] = m.sizes[0]
          changed = true
        }
      })
      return changed ? next : prev
    })
  }, [catalogModels])

  // Filter catalog models not yet installed
  const libraryModels = useMemo(() => {
    return catalogModels.filter(c => {
      const hasInstalled = models.some(m => m.name.startsWith(c.name))
      if (hasInstalled) return false
      if (!search.trim()) return true
      const q = search.toLowerCase()
      return c.name.includes(q) || c.description.toLowerCase().includes(q) || c.creator.toLowerCase().includes(q)
    })
  }, [models, search, catalogModels])

  // Filter installed models
  const filteredInstalled = useMemo(() => {
    if (!search.trim()) return models
    const q = search.toLowerCase()
    return models.filter(m =>
      m.name.toLowerCase().includes(q) ||
      m.family.toLowerCase().includes(q) ||
      m.quantization.toLowerCase().includes(q)
    )
  }, [models, search])

  const startPull = (name: string) => {
    if (pull.pulling) return

    setPull({ pulling: true, modelName: name, file: '', downloaded: 0, total: 0, percent: 0, speed: 0, error: null })
    speedRef.current = { lastBytes: 0, lastTime: Date.now(), ema: 0 }

    abortRef.current = pullModel(name, {
      onProgress: (p: DownloadProgress) => {
        const now = Date.now()
        const sr = speedRef.current
        const elapsed = (now - sr.lastTime) / 1000
        if (elapsed >= 0.5 && p.downloaded > sr.lastBytes) {
          const instant = (p.downloaded - sr.lastBytes) / elapsed
          sr.ema = sr.ema === 0 ? instant : 0.3 * instant + 0.7 * sr.ema
          sr.lastBytes = p.downloaded
          sr.lastTime = now
        }

        setPull(prev => ({
          ...prev,
          file: p.file || prev.file,
          downloaded: p.downloaded,
          total: p.total,
          percent: p.percent,
          speed: sr.ema,
        }))
      },
      onDone: () => {
        setPull(prev => ({ ...prev, pulling: false }))
        setPullInput('')
        refreshModels()
      },
      onError: (message: string) => {
        setPull(prev => ({ ...prev, pulling: false, error: message }))
      },
    })
  }

  const handlePull = () => {
    const name = pullInput.trim()
    if (!name) return
    startPull(name)
  }

  const handleCancel = () => {
    abortRef.current?.abort()
    abortRef.current = null
    setPull(prev => ({ ...prev, pulling: false }))
  }

  const handleDelete = async (name: string) => {
    if (!confirm(`Delete "${name}"? This cannot be undone.`)) return
    try {
      await api.deleteModel(name)
      refreshModels()
    } catch (e: unknown) {
      alert(`Error: ${(e as Error).message}`)
    }
  }

  const catalogFor = (name: string) => catalogModels.find(c => name.startsWith(c.name))

  const getSelectedSize = (modelName: string) => {
    return selectedSizes[modelName] || catalogModels.find(m => m.name === modelName)?.sizes[0] || ''
  }

  return (
    <main className="max-w-4xl mx-auto px-4 sm:px-6 py-8 space-y-8">
      {/* Search */}
      <div className="relative">
        <svg className="absolute left-4 top-1/2 -translate-y-1/2 text-[var(--text-tertiary)]" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" />
        </svg>
        <input
          type="text"
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="Search models..."
          className="w-full pl-12 pr-4 py-3 text-base rounded-xl bg-[var(--bg-card)] border border-[var(--border)] text-[var(--text)] placeholder:text-[var(--text-tertiary)] focus:outline-none focus:border-[var(--text-tertiary)] transition-colors"
        />
      </div>

      {/* Pull custom model */}
      <div className="flex gap-3">
        <input
          type="text"
          value={pullInput}
          onChange={e => setPullInput(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handlePull()}
          placeholder="Pull by name — e.g. llama3.2:8b"
          disabled={pull.pulling}
          className="flex-1 px-4 py-2.5 text-sm rounded-lg bg-[var(--bg-input)] border border-[var(--border)] text-[var(--text)] placeholder:text-[var(--text-tertiary)] focus:outline-none focus:border-[var(--text-tertiary)] disabled:opacity-50 transition-colors"
        />
        <button
          onClick={handlePull}
          disabled={pull.pulling || !pullInput.trim()}
          className="px-5 py-2.5 text-sm font-medium rounded-lg bg-[var(--text)] text-[var(--bg)] hover:opacity-90 disabled:opacity-40 transition-opacity"
        >
          Pull
        </button>
      </div>

      {/* Global progress (for manual pull input) */}
      {pull.pulling && !libraryModels.some(m => `${m.name}:${getSelectedSize(m.name)}` === pull.modelName) && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-card)] p-4 space-y-3">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-[var(--text)] font-mono">{pull.modelName}</span>
            <button onClick={handleCancel} className="text-xs text-[var(--red)] hover:underline">Cancel</button>
          </div>
          <div className="h-1 rounded-full bg-[var(--bg-hover)] overflow-hidden">
            <div
              className="h-full rounded-full bg-[var(--text)] transition-[width] duration-300"
              style={{ width: `${Math.min(pull.percent, 100)}%` }}
            />
          </div>
          <div className="flex items-center justify-between text-xs text-[var(--text-tertiary)]">
            <span>{pull.file || 'Starting...'}</span>
            <span>
              {formatSize(pull.downloaded)} / {formatSize(pull.total)}
              {pull.speed > 0 && ` · ${formatSpeed(pull.speed)}`}
            </span>
          </div>
        </div>
      )}

      {/* Error */}
      {pull.error && (
        <div className="rounded-lg bg-[var(--bg-error)] px-4 py-3 text-sm text-[var(--red)] flex items-center justify-between">
          <span>{pull.error}</span>
          <button onClick={() => setPull(prev => ({ ...prev, error: null }))} className="text-xs underline ml-2">Dismiss</button>
        </div>
      )}

      {/* Installed models */}
      {loading ? (
        <p className="text-sm text-[var(--text-tertiary)]">Loading...</p>
      ) : filteredInstalled.length > 0 && (
        <section>
          <h2 className="text-xs font-medium uppercase tracking-wider text-[var(--text-tertiary)] mb-3">
            Installed ({models.length})
          </h2>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-card)] divide-y divide-[var(--border)]">
            {filteredInstalled.map(model => {
              const catalog = catalogFor(model.name)
              return (
                <div key={model.name} className="group flex items-center gap-4 px-5 py-4 hover:bg-[var(--bg-hover)] transition-colors">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2.5">
                      <span className="font-mono text-sm font-semibold text-[var(--text)] truncate">{model.name}</span>
                      <span className="flex-shrink-0 w-1.5 h-1.5 rounded-full bg-green-500" />
                    </div>
                    <p className="mt-0.5 text-xs text-[var(--text-tertiary)] truncate">
                      {catalog?.creator && <>{catalog.creator} · </>}
                      {[model.family, model.params, model.quantization].filter(Boolean).join(' · ')}
                      {' · '}{formatSize(model.size)}
                    </p>
                  </div>
                  <button
                    onClick={() => handleDelete(model.name)}
                    className="flex-shrink-0 p-1.5 rounded-md text-[var(--text-tertiary)] hover:text-[var(--red)] hover:bg-[var(--bg-hover)] opacity-0 group-hover:opacity-100 transition-all"
                    title="Delete"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                    </svg>
                  </button>
                </div>
              )
            })}
          </div>
        </section>
      )}

      {/* Library */}
      {libraryModels.length > 0 && (
        <section>
          <h2 className="text-xs font-medium uppercase tracking-wider text-[var(--text-tertiary)] mb-3">
            Library
          </h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            {libraryModels.map(model => {
              const size = getSelectedSize(model.name)
              const fullName = `${model.name}:${size}`
              const isPulling = pull.pulling && pull.modelName === fullName
              const vramGB = model.vram[size] || 0
              const compat = getCompat(vramGB, totalMemoryMB)
              const ci = compatConfig[compat]

              return (
                <div
                  key={model.name}
                  className="rounded-xl border border-[var(--border)] hover:border-[var(--text-tertiary)] bg-[var(--bg-card)] p-5 flex flex-col transition-colors"
                >
                  {/* Top row: badge + compat */}
                  <div className="flex items-center justify-between">
                    <Badge variant={categoryBadge[model.category] || 'gray'}>{model.category}</Badge>
                    {compat !== 'unknown' && (
                      <span className={`inline-flex items-center gap-1.5 text-[10px] font-medium ${ci.color}`}>
                        <span className={`w-1.5 h-1.5 rounded-full ${ci.dot}`} />
                        {ci.label}
                      </span>
                    )}
                  </div>

                  {/* Name + creator */}
                  <h3 className="font-mono text-base font-semibold text-[var(--text)] mt-3">{model.name}</h3>
                  <p className="text-xs text-[var(--text-tertiary)]">by {model.creator}</p>

                  {/* Description */}
                  <p className="text-sm text-[var(--text-secondary)] leading-relaxed line-clamp-2 mt-2">{model.description}</p>

                  {/* Capabilities + context */}
                  <div className="flex flex-wrap items-center gap-1.5 mt-3">
                    {model.capabilities.map(cap => (
                      <span key={cap} className="text-[10px] font-medium uppercase tracking-wider text-[var(--text-tertiary)] bg-[var(--bg-hover)] px-1.5 py-0.5 rounded">
                        {cap}
                      </span>
                    ))}
                    <span className="text-[10px] text-[var(--text-tertiary)] ml-auto font-mono">
                      {formatContext(model.context)} ctx
                    </span>
                  </div>

                  {/* Size selector + VRAM + Install */}
                  <div className="mt-auto pt-4 space-y-3">
                    <div className="flex flex-wrap items-center gap-1.5">
                      {model.sizes.map(s => {
                        const v = model.vram[s]
                        return (
                          <button
                            key={s}
                            onClick={() => setSelectedSizes(prev => ({ ...prev, [model.name]: s }))}
                            className={`text-xs font-mono px-2.5 py-1 rounded-md border transition-colors ${
                              size === s
                                ? 'bg-[var(--text)] text-[var(--bg)] border-[var(--text)]'
                                : 'border-[var(--border)] text-[var(--text-tertiary)] hover:text-[var(--text)] hover:border-[var(--text-tertiary)]'
                            }`}
                          >
                            {s}{v ? ` · ${v}GB` : ''}
                          </button>
                        )
                      })}
                    </div>

                    {isPulling ? (
                      <div className="space-y-2">
                        <div className="h-1.5 rounded-full bg-[var(--bg-hover)] overflow-hidden">
                          <div
                            className="h-full rounded-full bg-[var(--text)] transition-[width] duration-300"
                            style={{ width: `${Math.min(pull.percent, 100)}%` }}
                          />
                        </div>
                        <div className="flex items-center justify-between">
                          <span className="text-xs text-[var(--text-tertiary)]">
                            {pull.percent > 0 ? `Installing ${Math.round(pull.percent)}%` : 'Starting...'}
                            {pull.speed > 0 && ` · ${formatSpeed(pull.speed)}`}
                          </span>
                          <button onClick={handleCancel} className="text-xs text-[var(--red)] hover:underline">Cancel</button>
                        </div>
                      </div>
                    ) : (
                      <button
                        onClick={() => startPull(fullName)}
                        disabled={pull.pulling}
                        className={`w-full px-4 py-2 text-sm font-medium rounded-lg transition-opacity ${
                          compat === 'heavy'
                            ? 'bg-[var(--bg-hover)] text-[var(--text-tertiary)] hover:opacity-80 disabled:opacity-40'
                            : 'bg-[var(--text)] text-[var(--bg)] hover:opacity-90 disabled:opacity-40'
                        }`}
                      >
                        {compat === 'heavy' ? 'Install anyway' : 'Install'}
                      </button>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
        </section>
      )}

      {/* Empty state */}
      {!loading && filteredInstalled.length === 0 && libraryModels.length === 0 && search && (
        <p className="text-center text-sm text-[var(--text-tertiary)] py-12">
          No models matching "{search}"
        </p>
      )}
    </main>
  )
}
