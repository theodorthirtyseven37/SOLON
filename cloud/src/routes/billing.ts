import { Hono } from 'hono'
import type { Env, UserRow, ManagedInstanceRow } from '../types'
import { getPlanLimits } from '../lib/plans'
import { createCheckoutSession, cancelSubscription, SERVER_TIERS } from '../lib/stripe'
import { badRequest, notFound } from '../lib/errors'

type Variables = { userId: string; userPlan: string }

const billing = new Hono<{ Bindings: Env; Variables: Variables }>()

// GET /billing — Billing overview: managed servers, account plan, available tiers
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

  // Get managed server instances
  const managedInstances = await c.env.DB.prepare(
    'SELECT * FROM managed_instances WHERE user_id = ? AND status != ? ORDER BY created_at DESC',
  )
    .bind(userId, 'deleted')
    .all<ManagedInstanceRow>()

  return c.json({
    plan: user.plan,
    status: 'active',
    usage: {
      instances: { used: instanceCount?.cnt || 0, limit: limits.instances },
      team_members: { used: memberCount?.cnt || 0, limit: limits.members },
    },
    managed_servers: managedInstances?.results || [],
    available_tiers: Object.entries(SERVER_TIERS).map(([key, tier]) => ({
      key,
      name: tier.name,
      price_cents: tier.priceCents,
      billing: tier.billing,
      description: tier.description,
      features: tier.features,
      vcpu: tier.vcpu,
      ram_gb: tier.ramGb,
      disk_gb: tier.diskGb,
      models: tier.models,
      agents: tier.agents,
      has_gpu: tier.hasGpu,
      gpu_model: tier.gpuModel || null,
      gpu_vram_gb: tier.gpuVramGb || null,
      security: tier.security,
    })),
  })
})

// POST /billing/checkout — Create a Stripe Checkout session for a managed server
billing.post('/checkout', async (c) => {
  const userId = c.get('userId')
  const user = await c.env.DB.prepare('SELECT * FROM users WHERE id = ?').bind(userId).first<UserRow>()
  if (!user) throw notFound('User not found')

  const body = await c.req.json<{ tier: string; region?: string; name?: string }>()
  if (!body.tier || !SERVER_TIERS[body.tier]) {
    throw badRequest(`Invalid tier: must be one of ${Object.keys(SERVER_TIERS).join(', ')}`)
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

// POST /billing/portal — Create a Stripe Customer Portal session for managing servers
billing.post('/portal', async (c) => {
  const userId = c.get('userId')

  const sub = await c.env.DB.prepare(
    'SELECT stripe_subscription_id FROM managed_instances WHERE user_id = ? AND status != ? LIMIT 1',
  )
    .bind(userId, 'deleted')
    .first<{ stripe_subscription_id: string }>()

  if (!sub?.stripe_subscription_id) {
    throw badRequest('No active server subscription found')
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

// POST /billing/servers/:id/cancel — Cancel a managed server subscription
billing.post('/servers/:id/cancel', async (c) => {
  const userId = c.get('userId')
  const serverId = c.req.param('id')

  const instance = await c.env.DB.prepare(
    'SELECT stripe_subscription_id FROM managed_instances WHERE id = ? AND user_id = ? AND status != ?',
  ).bind(serverId, userId, 'deleted').first<{ stripe_subscription_id: string | null }>()

  if (!instance) throw notFound('Server not found')
  if (!instance.stripe_subscription_id) throw badRequest('No subscription for this server')

  await cancelSubscription(c.env.STRIPE_SECRET_KEY, instance.stripe_subscription_id)

  return c.json({ status: 'canceling', message: 'Server subscription will cancel at end of billing period' })
})

// GET /billing/managed — List managed server instances for the current user
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
