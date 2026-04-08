// Cloud provider abstraction for multi-provider GPU provisioning.
// Supports Hetzner, Verda (ex-DataCrunch), Scaleway, and more.
// NVIDIA is a fallback — users choose their GPU vendor.

// --- Provider interface ---

export interface CloudProvider {
  readonly name: ProviderName
  createServer(opts: CreateServerOpts): Promise<ProviderServerResult>
  deleteServer(serverId: string): Promise<void>
  findServer(instanceId: string): Promise<ProviderServerInfo | null>
}

export interface CreateServerOpts {
  name: string
  instanceId: string
  gpu: GpuTier
  region: string
  userData: string
  labels: Record<string, string>
}

export interface ProviderServerResult {
  serverId: string
  name: string
  ipv4?: string
  status: string
}

export interface ProviderServerInfo {
  serverId: string
  ipv4: string
  status: string
}

// --- Provider registry ---

export type ProviderName = 'hetzner' | 'verda' | 'scaleway' | 'runpod'

// --- GPU types ---

export type GpuVendor = 'nvidia' | 'amd' | 'intel'

export interface GpuSpec {
  name: string
  vendor: GpuVendor
  vram: number       // GB
  architecture: string
}

export const GPU_SPECS: Record<string, GpuSpec> = {
  // NVIDIA
  'rtx-4000-sff':   { name: 'RTX 4000 SFF Ada', vendor: 'nvidia', vram: 20, architecture: 'Ada Lovelace' },
  'l4':             { name: 'NVIDIA L4',         vendor: 'nvidia', vram: 24, architecture: 'Ada Lovelace' },
  'l40s':           { name: 'NVIDIA L40S',       vendor: 'nvidia', vram: 48, architecture: 'Ada Lovelace' },
  'a100-40':        { name: 'NVIDIA A100 40GB',  vendor: 'nvidia', vram: 40, architecture: 'Ampere' },
  'a100-80':        { name: 'NVIDIA A100 80GB',  vendor: 'nvidia', vram: 80, architecture: 'Ampere' },
  'h100':           { name: 'NVIDIA H100',       vendor: 'nvidia', vram: 80, architecture: 'Hopper' },
  'h200':           { name: 'NVIDIA H200',       vendor: 'nvidia', vram: 141, architecture: 'Hopper' },
  // AMD
  'mi300x':         { name: 'AMD MI300X',        vendor: 'amd',    vram: 192, architecture: 'CDNA 3' },
  'mi250':          { name: 'AMD MI250',          vendor: 'amd',    vram: 128, architecture: 'CDNA 2' },
  // Intel
  'gaudi2':         { name: 'Intel Gaudi2',      vendor: 'intel',  vram: 96, architecture: 'Gaudi' },
  'gaudi3':         { name: 'Intel Gaudi3',      vendor: 'intel',  vram: 128, architecture: 'Gaudi' },
}

// --- GPU tiers (what customers buy) ---

export interface GpuTier {
  id: string
  name: string
  gpuId: string            // key into GPU_SPECS
  gpuCount: number
  provider: ProviderName
  providerServerType: string  // provider-specific instance type
  region: string              // provider-specific region/location
  priceMonthly?: number       // cents (for fixed monthly billing)
  priceHourly?: number        // cents (for usage-based billing)
  billingModel: 'monthly' | 'hourly'
  /** Models that fit in this tier's total VRAM */
  fitsModels: string[]
}

