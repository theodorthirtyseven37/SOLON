/**
 * Account-level plan limits.
 * Revenue comes from managed server subscriptions (see SERVER_TIERS in stripe.ts).
 * These limits gate how many self-hosted instances a user can register
 * and how many team members they can invite.
 */
export interface PlanLimits {
  /** Max registered self-hosted instances */
  instances: number
  /** Max team members */
  members: number
  /** Rate limit for cloud API (requests per minute) */
  requestsPerMin: number
}

export const PLANS: Record<string, PlanLimits> = {
  free: {
    instances: 1,
    members: 1,
    requestsPerMin: 60,
  },
  pro: {
    instances: 10,
    members: 5,
    requestsPerMin: 300,
  },
  team: {
    instances: 50,
    members: 25,
    requestsPerMin: 1000,
  },
}

export function getPlanLimits(plan: string): PlanLimits {
  return PLANS[plan] || PLANS.free
}
