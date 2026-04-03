export interface PlanLimits {
  instances: number
  members: number
  requestsPerMin: number
  /** Monthly included API requests (0 = unlimited) */
  requestsPerMonth: number
  /** Max models that can be loaded */
  models: number
  /** Security scan frequency */
  scanFrequency: 'weekly' | 'daily' | 'continuous' | 'custom'
  /** Whether overage billing is enabled (false = hard cap) */
  overageEnabled: boolean
  /** Monthly price in cents (0 = free) */
  priceCents: number
}

export const PLANS: Record<string, PlanLimits> = {
  free: {
    instances: 1,
    members: 1,
    requestsPerMin: 60,
    requestsPerMonth: 3000, // ~100/day
    models: 1,
    scanFrequency: 'weekly',
    overageEnabled: false,
    priceCents: 0,
  },
  pro: {
    instances: 10,
    members: 1,
    requestsPerMin: 300,
    requestsPerMonth: 10_000,
    models: 5,
    scanFrequency: 'daily',
    overageEnabled: true,
    priceCents: 1900,
  },
  team: {
    instances: 50,
    members: 25,
    requestsPerMin: 1000,
    requestsPerMonth: 50_000,
    models: 0, // unlimited
    scanFrequency: 'continuous',
    overageEnabled: true,
    priceCents: 4900,
  },
  enterprise: {
    instances: 0, // unlimited
    members: 0, // unlimited
    requestsPerMin: 0, // unlimited
    requestsPerMonth: 0, // committed usage tiers
    models: 0, // unlimited
    scanFrequency: 'custom',
    overageEnabled: true,
    priceCents: 0, // negotiated
  },
}

export function getPlanLimits(plan: string): PlanLimits {
  return PLANS[plan] || PLANS.free
}

/** Metered usage dimensions and their per-unit overage prices (in cents) */
export const METERED_DIMENSIONS = {
  api_requests: {
    name: 'API Requests',
    unit: 'request',
    /** Price per 1,000 requests overage */
    overagePricePer1k: 50, // $0.50 per 1K
  },
  security_scans: {
    name: 'Security Scans',
    unit: 'scan',
    /** Price per extra scan */
    overagePricePerUnit: 10, // $0.10 per scan
  },
  tunnel_bandwidth: {
    name: 'Tunnel Bandwidth',
    unit: 'GB',
    /** Price per GB overage */
    overagePricePerUnit: 5, // $0.05 per GB
  },
} as const

export type MeteredDimension = keyof typeof METERED_DIMENSIONS
