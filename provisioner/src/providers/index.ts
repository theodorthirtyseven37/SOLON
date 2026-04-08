export type { CloudProvider, CreateServerOpts, ProviderServerResult, ProviderServerInfo } from './types'
export type { ProviderName, GpuVendor, GpuTier, GpuSpec, RegionInfo } from './types'
export { GPU_SPECS, GPU_TIERS, REGIONS, getTierById, getTiersForProvider, getTotalVram, getGpuVendor, getGpuSpec } from './types'

export { HetznerProvider } from './hetzner'
export { VerdaProvider } from './verda'
export { ScalewayProvider } from './scaleway'

import type { CloudProvider, ProviderName } from './types'
import { HetznerProvider } from './hetzner'
import { VerdaProvider } from './verda'
import { ScalewayProvider } from './scaleway'

export interface ProviderEnv {
  HETZNER_API_TOKEN?: string
  VERDA_API_TOKEN?: string
  SCALEWAY_API_TOKEN?: string
  SCALEWAY_PROJECT_ID?: string
}

/** Resolve a CloudProvider by name from environment credentials. */
export function resolveProvider(name: ProviderName, env: ProviderEnv): CloudProvider {
  switch (name) {
    case 'hetzner': {
      if (!env.HETZNER_API_TOKEN) throw new Error('HETZNER_API_TOKEN not configured')
      return new HetznerProvider(env.HETZNER_API_TOKEN)
    }
    case 'verda': {
      if (!env.VERDA_API_TOKEN) throw new Error('VERDA_API_TOKEN not configured')
      return new VerdaProvider(env.VERDA_API_TOKEN)
    }
    case 'scaleway': {
      if (!env.SCALEWAY_API_TOKEN) throw new Error('SCALEWAY_API_TOKEN not configured')
      if (!env.SCALEWAY_PROJECT_ID) throw new Error('SCALEWAY_PROJECT_ID not configured')
      return new ScalewayProvider(env.SCALEWAY_API_TOKEN, env.SCALEWAY_PROJECT_ID)
    }
    case 'runpod':
      throw new Error('RunPod provider not yet implemented')
    default:
      throw new Error(`Unknown provider: ${name}`)
  }
}
