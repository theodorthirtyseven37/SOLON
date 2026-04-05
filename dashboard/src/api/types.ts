// Solon instance types

export interface HealthStatus {
  status: string
  version: string
}

export interface SystemInfo {
  total_memory_mb: number
}

export interface ModelInfo {
  name: string
  size: number
  format: string
  family: string
  params: string
  quantization: string
  modified: string
}

export interface APIKey {
  id: string
  name: string
  prefix: string
  scope: string
  rate_limit: number
  created_at: string
  last_used?: string
  tunnel_access: boolean
  expires_at?: string
  allowed_models?: string[]
}

export interface RequestLogEntry {
  id: number
  key_id: string
  method: string
  path: string
  model: string
  tokens_in: number
  tokens_out: number
  latency_ms: number
  status_code: number
  created_at: string
}

export interface UsageStats {
  total_requests: number
  total_tokens_in: number
  total_tokens_out: number
  avg_latency_ms: number
  requests_today: number
  unique_keys_used: number
  most_used_model: string
}

export interface KeyUsage {
  request_count: number
  total_tokens: number
}

export interface TunnelStatus {
  enabled: boolean
  url?: string
  provider?: string
  persistent?: boolean
}

export interface RemoteStatus {
  enabled: boolean
  url?: string
  instance_id?: string
  provider?: string
}

export interface CatalogModel {
  name: string
  description: string
  creator: string
  sizes: string[]
  category: 'chat' | 'code' | 'embedding'
  capabilities: string[]
  context: number
  vram: Record<string, number>
  sources: Record<string, { repo: string; file: string }>
}

// InstanceAPI — common interface for local instance client

export interface InstanceAPI {
  health: () => Promise<HealthStatus>
  system: () => Promise<SystemInfo>
  models: () => Promise<ModelInfo[]>
  deleteModel: (name: string) => Promise<{ status: string }>
  keys: {
    list: () => Promise<APIKey[]>
    create: (opts: CreateKeyOptions) => Promise<{ key: string; name: string; id: string }>
    revoke: (id: string) => Promise<{ status: string }>
  }
  analytics: {
    requests: () => Promise<RequestLogEntry[]>
    usage: () => Promise<UsageStats>
    usageByKey: () => Promise<Record<string, KeyUsage>>
  }
  tunnel: {
    status: () => Promise<TunnelStatus>
    enable: () => Promise<TunnelStatus>
    disable: () => Promise<{ status: string }>
  }
  remote: {
    status: () => Promise<RemoteStatus>
  }
  catalog: () => Promise<CatalogModel[]>
}

export interface CreateKeyOptions {
  name: string
  scope?: string
  rate_limit?: number
  ttl_seconds?: number
  allowed_models?: string[]
  tunnel_access?: boolean
}

// Provider types

export interface ProviderConfig {
  id: string
  name: string
  base_url: string
  api_key: string   // Masked (last 4 chars)
  enabled: boolean
  created_at: string
}

// Sandbox types

export interface SandboxInfo {
  id: string
  name: string
  container_id?: string
  status: string    // "created" | "running" | "stopped" | "failed"
  policy: string
  tier: number      // 1=locked, 2=standard, 3=advanced, 4=maximum
  api_key_id?: string
  config?: SandboxConfig
  created_at: string
  started_at?: string
  stopped_at?: string
}

export interface SandboxConfig {
  env?: Record<string, string>
  image?: string
  memory?: number
  tier?: number
}

export interface SandboxTier {
  level: number
  name: string
  description: string
  memory_mb: number
  allow_exec: boolean
  allow_browser: boolean
  persistent: boolean
}

export interface SandboxPreset {
  name: string
  description: string
  allowed_hosts?: string[]
}

export interface SandboxStats {
  cpu_percent: number
  mem_usage_mb: number
  mem_limit_mb: number
  mem_percent: number
  net_rx_mb: number
  net_tx_mb: number
}

// Cloud API types

export interface User {
  id: string
  name: string
  email: string
  plan: 'free' | 'pro' | 'team' | 'enterprise'
  avatar_url: string | null
  role: 'admin' | 'user' | 'waitlisted'
  provider: 'github' | 'google' | null
  created_at: string
}

export interface AdminUser {
  id: string
  name: string
  email: string
  avatar_url: string | null
  role: 'admin' | 'user' | 'waitlisted'
  provider: 'github' | 'google' | null
  created_at: string
}

export interface AuthResponse {
  token: string
  user: User
}

export interface Instance {
  id: string
  name: string
  url: string
  api_key: string
  status: 'online' | 'offline' | 'unknown'
  version?: string
  models_count?: number
  added_at: string
}

export interface ManagedInstance {
  id: string
  name: string
  tier: 'starter' | 'pro' | 'gpu'
  status: 'pending' | 'provisioning' | 'running' | 'suspended' | 'deleting' | 'deleted' | 'failed'
  ipv4: string | null
  region: string
  dashboard_url: string | null
  created_at: string
  ready_at: string | null
}

export interface BillingInfo {
  plan: 'free' | 'pro' | 'team' | 'enterprise'
  status: 'active' | 'past_due' | 'canceled'
  current_period_end: string
  usage: {
    instances: { used: number; limit: number }
    requests: { used: number; limit: number }
    team_members: { used: number; limit: number }
  }
  managed_instances: ManagedInstance[]
  payment_method?: {
    type: string
    last4: string
    exp: string
  }
}

export interface TeamMember {
  id: string
  name: string
  email: string
  role: 'owner' | 'admin' | 'member'
  joined_at: string
  last_active?: string
}

export interface CloudAPIToken {
  id: string
  name: string
  prefix: string
  created_at: string
  last_used?: string
}

// Telegram integration types

export interface TelegramIntegration {
  id: string
  sandbox_id: string
  bot_username?: string
  status: 'connected' | 'disconnected' | 'error'
  error_msg?: string
  created_at: string
  updated_at: string
}

// Download/pull types

export interface DownloadProgress {
  event: string      // "start" | "progress" | "done" | "error"
  file: string
  downloaded: number
  total: number
  percent: number
  message: string
}
