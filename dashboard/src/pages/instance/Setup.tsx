import { useState } from 'react'
import type { ProviderConfig, SandboxPreset } from '../../api/types'
import { providerAPI, sandboxAPI } from '../../api/local'
import { errorFromResponse } from '../../api/client'

const STEPS = ['welcome', 'provider', 'test', 'sandbox', 'done'] as const
type Step = (typeof STEPS)[number]

const WELL_KNOWN: Record<string, { label: string; placeholder: string; models: string[] }> = {
  anthropic: {
    label: 'Anthropic',
    placeholder: 'sk-ant-...',
    models: ['claude-opus-4', 'claude-sonnet-4', 'claude-haiku-4.5'],
  },
  openai: {
    label: 'OpenAI',
    placeholder: 'sk-...',
    models: ['gpt-4o', 'gpt-4o-mini'],
  },
}

interface SetupProps {
  onComplete: () => void
}

export default function Setup({ onComplete }: SetupProps) {
  const [step, setStep] = useState<Step>('welcome')
  const [provider, setProvider] = useState('anthropic')
  const [apiKey, setApiKey] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [testResult, setTestResult] = useState<string | null>(null)
  const [sandboxName, setSandboxName] = useState('my-agent')
  const [sandboxPolicy, setSandboxPolicy] = useState('api-only')
  const [presets, setPresets] = useState<SandboxPreset[]>([])
  const [createdSandboxId, setCreatedSandboxId] = useState<string | null>(null)

  const currentIndex = STEPS.indexOf(step)
  const progress = ((currentIndex + 1) / STEPS.length) * 100

  const handleAddProvider = async () => {
    if (!apiKey.trim()) {
      setError('API key is required')
      return
    }
    setLoading(true)
    setError('')
    try {
      await providerAPI.add(provider, apiKey.trim())
      setStep('test')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const handleTest = async () => {
    setLoading(true)
    setError('')
    setTestResult(null)
    try {
      // Make a minimal chat completion to verify the provider works
      const resp = await fetch('/v1/chat/completions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          model: `${provider}/claude-sonnet-4-20250514`,
          messages: [{ role: 'user', content: 'Say "hello" in one word.' }],
          max_tokens: 10,
        }),
      })
      if (!resp.ok) throw await errorFromResponse(resp)
      const data = await resp.json() as { choices?: { message?: { content?: string } }[] }
      const reply = data.choices?.[0]?.message?.content || 'OK'
      setTestResult(reply)

      // Load presets for next step
      sandboxAPI.presets().then(setPresets).catch(() => {})
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const handleCreateSandbox = async () => {
    if (!sandboxName.trim()) {
      setError('Name is required')
      return
    }
    setLoading(true)
    setError('')
    try {
      const policyToTier: Record<string, number> = { 'inference-only': 1, 'api-only': 2, 'full': 3 }
      const sb = await sandboxAPI.create(sandboxName.trim(), policyToTier[sandboxPolicy] || 2)
      await sandboxAPI.start(sb.id)
      setCreatedSandboxId(sb.id)
      setStep('done')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <main className="max-w-lg mx-auto px-4 sm:px-6 py-12">
      {/* Progress bar */}
      <div className="mb-8">
        <div className="h-1 rounded-full bg-[var(--border)] overflow-hidden">
          <div
            className="h-full rounded-full bg-[var(--accent)] transition-all duration-500"
            style={{ width: `${progress}%` }}
          />
        </div>
        <p className="text-xs text-[var(--text-tertiary)] mt-2">
          Step {currentIndex + 1} of {STEPS.length}
        </p>
      </div>

      {/* Step: Welcome */}
      {step === 'welcome' && (
        <div className="space-y-6">
          <div>
            <h1 className="text-2xl font-bold text-[var(--text)]">Welcome to Solon</h1>
            <p className="mt-2 text-[var(--text-secondary)]">
              Your server is ready. Let's set it up in a few steps — add an AI provider,
              verify it works, and create your first secure sandbox.
            </p>
          </div>

          <div className="space-y-3">
            <Feature title="Any model" desc="Use Claude, GPT, open source — your choice" />
            <Feature title="Secure sandboxes" desc="Run agents in isolated containers with network policies" />
            <Feature title="Full control" desc="Your server, your data, your rules" />
          </div>

          <button
            onClick={() => setStep('provider')}
            className="w-full px-4 py-3 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity"
          >
            Let's go
          </button>
        </div>
      )}

      {/* Step: Add Provider */}
      {step === 'provider' && (
        <div className="space-y-6">
          <div>
            <h1 className="text-xl font-bold text-[var(--text)]">Add an AI Provider</h1>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">
              Connect your AI provider so Solon can route inference requests.
            </p>
          </div>

          <div className="space-y-4">
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1.5">Provider</label>
              <div className="grid grid-cols-2 gap-2">
                {Object.entries(WELL_KNOWN).map(([key, info]) => (
                  <button
                    key={key}
                    onClick={() => setProvider(key)}
                    className={`p-3 rounded-lg border text-left transition-colors ${
                      provider === key
                        ? 'border-[var(--accent)] bg-[var(--accent)]/5'
                        : 'border-[var(--border)] hover:border-[var(--text-tertiary)]'
                    }`}
                  >
                    <div className="text-sm font-medium text-[var(--text)]">{info.label}</div>
                    <div className="text-xs text-[var(--text-tertiary)] mt-0.5">
                      {info.models.join(', ')}
                    </div>
                  </button>
                ))}
              </div>
            </div>

            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1.5">API Key</label>
              <input
                type="password"
                value={apiKey}
                onChange={e => setApiKey(e.target.value)}
                placeholder={WELL_KNOWN[provider]?.placeholder}
                className="w-full px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-[var(--text)] text-sm font-mono"
                onKeyDown={e => e.key === 'Enter' && handleAddProvider()}
              />
            </div>

            {error && <p className="text-sm text-red-400">{error}</p>}

            <div className="flex gap-3">
              <button
                onClick={() => setStep('welcome')}
                className="flex-1 px-4 py-2.5 rounded-lg text-sm border border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              >
                Back
              </button>
              <button
                onClick={handleAddProvider}
                disabled={loading}
                className="flex-1 px-4 py-2.5 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 disabled:opacity-50"
              >
                {loading ? 'Adding...' : 'Add Provider'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Step: Test */}
      {step === 'test' && (
        <div className="space-y-6">
          <div>
            <h1 className="text-xl font-bold text-[var(--text)]">Test Connection</h1>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">
              Let's verify that {WELL_KNOWN[provider]?.label || provider} is working through Solon's guardrails.
            </p>
          </div>

          {testResult ? (
            <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-4">
              <p className="text-sm font-medium text-green-400">Connection successful</p>
              <p className="text-sm text-[var(--text-secondary)] mt-1 font-mono">"{testResult}"</p>
            </div>
          ) : error ? (
            <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-4">
              <p className="text-sm font-medium text-red-400">Connection failed</p>
              <p className="text-sm text-[var(--text-secondary)] mt-1">{error}</p>
            </div>
          ) : null}

          <div className="flex gap-3">
            <button
              onClick={() => { setError(''); setStep('provider') }}
              className="flex-1 px-4 py-2.5 rounded-lg text-sm border border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
            >
              Back
            </button>
            {testResult ? (
              <button
                onClick={() => setStep('sandbox')}
                className="flex-1 px-4 py-2.5 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90"
              >
                Next
              </button>
            ) : (
              <button
                onClick={handleTest}
                disabled={loading}
                className="flex-1 px-4 py-2.5 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 disabled:opacity-50"
              >
                {loading ? 'Testing...' : 'Send Test Prompt'}
              </button>
            )}
          </div>
        </div>
      )}

      {/* Step: Create Sandbox */}
      {step === 'sandbox' && (
        <div className="space-y-6">
          <div>
            <h1 className="text-xl font-bold text-[var(--text)]">Create a Sandbox</h1>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">
              Sandboxes run OpenClaw agents in isolated containers. Choose a network policy to control what your agent can access.
            </p>
          </div>

          <div className="space-y-4">
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1.5">Sandbox Name</label>
              <input
                type="text"
                value={sandboxName}
                onChange={e => setSandboxName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))}
                placeholder="my-agent"
                className="w-full px-3 py-2 rounded-lg border border-[var(--border)] bg-[var(--bg)] text-[var(--text)] text-sm font-mono"
              />
            </div>

            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1.5">Network Policy</label>
              <div className="space-y-2">
                {(presets.length > 0 ? presets : [
                  { name: 'api-only', description: 'HTTPS access to known AI providers, npm, and PyPI only' },
                  { name: 'inference-only', description: 'Only Solon inference — no internet access' },
                  { name: 'full', description: 'Unrestricted network access' },
                ]).filter(p => p.name !== 'custom').map(p => (
                  <label
                    key={p.name}
                    className={`flex items-center gap-3 p-3 rounded-lg border cursor-pointer transition-colors ${
                      sandboxPolicy === p.name
                        ? 'border-[var(--accent)] bg-[var(--accent)]/5'
                        : 'border-[var(--border)] hover:border-[var(--text-tertiary)]'
                    }`}
                  >
                    <input
                      type="radio"
                      name="policy"
                      value={p.name}
                      checked={sandboxPolicy === p.name}
                      onChange={() => setSandboxPolicy(p.name)}
                    />
                    <div>
                      <div className="text-sm font-medium text-[var(--text)]">{p.name}</div>
                      <div className="text-xs text-[var(--text-tertiary)]">{p.description}</div>
                    </div>
                  </label>
                ))}
              </div>
            </div>

            {error && <p className="text-sm text-red-400">{error}</p>}

            <div className="flex gap-3">
              <button
                onClick={() => { setError(''); setStep('test') }}
                className="flex-1 px-4 py-2.5 rounded-lg text-sm border border-[var(--border)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              >
                Back
              </button>
              <button
                onClick={handleCreateSandbox}
                disabled={loading}
                className="flex-1 px-4 py-2.5 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 disabled:opacity-50"
              >
                {loading ? 'Creating...' : 'Create & Start'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Step: Done */}
      {step === 'done' && (
        <div className="space-y-6 text-center">
          <div className="text-4xl">&#x2713;</div>
          <div>
            <h1 className="text-2xl font-bold text-[var(--text)]">You're all set</h1>
            <p className="mt-2 text-[var(--text-secondary)]">
              Solon is running with {WELL_KNOWN[provider]?.label || provider} and your sandbox "{sandboxName}" is active.
            </p>
          </div>

          <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-card)] p-4 text-left space-y-3">
            <div>
              <p className="text-xs text-[var(--text-tertiary)]">Provider</p>
              <p className="text-sm font-medium text-[var(--text)]">{WELL_KNOWN[provider]?.label || provider}</p>
            </div>
            <div>
              <p className="text-xs text-[var(--text-tertiary)]">Sandbox</p>
              <p className="text-sm font-medium text-[var(--text)]">{sandboxName} ({sandboxPolicy})</p>
            </div>
          </div>

          <button
            onClick={onComplete}
            className="w-full px-4 py-3 rounded-lg text-sm font-medium bg-[var(--accent)] text-white hover:opacity-90 transition-opacity"
          >
            Go to Dashboard
          </button>
        </div>
      )}
    </main>
  )
}

function Feature({ title, desc }: { title: string; desc: string }) {
  return (
    <div className="flex items-start gap-3 p-3 rounded-lg bg-[var(--bg-card)] border border-[var(--border)]">
      <span className="text-[var(--accent)] mt-0.5">&#x2022;</span>
      <div>
        <p className="text-sm font-medium text-[var(--text)]">{title}</p>
        <p className="text-xs text-[var(--text-tertiary)]">{desc}</p>
      </div>
    </div>
  )
}
