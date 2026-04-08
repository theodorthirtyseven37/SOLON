import type { ProviderName } from './providers/types'

export interface Env {
  // Provider API tokens (at least one required)
  HETZNER_API_TOKEN?: string
  VERDA_API_TOKEN?: string
  SCALEWAY_API_TOKEN?: string
  SCALEWAY_PROJECT_ID?: string
  // Core config
  PROVISIONER_SECRET: string
  CLOUD_API_URL: string
  CLOUD_API_CALLBACK_SECRET: string
  SSH_PUBLIC_KEY?: string
  ENVIRONMENT: string
}

export interface ProvisionRequest {
  action: 'create' | 'delete'
  instance_id: string
  /** GPU tier ID from GPU_TIERS (e.g. 'verda-h100', 'scaleway-l40s', 'hetzner-rtx4000') */
  gpu_tier?: string
  /** Override region (defaults to tier's default region) */
  region?: string
  name?: string
  // Legacy fields (backwards compat with existing cloud API)
  tier?: string
}

export interface CallbackPayload {
  instance_id: string
  status: 'running' | 'failed' | 'deleted'
  provider?: ProviderName
  gpu_tier?: string
  ipv4?: string
  solon_api_key?: string
  dashboard_url?: string
  error?: string
}

// Legacy tier mappings — kept for backwards compatibility with existing Hetzner-only flows.
// New provisioning should use gpu_tier instead.
export const LEGACY_TIER_MAP: Record<string, string> = {
  starter: 'hetzner-rtx4000', // map old "starter" to cheapest GPU
  pro: 'hetzner-rtx4000',
  gpu: 'hetzner-rtx4000',
}

/** Maps legacy region names to Hetzner locations */
export const LEGACY_REGION_MAP: Record<string, string> = {
  'eu-central': 'fsn1',
  'eu-west': 'nbg1',
  'eu-north': 'hel1',
  'us-east': 'ash',
}