// All available GPU tiers across providers.
// Sorted by price ascending within each provider.
export const GPU_TIERS: GpuTier[] = [
  // --- Hetzner (Germany/Finland) — monthly, budget entry ---
  {
    id: 'hetzner-rtx4000',
    name: 'GPU Starter (Hetzner)',
    gpuId: 'rtx-4000-sff',
    gpuCount: 1,
    provider: 'hetzner',
    providerServerType: 'gex44',
    region: 'fsn1',
    priceMonthly: 18400, // €184 ≈ $200
    billingModel: 'monthly',
    fitsModels: ['llama3.2:8b', 'phi4:14b', 'gemma3:12b', 'qwen2.5:14b'],
  },

  // --- Scaleway (France/Poland) — hourly, mid-range ---
  {
    id: 'scaleway-l4',
    name: 'GPU L4 (Scaleway)',
    gpuId: 'l4',
    gpuCount: 1,
    provider: 'scaleway',
    providerServerType: 'GPU-3070-S', // Scaleway L4 instance type
    region: 'fr-par-1',
    priceHourly: 75, // $0.75/hr
    billingModel: 'hourly',
    fitsModels: ['llama3.2:8b', 'phi4:14b', 'gemma3:12b', 'qwen2.5:14b'],
  },
  {
    id: 'scaleway-l40s',
    name: 'GPU L40S (Scaleway)',
    gpuId: 'l40s',
    gpuCount: 1,
    provider: 'scaleway',
    providerServerType: 'L40S-1-48G',
    region: 'fr-par-2',
    priceHourly: 140, // $1.40/hr
    billingModel: 'hourly',
    fitsModels: ['llama3.1:70b', 'deepseek-r1:32b', 'mixtral:8x7b', 'qwen2.5:32b'],
  },
  {
    id: 'scaleway-h100',
    name: 'GPU H100 (Scaleway)',
    gpuId: 'h100',
    gpuCount: 1,
    provider: 'scaleway',
    providerServerType: 'H100-1-80G',
    region: 'fr-par-2',
    priceHourly: 273, // $2.73/hr
    billingModel: 'hourly',
    fitsModels: ['llama3.1:70b', 'deepseek-r1:70b', 'mixtral:8x7b'],
  },

  // --- Verda / DataCrunch (Finland) — hourly, high-end ---
  {
    id: 'verda-a100-80',
    name: 'GPU A100 80GB (Verda)',
    gpuId: 'a100-80',
    gpuCount: 1,
    provider: 'verda',
    providerServerType: 'A100.80G',
    region: 'FIN-01',
    priceHourly: 189, // $1.89/hr
    billingModel: 'hourly',
    fitsModels: ['llama3.1:70b', 'deepseek-r1:70b', 'mixtral:8x7b'],
  },
  {
    id: 'verda-h100',
    name: 'GPU H100 (Verda)',
    gpuId: 'h100',
    gpuCount: 1,
    provider: 'verda',
    providerServerType: 'H100.80G',
    region: 'FIN-01',
    priceHourly: 229, // $2.29/hr
    billingModel: 'hourly',
    fitsModels: ['llama3.1:70b', 'deepseek-r1:70b', 'mixtral:8x7b'],
  },
  {
    id: 'verda-h200',
    name: 'GPU H200 (Verda)',
    gpuId: 'h200',
    gpuCount: 1,
    provider: 'verda',
    providerServerType: 'H200.141G',
    region: 'FIN-01',
    priceHourly: 329, // $3.29/hr
    billingModel: 'hourly',
    fitsModels: ['llama3.1:70b', 'deepseek-r1:70b'],
  },
]

// --- Region catalog ---

export interface RegionInfo {
  id: string
  provider: ProviderName
  displayName: string
  country: string
  city: string
}

export const REGIONS: RegionInfo[] = [
  // Hetzner
  { id: 'fsn1', provider: 'hetzner',  displayName: 'Falkenstein, DE',  country: 'DE', city: 'Falkenstein' },
  { id: 'nbg1', provider: 'hetzner',  displayName: 'Nuremberg, DE',   country: 'DE', city: 'Nuremberg' },
  { id: 'hel1', provider: 'hetzner',  displayName: 'Helsinki, FI',    country: 'FI', city: 'Helsinki' },
  // Scaleway
  { id: 'fr-par-1', provider: 'scaleway', displayName: 'Paris 1, FR',   country: 'FR', city: 'Paris' },
  { id: 'fr-par-2', provider: 'scaleway', displayName: 'Paris 2, FR',   country: 'FR', city: 'Paris' },
  { id: 'pl-waw-2', provider: 'scaleway', displayName: 'Warsaw, PL',    country: 'PL', city: 'Warsaw' },
  // Verda (ex-DataCrunch)
  { id: 'FIN-01', provider: 'verda',  displayName: 'Helsinki 1, FI',  country: 'FI', city: 'Helsinki' },
  { id: 'FIN-02', provider: 'verda',  displayName: 'Helsinki 2, FI',  country: 'FI', city: 'Helsinki' },
  { id: 'ISL-01', provider: 'verda',  displayName: 'Reykjanesbær, IS', country: 'IS', city: 'Reykjanesbær' },
]

// --- Helpers ---

export function getGpuSpec(gpuId: string): GpuSpec | undefined {
  return GPU_SPECS[gpuId]
}

export function getTierById(tierId: string): GpuTier | undefined {
  return GPU_TIERS.find(t => t.id === tierId)
}

export function getTiersForProvider(provider: ProviderName): GpuTier[] {
  return GPU_TIERS.filter(t => t.provider === provider)
}

export function getTotalVram(tier: GpuTier): number {
  const spec = GPU_SPECS[tier.gpuId]
  if (!spec) return 0
  return spec.vram * tier.gpuCount
}

export function getGpuVendor(tier: GpuTier): GpuVendor {
  return GPU_SPECS[tier.gpuId]?.vendor ?? 'nvidia'
}
