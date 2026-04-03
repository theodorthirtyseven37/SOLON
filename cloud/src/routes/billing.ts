import { Hono } from 'hono'
import type { Env, UserRow, ManagedInstanceRow } from '../types'
import { getPlanLimits, METERED_DIMENSIONS } from '../lib/plans'
import { createCheckoutSession, createSubscriptionCheckout, cancelSubscription, MANAGED_TIERS, SAAS_TIERS } from '../lib/stripe'
import { badRequest, notFound } from '../lib/errors'

type Variables = { userId: string; userPlan: string }
type SubscriptionRow = { id: string; stripe_customer_id: string | null; stripe_subscription_id: string | null; plan: string; status: string; current_period_start: string | null; current_period_end: string | null; cancel_at_period_end: number }

const billing = new Hono<{ Bindings: Env; Variables: Variables }>()

// GET /billing — Full billing overview with plan, usage, and subscription details
billing.get('/', async (c) => {
  const userId = c.get('userId')

  const user = await c.env.DB.prepare('SELECT * FROM users WHERE id = ?').bind(userId).first<UserRow>()
  if (!user) throw notFound('User not found')

  const limits = getPlanLimits(user.plan)

  const instanceCount = await c.env.DB.prepare('SELECT COUNT(*) as cnt FROM instances WHERE user_id = ?')
    .bind(userId)
    .first<{ cnt: number }>()

  const memberCount = await c.env.DB.prepare(
    'SELECT COUNT(*) as cnt FROM team_members tm JOIN teams t ON t.id = tm.team_id WHERE t.owner_id = ?',
  )
    .bind(userId)
    .first<{ cnt: number }>()

  // Get managed instances
  const managedInstances = await c.env.DB.prepare(
    'SELECT * FROM managed_instances WHERE user_id = ? AND status != ? ORDER BY created_at DESC',
  )
    .bind(userId, 'deleted')
    .all<ManagedInstanceRow>()

  // Get subscription details
  const subscription = await c.env.DB.prepare(
    'SELECT * FROM subscriptions WHERE user_id = ?',
  ).bind(userId).first<SubscriptionRow>()

  // Get current month usage
  const now = new Date()
  const periodStart = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-01`
  const usageRows = await c.env.DB.prepare(
    'SELECT dimension, SUM(quantity) as total FROM usage_daily WHERE user_id = ? AND day >= ? GROUP BY dimension',
  ).bind(userId, periodStart).all<{ dimension: string; total: number }>()

  const usageMap: Record<string, number> = {}
  for (const row of usageRows?.results || []) {
    usageMap[row.dimension] = row.total
  }

  return c.json({
    plan: user.plan,
    status: subscription?.status || 'active',
    current_period_end: subscription?.current_period_end || null,
    cancel_at_period_end: subscription?.cancel_at_period_end === 1,
    limits: {
      instances: limits.instances,
      members: limits.members,
      requests_per_month: limits.requestsPerMonth,
      models: limits.models,
      scan_frequency: limits.scanFrequency,
      overage_enabled: limits.overageEnabled,
    },
    usage: {
      instances: { used: instanceCount?.cnt || 0, limit: limits.instances },
      requests: { used: usageMap['api_requests'] || 0, limit: limits.requestsPerMonth },
      team_members: { used: memberCount?.cnt || 0, limit: limits.members },
      security_scans: { used: usageMap['security_scans'] || 0 },
      tunnel_bandwidth_gb: { used: usageMap['tunnel_bandwidth'] || 0 },
    },
    managed_instances: managedInstances?.results || [],
    available_plans: Object.entries(SAAS_TIERS).map(([key, tier]) => ({
      key,
      name: tier.name,
      price_cents: tier.priceCents,
      features: tier.features,
    })),
    metered_pricing: Object.entries(METERED_DIMENSIONS).map(([key, dim]) => ({
      key,
      name: dim.name,
      unit: dim.unit,
    })),
  })
})

// POST /billing/subscribe — Create a Stripe Checkout session for SaaS plan (Pro/Team)
billing.post('/subscribe', async (c) => {
  const userId = c.get('userId')
  const user = await c.env.DB.prepare('SELECT * FROM users WHERE id = ?').bind(userId).first<UserRow>()
  if (!user) throw notFound('User not found')

  const body = await c.req.json<{ plan: string }>()
  if (!body.plan || !SAAS_TIERS[body.plan]) {
    throw badRequest(`Invalid plan: must be one of ${Object.keys(SAAS_TIERS).join(', ')}`)
  }

  if (user.plan === body.plan) {
    throw badRequest('Already on this plan')
  }

  // Check if user has an existing Stripe customer ID
  const sub = await c.env.DB.prepare(
    'SELECT stripe_customer_id FROM subscriptions WHERE user_id = ?',
  ).bind(userId).first<{ stripe_customer_id: string | null }>()

  const session = await createSubscriptionCheckout(c.env.STRIPE_SECRET_KEY, {
    userId,
    userEmail: user.email,
    plan: body.plan,
    customerId: sub?.stripe_customer_id || undefined,
    successUrl: `${c.env.DASHBOARD_URL}/billing?success=true&plan=${body.plan}`,
    cancelUrl: `${c.env.DASHBOARD_URL}/billing?canceled=true`,
  })

  return c.json({ checkout_url: session.url })
})

// POST /billing/cancel — Cancel current SaaS subscription at period end
billing.post('/cancel', async (c) => {
  const userId = c.get('userId')

  const sub = await c.env.DB.prepare(
    'SELECT stripe_subscription_id FROM subscriptions WHERE user_id = ? AND status = ?',
  ).bind(userId, 'active').first<{ stripe_subscription_id: string | null }>()

  if (!sub?.stripe_subscription_id) {
    throw badRequest('No active subscription to cancel')
  }

  await cancelSubscription(c.env.STRIPE_SECRET_KEY, sub.stripe_subscription_id)

  await c.env.DB.prepare(
    'UPDATE subscriptions SET cancel_at_period_end = 1, updated_at = datetime(\'now\') WHERE user_id = ?',
  ).bind(userId).run()

  return c.json({ status: 'canceling', message: 'Subscription will cancel at end of billing period' })
})

// GET /billing/usage — Detailed usage breakdown for current period
billing.get('/usage', async (c) => {
  const userId = c.get('userId')
  const limits = getPlanLimits(c.get('userPlan'))

  const now = new Date()
  const periodStart = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-01`

  const dailyUsage = await c.env.DB.prepare(
    'SELECT dimension, day, quantity FROM usage_daily WHERE user_id = ? AND day >= ? ORDER BY day ASC',
  ).bind(userId, periodStart).all<{ dimension: string; day: string; quantity: number }>()

  const totals = await c.env.DB.prepare(
    'SELECT dimension, SUM(quantity) as total FROM usage_daily WHERE user_id = ? AND day >= ? GROUP BY dimension',
  ).bind(userId, periodStart).all<{ dimension: string; total: number }>()

  const totalMap: Record<string, number> = {}
  for (const row of totals?.results || []) {
    totalMap[row.dimension] = row.total
  }

  return c.json({
    period_start: periodStart,
    plan: c.get('userPlan'),
    dimensions: Object.entries(METERED_DIMENSIONS).map(([key, dim]) => ({
      key,
      name: dim.name,
      unit: dim.unit,
      used: totalMap[key] || 0,
      included: key === 'api_requests' ? limits.requestsPerMonth : 0,
      overage_enabled: limits.overageEnabled,
    })),
    daily: dailyUsage?.results || [],
  })
})

