import { useState, useEffect, useCallback } from 'react'
import { fetchJSON, getLocalApiKey, isNonLocalhost } from '../../api/client'
import Card from '../../components/Card'

function authHeaders(): Record<string, string> {
  const apiKey = isNonLocalhost() ? getLocalApiKey() : null
  return apiKey ? { Authorization: `Bearer ${apiKey}` } : {}
}

interface Agent {
  name: string
  model: string
  modelProvider: string
}

interface Channel {
  name: string
  type: string
  status: string
  username: string
}

interface Skill {
  name: string
  description: string
  enabled: boolean
}

type Tab = 'model' | 'channels' | 'workspace' | 'skills' | 'access'

const TABS: { id: Tab; label: string }[] = [
  { id: 'model', label: 'Model' },
  { id: 'channels', label: 'Channels' },
  { id: 'workspace', label: 'Workspace' },
  { id: 'skills', label: 'Skills' },
  { id: 'access', label: 'Access' },
]

const WORKSPACE_FILES: { name: string; description: string }[] = [
  { name: 'SOUL.md', description: 'Core personality, values, and behavioral principles of your agent.' },
  { name: 'IDENTITY.md', description: 'Agent name, role, and self-description presented to users.' },
  { name: 'USER.md', description: 'Information about the user — their preferences, context, and goals.' },
  { name: 'AGENTS.md', description: 'Other agents in the network and how to collaborate with them.' },
]

function channelIcon(type: string): string {
  switch (type.toLowerCase()) {
    case 'telegram': return '📱'
    case 'whatsapp': return '💬'
    case 'discord': return '🎮'
    default: return '🔌'
  }
}

