import { useState } from 'react'

interface Props {
  isOpen: boolean
  onClose: () => void
  serverHost?: string
}

function CopyBlock({ label, command }: { label: string; command: string }) {
  const [copied, setCopied] = useState(false)

  function handleCopy() {
    navigator.clipboard.writeText(command).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  return (
    <div>
      <p className="text-xs text-[var(--text-tertiary)] mb-1">{label}</p>
      <div className="flex items-center gap-2">
        <code className="flex-1 text-xs bg-[var(--bg)] px-3 py-2 rounded-lg font-mono text-[var(--text)] overflow-x-auto">
          {command}
        </code>
        <button onClick={handleCopy}
          className="px-2 py-2 text-xs rounded-lg border border-[var(--border)] hover:bg-[var(--bg-hover)] text-[var(--text-tertiary)] shrink-0">
          {copied ? '✓' : '📋'}
        </button>
      </div>
    </div>
  )
}

export default function TerminalAccess({ isOpen, onClose, serverHost }: Props) {
  if (!isOpen) return null

  const host = serverHost || window.location.hostname

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div className="bg-[var(--bg-card)] rounded-xl border border-[var(--border)] p-6 max-w-lg w-full mx-4 space-y-4"
        onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-[var(--text)]">Terminal Access</h2>
          <button onClick={onClose} className="text-[var(--text-tertiary)] hover:text-[var(--text)]">✕</button>
        </div>

        <CopyBlock
          label="1. SSH into your server"
          command={`ssh root@${host}`}
        />

        <CopyBlock
          label="2. Open the OpenClaw TUI"
          command="docker exec -it openclaw-sandbox-openclaw openclaw tui"
        />

        <CopyBlock
          label="3. Or send a one-off message"
          command='docker exec openclaw-sandbox-openclaw openclaw agent --agent main --message "hello"'
        />

        <p className="text-xs text-[var(--text-tertiary)]">
          The TUI gives you a full terminal interface to your agent with autocomplete, history, and streaming responses.
        </p>
      </div>
    </div>
  )
}
