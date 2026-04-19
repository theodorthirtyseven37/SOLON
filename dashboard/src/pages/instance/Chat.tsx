import { useState, useEffect, useRef, useCallback } from 'react'
import { errorFromResponse, fetchJSON, getLocalApiKey, isNonLocalhost } from '../../api/client'
import type { ModelInfo } from '../../api/types'

function authHeaders(): Record<string, string> {
  const apiKey = isNonLocalhost() ? getLocalApiKey() : null
  return apiKey ? { Authorization: `Bearer ${apiKey}` } : {}
}

interface ChatMessage {
  id: string
  role: 'user' | 'assistant' | 'system'
  content: string
  timestamp: number
  isStreaming?: boolean
}

interface SessionInfo {
  key: string
  sessionId: string
  model: string
  modelProvider: string
  totalTokens: number
  updatedAt: number
}

type ConnectionMode = 'agent' | 'direct' | 'connecting' | 'disconnected'

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

export default function Chat() {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [mode, setMode] = useState<ConnectionMode>('connecting')
  const [error, setError] = useState('')
  const [models, setModels] = useState<ModelInfo[]>([])
  const [selectedModel, setSelectedModel] = useState('')
  const [sending, setSending] = useState(false)
  const [starting, setStarting] = useState(false)
  const [dockerAvailable, setDockerAvailable] = useState(true)
  const [sessions, setSessions] = useState<SessionInfo[]>([])
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [activeSession, setActiveSession] = useState<string | null>(null)
  const [agentList, setAgentList] = useState<{ name: string }[]>([])
  const [selectedAgent, setSelectedAgent] = useState('main')
  const [thinkingLevel, setThinkingLevel] = useState<'off' | 'low' | 'medium' | 'high'>('medium')
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => { messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [messages])
  useEffect(() => { if (mode === 'agent' || mode === 'direct') inputRef.current?.focus() }, [mode])

  // On mount: detect if OpenClaw agent is running
  const connect = useCallback(async () => {
    setMode('connecting')
    setError('')

    try {
      const status = await fetchJSON<{ available: boolean; running: boolean }>('/api/v1/openclaw/status')
      setDockerAvailable(status.available !== false)
      if (status.running) {
        setMode('agent')
        return
      }
    } catch { /* fall through to direct mode */ }

    // Fallback: direct SSE mode with model selector
    try {
      const modelList = await fetchJSON<{ models: ModelInfo[] }>('/api/v1/models').then(r => r.models || [])
      setModels(modelList)
      if (modelList.length > 0 && !selectedModel) setSelectedModel(modelList[0].name)
      setMode(modelList.length > 0 ? 'direct' : 'disconnected')
    } catch {
      setMode('disconnected')
    }
  }, [selectedModel])

  const loadSessions = useCallback(async () => {
    try {
      const d = await fetchJSON<{ sessions?: SessionInfo[] }>('/api/v1/openclaw/sessions')
      setSessions(d.sessions || [])
    } catch { /* */ }
  }, [])

  async function handleStartAgent() {
    setStarting(true)
    setError('')
    try {
      await fetch('/api/v1/openclaw/start', { method: 'POST', headers: authHeaders() }).then(async r => {
        if (!r.ok) throw await errorFromResponse(r)
      })
      // Wait briefly for container to be ready, then reconnect
      await new Promise(resolve => setTimeout(resolve, 3000))
      await connect()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setStarting(false)
    }
  }

  useEffect(() => {
    connect()
    loadSessions()
    fetchJSON<{ agents?: { name: string }[] }>('/api/v1/openclaw/agents')
      .then(d => setAgentList(d.agents || []))
      .catch(() => {})
  }, [])

  async function handleSend() {
    const text = input.trim()
    if (!text || sending) return
    setInput('')
    setMessages(prev => [...prev, { id: Math.random().toString(36).slice(2) + Date.now().toString(36), role: 'user', content: text, timestamp: Date.now() }])

    if (mode === 'agent') {
      await sendViaAgent(text)
    } else {
      await sendViaSSE(text)
    }
  }

  async function sendViaAgent(text: string) {
    setSending(true)
    const id = Math.random().toString(36).slice(2) + Date.now().toString(36)
    setMessages(prev => [...prev, { id, role: 'assistant', content: '', timestamp: Date.now(), isStreaming: true }])

    try {
      const resp = await fetch('/api/v1/openclaw/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ message: text, agent: selectedAgent, thinking: thinkingLevel }),
      })
      if (!resp.ok) throw await errorFromResponse(resp)

      const contentType = resp.headers.get('content-type') ?? ''
      if (contentType.includes('text/event-stream') && resp.body) {
        // Streaming SSE response from the agent bridge
        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        let accumulated = ''
        let buffer = ''

        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const events = buffer.split('\n\n')
          buffer = events.pop() ?? ''
          for (const event of events) {
            const lines = event.split('\n')
            let eventType = 'message'
            let data = ''
            for (const line of lines) {
              if (line.startsWith('event: ')) eventType = line.slice(7)
              else if (line.startsWith('data: ')) data += line.slice(6)
            }
            if (eventType === 'done') break
            if (eventType === 'error') {
              const parsed = JSON.parse(data) as { error?: string }
              if (parsed.error) accumulated += `\n[error: ${parsed.error}]`
            } else if (data) {
              // Try to parse JSON output from openclaw agent
              try {
                const parsed = JSON.parse(data) as Record<string, unknown>
                const result = parsed.result as Record<string, unknown> | undefined
                const payloads = result?.payloads as { text?: string }[] | undefined
                const chunk = payloads?.[0]?.text || (parsed.reply as string) || (parsed.content as string) || data
                accumulated += chunk
              } catch {
                accumulated += data
              }
            }
            setMessages(prev => prev.map(m => m.id === id ? { ...m, content: accumulated } : m))
          }
        }
        setMessages(prev => prev.map(m => m.id === id ? { ...m, content: accumulated, isStreaming: false } : m))
      } else {
        // Fallback: non-streaming JSON response
        const data = await resp.json() as Record<string, unknown>
        const result = data.result as Record<string, unknown> | undefined
        const payloads = result?.payloads as { text?: string }[] | undefined
        const content = payloads?.[0]?.text || (data.reply as string) || (data.content as string) || JSON.stringify(data)
        setMessages(prev => prev.map(m => m.id === id ? { ...m, content, isStreaming: false } : m))
      }
    } catch (e) {
      setError((e as Error).message)
      setMessages(prev => prev.filter(m => m.id !== id || m.content !== ''))
    } finally {
      setSending(false)
    }
  }

  async function sendViaSSE(text: string) {
    if (!selectedModel) { setError('No model selected'); return }
    setSending(true)
    const id = Math.random().toString(36).slice(2) + Date.now().toString(36)
    setMessages(prev => [...prev, { id, role: 'assistant', content: '', timestamp: Date.now(), isStreaming: true }])

    try {
      const allMsgs = messages.filter(m => m.role !== 'system').map(m => ({ role: m.role, content: m.content }))
      allMsgs.push({ role: 'user', content: text })

      const response = await fetch('/v1/chat/completions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ model: selectedModel, messages: allMsgs, stream: true }),
      })
      if (!response.ok) throw await errorFromResponse(response)

      const reader = response.body?.getReader()
      if (!reader) throw new Error('No response body')
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
          if (!trimmed || !trimmed.startsWith('data: ')) continue
          const payload = trimmed.slice(6)
          if (payload === '[DONE]') break
          try {
            const chunk = JSON.parse(payload) as { choices?: { delta?: { content?: string } }[] }
            const delta = chunk.choices?.[0]?.delta?.content
            if (delta) setMessages(prev => prev.map(m => m.id === id ? { ...m, content: m.content + delta } : m))
          } catch { /* skip */ }
        }
      }
      setMessages(prev => prev.map(m => m.id === id ? { ...m, isStreaming: false } : m))
    } catch (e) {
      setError((e as Error).message)
      setMessages(prev => prev.filter(m => m.id !== id || m.content !== ''))
    } finally {
      setSending(false)
    }
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend() }
  }

  const modeLabel = { agent: 'Agent', direct: 'Direct', connecting: 'Connecting...', disconnected: 'Disconnected' }[mode]
  const modeColor = { agent: 'text-green-400', direct: 'text-blue-400', connecting: 'text-yellow-400', disconnected: 'text-red-400' }[mode]
  const dotColor = { agent: 'bg-green-400', direct: 'bg-blue-400', connecting: 'bg-yellow-400 animate-pulse', disconnected: 'bg-red-400' }[mode]

  return (
    <div className="flex h-[calc(100vh-3.5rem)]">
      {/* Sidebar — only in agent mode */}
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
                  onClick={() => {
                    setActiveSession(s.sessionId)
                    setMessages([{
                      id: Math.random().toString(36).slice(2) + Date.now().toString(36),
                      role: 'system',
                      content: `Resumed session: ${s.key.replace('agent:main:', '') || s.sessionId}\nModel: ${s.model} · ${formatTokens(s.totalTokens)} tokens\n\nPrevious messages are not loaded yet — new messages will continue this session.`,
                      timestamp: Date.now(),
                    }])
                  }}
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
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-2 border-b border-[var(--border)] bg-[var(--bg-card)]">
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
            {mode === 'direct' && models.length > 0 && (
              <select value={selectedModel} onChange={e => setSelectedModel(e.target.value)}
                className="text-xs px-2 py-1 rounded border border-[var(--border)] bg-[var(--bg)] text-[var(--text)]">
                {models.map(m => <option key={m.name} value={m.name}>{m.name}</option>)}
              </select>
            )}

            {/* Agent selector — only in agent mode with multiple agents */}
            {mode === 'agent' && agentList.length > 1 && (
              <select value={selectedAgent} onChange={e => setSelectedAgent(e.target.value)}
                className="text-xs px-2 py-1 rounded border border-[var(--border)] bg-[var(--bg)] text-[var(--text)]">
                {agentList.map(a => <option key={a.name} value={a.name}>{a.name}</option>)}
              </select>
            )}

            {/* Thinking level — only in agent mode */}
            {mode === 'agent' && (
              <select value={thinkingLevel} onChange={e => setThinkingLevel(e.target.value as 'off' | 'low' | 'medium' | 'high')}
                className="text-xs px-2 py-1 rounded border border-[var(--border)] bg-[var(--bg)] text-[var(--text)]">
                <option value="off">Thinking: Off</option>
                <option value="low">Thinking: Low</option>
                <option value="medium">Thinking: Medium</option>
                <option value="high">Thinking: High</option>
              </select>
            )}
          </div>
          <div className="flex items-center gap-2">
            {mode !== 'agent' && dockerAvailable && !starting && (
              <button onClick={handleStartAgent}
                className="text-xs px-3 py-1 rounded-md bg-green-600 text-white hover:bg-green-500 transition-colors">
                Start Agent
              </button>
            )}
            {starting && <span className="text-xs text-yellow-400 animate-pulse">Starting agent...</span>}
            {mode === 'disconnected' && <button onClick={connect} className="text-xs text-[var(--accent)] hover:underline">Reconnect</button>}
            {messages.length > 0 && <button onClick={() => setMessages([])} className="text-xs text-[var(--text-tertiary)] hover:text-[var(--text)]">Clear</button>}
          </div>
        </div>

        {error && (
          <div className="px-4 py-2 bg-red-500/10 text-red-400 text-xs flex items-center justify-between">
            <span>{error}</span>
            <button onClick={() => setError('')} className="underline">dismiss</button>
          </div>
        )}

        {/* Messages */}
        <div className="flex-1 overflow-y-auto px-4 py-6 space-y-4">
          {messages.length === 0 ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center max-w-sm">
                <p className="text-4xl mb-4">&#x1F99E;</p>
                <p className="text-[var(--text-secondary)] text-sm">
                  {mode === 'agent' ? 'Connected to OpenClaw agent. Type a message to start.' :
                   starting ? 'Setting up your agent environment...' :
                   'Type a message to chat with your AI model.'}
                </p>
                {mode !== 'agent' && !starting && dockerAvailable && (
                  <button onClick={handleStartAgent}
                    className="mt-3 px-4 py-2 rounded-lg text-sm font-medium bg-green-600 text-white hover:bg-green-500 transition-colors">
                    Launch OpenClaw Agent
                  </button>
                )}
                {mode !== 'agent' && !starting && dockerAvailable && (
                  <p className="text-xs text-[var(--text-tertiary)] mt-2">Full agent with tools, code execution, and web browsing.</p>
                )}
                {!dockerAvailable && mode !== 'agent' && (
                  <p className="text-xs text-[var(--text-tertiary)] mt-2">Install Docker to enable the full agent experience.</p>
                )}
                {starting && (
                  <div className="mt-3 flex items-center gap-2 text-yellow-400 text-sm">
                    <span className="w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />
                    Pulling image and starting container... This may take a minute on first run.
                  </div>
                )}
              </div>
            </div>
          ) : (
            messages.map(msg => (
              <div key={msg.id} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                <div className={`max-w-[80%] rounded-xl px-4 py-2.5 text-sm whitespace-pre-wrap ${
                  msg.role === 'user' ? 'bg-[var(--accent)] text-white rounded-br-sm' : 'bg-[var(--bg-card)] border border-[var(--border)] text-[var(--text)] rounded-bl-sm'
                }`}>
                  {msg.content || (msg.isStreaming ? (
                    <span className="inline-flex gap-1">
                      <span className="w-1.5 h-1.5 rounded-full bg-[var(--text-tertiary)] animate-bounce" style={{ animationDelay: '0ms' }} />
                      <span className="w-1.5 h-1.5 rounded-full bg-[var(--text-tertiary)] animate-bounce" style={{ animationDelay: '150ms' }} />
                      <span className="w-1.5 h-1.5 rounded-full bg-[var(--text-tertiary)] animate-bounce" style={{ animationDelay: '300ms' }} />
                    </span>
                  ) : '')}
                </div>
              </div>
            ))
          )}
          <div ref={messagesEndRef} />
        </div>

        {/* Input */}
        <div className="border-t border-[var(--border)] bg-[var(--bg-card)] px-4 py-3">
          <div className="flex gap-2 max-w-3xl mx-auto">
            <textarea ref={inputRef} value={input} onChange={e => setInput(e.target.value)} onKeyDown={handleKeyDown}
              placeholder={mode === 'agent' ? 'Message your agent...' : 'Message...'} rows={1}
              className="flex-1 px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-[var(--text)] text-sm resize-none focus:outline-none focus:border-[var(--accent)]"
              disabled={mode === 'connecting' || mode === 'disconnected'}
              style={{ minHeight: '40px', maxHeight: '120px' }} />
            <button onClick={handleSend} disabled={!input.trim() || sending || mode === 'connecting' || mode === 'disconnected'}
              className="px-4 py-2 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity disabled:opacity-30">
              {sending ? '...' : 'Send'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