export default function AgentSettings() {
  const [activeTab, setActiveTab] = useState<Tab>('model')

  // Data state
  const [agents, setAgents] = useState<Agent[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // Channels tab state
  const [showTelegramForm, setShowTelegramForm] = useState(false)
  const [botToken, setBotToken] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [connectError, setConnectError] = useState('')

  // Workspace tab state
  const [selectedFile, setSelectedFile] = useState('SOUL.md')
  const [fileContent, setFileContent] = useState('')
  const [fileLoading, setFileLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  // Access tab state
  const [copiedField, setCopiedField] = useState<string | null>(null)

  const loadChannels = useCallback(async () => {
    try {
      const data = await fetchJSON<{ channels: Channel[] }>('/api/v1/openclaw/channels')
      setChannels(data.channels || [])
    } catch {
      // non-fatal
    }
  }, [])

  useEffect(() => {
    const load = async () => {
      setLoading(true)
      setError('')
      try {
        const [agentsData, channelsData, skillsData] = await Promise.all([
          fetchJSON<{ agents: Agent[] }>('/api/v1/openclaw/agents').catch(() => ({ agents: [] })),
          fetchJSON<{ channels: Channel[] }>('/api/v1/openclaw/channels').catch(() => ({ channels: [] })),
          fetchJSON<{ skills: Skill[] }>('/api/v1/openclaw/skills').catch(() => ({ skills: [] })),
        ])
        setAgents(agentsData.agents || [])
        setChannels(channelsData.channels || [])
        setSkills(skillsData.skills || [])
      } catch (e: unknown) {
        setError((e as Error).message)
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [])

  useEffect(() => {
    if (activeTab !== 'workspace') return
    const load = async () => {
      setFileLoading(true)
      try {
        const data = await fetchJSON<{ filename: string; content: string }>(
          `/api/v1/openclaw/workspace/${selectedFile}`
        )
        setFileContent(data.content || '')
      } catch {
        setFileContent('')
      } finally {
        setFileLoading(false)
      }
    }
    load()
  }, [activeTab, selectedFile])

  const handleConnect = async () => {
    if (!botToken.trim()) return
    setConnecting(true)
    setConnectError('')
    try {
      const res = await fetch('/api/v1/openclaw/channels', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ channel: 'telegram', bot_token: botToken.trim() }),
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error((err as { error?: string }).error || res.statusText)
      }
      setBotToken('')
      setShowTelegramForm(false)
      await loadChannels()
    } catch (e: unknown) {
      setConnectError((e as Error).message)
    } finally {
      setConnecting(false)
    }
  }

  const handleDisconnect = async (name: string) => {
    try {
      await fetch(`/api/v1/openclaw/channels/${encodeURIComponent(name)}`, {
        method: 'DELETE',
        headers: authHeaders(),
      })
      await loadChannels()
    } catch {
      // silently fail — reload will reflect truth
    }
  }

  const handleSaveWorkspace = async () => {
    setSaving(true)
    try {
      await fetch(`/api/v1/openclaw/workspace/${selectedFile}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ content: fileContent }),
      })
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch {
      // silently fail
    } finally {
      setSaving(false)
    }
  }

  const copyToClipboard = async (value: string, field: string) => {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = value
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    setCopiedField(field)
    setTimeout(() => setCopiedField(null), 2000)
  }

  const agent = agents[0]
  const host = window.location.host
  const origin = window.location.origin

  return (
    <main className="max-w-3xl mx-auto px-4 sm:px-6 py-8">
      <h1 className="text-2xl font-bold tracking-tight text-[var(--text)] mb-6">Agent Settings</h1>

      {error && (
        <div className="rounded-lg bg-[var(--bg-error)] px-4 py-3 text-sm text-[var(--red)] mb-6">
          {error}
        </div>
      )}

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-[var(--border)] mb-6">
        {TABS.map(tab => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`px-4 py-2 text-sm font-medium transition-colors border-b-2 -mb-px ${
              activeTab === tab.id
                ? 'border-[var(--accent)] text-[var(--text)]'
                : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text)]'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {loading ? (
        <div className="text-sm text-[var(--text-tertiary)] animate-pulse">Loading...</div>
      ) : (
        <>
          {/* Model tab */}
          {activeTab === 'model' && (
            <Card className="p-6">
              <h3 className="text-base font-semibold text-[var(--text)] mb-4">Current Model</h3>
              {agent ? (
                <div className="space-y-3">
                  <div className="flex items-center justify-between py-2 border-b border-[var(--border)]">
                    <span className="text-sm text-[var(--text-tertiary)]">Agent</span>
                    <span className="text-sm font-medium text-[var(--text)]">{agent.name}</span>
                  </div>
                  <div className="flex items-center justify-between py-2 border-b border-[var(--border)]">
                    <span className="text-sm text-[var(--text-tertiary)]">Model</span>
                    <span className="text-sm font-medium text-[var(--text)]">{agent.model}</span>
                  </div>
                  <div className="flex items-center justify-between py-2">
                    <span className="text-sm text-[var(--text-tertiary)]">Provider</span>
                    <span className="text-sm font-medium text-[var(--text)]">{agent.modelProvider}</span>
                  </div>
                </div>
              ) : (
                <p className="text-sm text-[var(--text-secondary)]">No agent found.</p>
              )}
              <p className="mt-4 text-xs text-[var(--text-tertiary)]">
                To change the model, edit the AGENTS.md file in the Workspace tab.
              </p>
            </Card>
          )}

          {/* Channels tab */}
          {activeTab === 'channels' && (
            <div className="space-y-6">
              {/* Connected channels */}
              <Card className="p-6">
                <h3 className="text-base font-semibold text-[var(--text)] mb-4">Connected Channels</h3>
                {channels.length === 0 ? (
                  <p className="text-sm text-[var(--text-secondary)]">No channels connected yet.</p>
                ) : (
                  <div className="space-y-2">
                    {channels.map(ch => (
                      <div
                        key={ch.name}
                        className="flex items-center justify-between py-3 border-b border-[var(--border)] last:border-0"
                      >
                        <div className="flex items-center gap-3">
                          <span className="text-lg">{channelIcon(ch.type)}</span>
                          <div>
                            <p className="text-sm font-medium text-[var(--text)]">{ch.name}</p>
                            <p className="text-xs text-[var(--text-tertiary)]">
                              {ch.type}{ch.username ? ` · @${ch.username}` : ''}
                            </p>
                          </div>
                        </div>
                        <div className="flex items-center gap-3">
                          <span className="flex items-center gap-1.5 text-xs">
                            <span
                              className={`w-2 h-2 rounded-full ${
                                ch.status === 'connected' ? 'bg-green-400' : 'bg-yellow-400'
                              }`}
                            />
                            <span className="text-[var(--text-tertiary)]">{ch.status}</span>
                          </span>
                          <button
                            onClick={() => handleDisconnect(ch.name)}
                            className="text-xs text-[var(--red)] hover:underline"
                          >
                            Disconnect
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </Card>

              {/* Connect a channel */}
              <Card className="p-6">
                <h3 className="text-base font-semibold text-[var(--text)] mb-4">Connect a Channel</h3>
                <div className="flex gap-2 flex-wrap">
                  <button
                    onClick={() => setShowTelegramForm(v => !v)}
                    className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--border)] text-sm text-[var(--text)] hover:bg-[var(--bg-hover)] transition-colors"
                  >
                    <span>📱</span> Telegram
                  </button>
                  <button
                    disabled
                    className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--border)] text-sm text-[var(--text-tertiary)] opacity-50 cursor-not-allowed"
                    title="Coming soon"
                  >
                    <span>💬</span> WhatsApp <span className="text-[10px] text-[var(--text-tertiary)]">soon</span>
                  </button>
                  <button
                    disabled
                    className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--border)] text-sm text-[var(--text-tertiary)] opacity-50 cursor-not-allowed"
                    title="Coming soon"
                  >
                    <span>🎮</span> Discord <span className="text-[10px] text-[var(--text-tertiary)]">soon</span>
                  </button>
                </div>

                {showTelegramForm && (
                  <div className="mt-4 space-y-3">
                    <div className="rounded-lg bg-[var(--bg-hover)] px-4 py-3 text-sm text-[var(--text-secondary)]">
                      <p className="font-medium text-[var(--text)] mb-1">Setup instructions</p>
                      <ol className="list-decimal list-inside space-y-1 text-xs">
                        <li>Open Telegram and search for <span className="font-mono">@BotFather</span></li>
                        <li>Send <span className="font-mono">/newbot</span> and follow the prompts</li>
                        <li>Copy the bot token and paste it below</li>
                      </ol>
                    </div>
                    <div className="flex gap-2">
                      <input
                        type="text"
                        value={botToken}
                        onChange={e => setBotToken(e.target.value)}
                        placeholder="1234567890:ABCDefgh..."
                        className="flex-1 px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-sm text-[var(--text)] placeholder-[var(--text-tertiary)] focus:outline-none focus:border-[var(--accent)]"
                      />
                      <button
                        onClick={handleConnect}
                        disabled={connecting || !botToken.trim()}
                        className="px-4 py-2 rounded-lg bg-[var(--accent)] text-white text-sm font-medium hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed transition-opacity"
                      >
                        {connecting ? 'Connecting...' : 'Connect'}
                      </button>
                    </div>
                    {connectError && (
                      <p className="text-xs text-[var(--red)]">{connectError}</p>
                    )}
                  </div>
                )}
              </Card>
            </div>
          )}

          {/* Workspace tab */}
          {activeTab === 'workspace' && (
            <Card className="p-6">
              <h3 className="text-base font-semibold text-[var(--text)] mb-4">Workspace Files</h3>

              {/* File selector */}
              <div className="flex gap-2 flex-wrap mb-2">
                {WORKSPACE_FILES.map(f => (
                  <button
                    key={f.name}
                    onClick={() => setSelectedFile(f.name)}
                    className={`px-3 py-1.5 rounded-lg text-sm font-mono transition-colors border ${
                      selectedFile === f.name
                        ? 'bg-[var(--accent)] text-white border-[var(--accent)]'
                        : 'border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text)] hover:bg-[var(--bg-hover)]'
                    }`}
                  >
                    {f.name}
                  </button>
                ))}
              </div>

              {/* File description */}
              {WORKSPACE_FILES.find(f => f.name === selectedFile) && (
                <p className="text-xs text-[var(--text-tertiary)] mb-3">
                  {WORKSPACE_FILES.find(f => f.name === selectedFile)!.description}
                </p>
              )}

              {/* Editor */}
              {fileLoading ? (
                <div className="h-64 flex items-center justify-center text-sm text-[var(--text-tertiary)] animate-pulse">
                  Loading...
                </div>
              ) : (
                <textarea
                  value={fileContent}
                  onChange={e => setFileContent(e.target.value)}
                  className="w-full h-64 px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-sm font-mono text-[var(--text)] placeholder-[var(--text-tertiary)] focus:outline-none focus:border-[var(--accent)] resize-y"
                  placeholder={`# ${selectedFile}\n\nStart writing...`}
                />
              )}

              <div className="flex items-center justify-between mt-3">
                <span className={`text-xs text-green-400 transition-opacity ${saved ? 'opacity-100' : 'opacity-0'}`}>
                  Saved
                </span>
                <button
                  onClick={handleSaveWorkspace}
                  disabled={saving || fileLoading}
                  className="px-4 py-2 rounded-lg bg-[var(--accent)] text-white text-sm font-medium hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed transition-opacity"
                >
                  {saving ? 'Saving...' : 'Save'}
                </button>
              </div>
            </Card>
          )}

          {/* Skills tab */}
          {activeTab === 'skills' && (
            <Card className="p-6">
              <h3 className="text-base font-semibold text-[var(--text)] mb-4">Skills</h3>
              {skills.length === 0 ? (
                <p className="text-sm text-[var(--text-secondary)]">No skills available.</p>
              ) : (
                <div className="space-y-2">
                  {skills.map(skill => (
                    <div
                      key={skill.name}
                      className="flex items-start justify-between py-3 border-b border-[var(--border)] last:border-0"
                    >
                      <div className="flex-1 min-w-0 pr-4">
                        <p className="text-sm font-medium text-[var(--text)]">{skill.name}</p>
                        {skill.description && (
                          <p className="text-xs text-[var(--text-tertiary)] mt-0.5">{skill.description}</p>
                        )}
                      </div>
                      <span
                        className={`shrink-0 text-xs font-medium px-2 py-0.5 rounded-full ${
                          skill.enabled
                            ? 'bg-green-500/10 text-green-500'
                            : 'bg-[var(--bg-hover)] text-[var(--text-tertiary)]'
                        }`}
                      >
                        {skill.enabled ? 'Active' : 'Inactive'}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </Card>
          )}

          {/* Access tab */}
          {activeTab === 'access' && (
            <Card className="p-6">
              <h3 className="text-base font-semibold text-[var(--text)] mb-4">Access</h3>
              <div className="space-y-4">
                {[
                  {
                    label: 'SSH',
                    field: 'ssh',
                    value: `ssh user@${host}`,
                    hint: 'Direct SSH access to the host',
                  },
                  {
                    label: 'TUI Command',
                    field: 'tui',
                    value: `openclaw tui --host ${origin}`,
                    hint: 'Run the OpenClaw terminal interface',
                  },
                  {
                    label: 'API Endpoint',
                    field: 'api',
                    value: `${origin}/v1`,
                    hint: 'OpenAI-compatible inference API',
                  },
                  {
                    label: 'OpenClaw UI',
                    field: 'ui',
                    value: `${origin}/api/v1/openclaw/ui`,
                    hint: 'OpenClaw web control panel',
                  },
                ].map(item => (
                  <div key={item.field}>
                    <div className="flex items-center justify-between mb-1">
                      <span className="text-xs font-medium text-[var(--text-tertiary)] uppercase tracking-wider">
                        {item.label}
                      </span>
                      <button
                        onClick={() => copyToClipboard(item.value, item.field)}
                        className="text-xs text-[var(--accent)] hover:underline"
                      >
                        {copiedField === item.field ? 'Copied!' : 'Copy'}
                      </button>
                    </div>
                    <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-[var(--bg)] border border-[var(--border)]">
                      <span className="text-sm font-mono text-[var(--text)] truncate flex-1">{item.value}</span>
                    </div>
                    {item.hint && (
                      <p className="text-xs text-[var(--text-tertiary)] mt-1">{item.hint}</p>
                    )}
                  </div>
                ))}
              </div>
            </Card>
          )}
        </>
      )}
    </main>
  )
}