// POST /billing/checkout — Create a Stripe Checkout session for managed hosting
billing.post('/checkout', async (c) => {
  const userId = c.get('userId')
  const user = await c.env.DB.prepare('SELECT * FROM users WHERE id = ?').bind(userId).first<UserRow>()
  if (!user) throw notFound('User not found')

  const body = await c.req.json<{ tier: string; region?: string; name?: string }>()
  if (!body.tier || !MANAGED_TIERS[body.tier]) {
    throw badRequest(`Invalid tier: must be one of ${Object.keys(MANAGED_TIERS).join(', ')}`)
  }

  const region = body.region || 'eu-central'
  const instanceName = body.name || `solon-${Date.now().toString(36)}`

  const session = await createCheckoutSession(c.env.STRIPE_SECRET_KEY, {
    userId,
    userEmail: user.email,
    tier: body.tier,
    region,
    instanceName,
    successUrl: `${c.env.DASHBOARD_URL}/billing?success=true`,
    cancelUrl: `${c.env.DASHBOARD_URL}/billing?canceled=true`,
  })

  return c.json({ checkout_url: session.url })
})

// POST /billing/portal — Create a Stripe Customer Portal session
billing.post('/portal', async (c) => {
  const userId = c.get('userId')

  const sub = await c.env.DB.prepare(
    'SELECT stripe_subscription_id FROM managed_instances WHERE user_id = ? AND status != ? LIMIT 1',
  )
    .bind(userId, 'deleted')
    .first<{ stripe_subscription_id: string }>()

  if (!sub?.stripe_subscription_id) {
    throw badRequest('No active subscription found')
  }

  // Get customer ID from Stripe subscription
  const resp = await fetch(`https://api.stripe.com/v1/subscriptions/${sub.stripe_subscription_id}`, {
    headers: { Authorization: `Bearer ${c.env.STRIPE_SECRET_KEY}` },
  })
  const subscription = await resp.json() as { customer: string }

  const { createPortalSession } = await import('../lib/stripe')
  const portal = await createPortalSession(
    c.env.STRIPE_SECRET_KEY,
    subscription.customer,
    `${c.env.DASHBOARD_URL}/billing`,
  )

  return c.json({ portal_url: portal.url })
})

// GET /billing/managed — List managed instances for the current user
billing.get('/managed', async (c) => {
  const userId = c.get('userId')

  const result = await c.env.DB.prepare(
    'SELECT id, name, tier, status, ipv4, region, dashboard_url, created_at, ready_at FROM managed_instances WHERE user_id = ? AND status != ? ORDER BY created_at DESC',
  )
    .bind(userId, 'deleted')
    .all()

  return c.json({ instances: result?.results || [] })
})

export default billing
