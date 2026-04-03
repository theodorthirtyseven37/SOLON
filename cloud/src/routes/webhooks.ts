import { Hono } from 'hono'
import type { Env, ManagedInstanceRow } from '../types'
import { verifyWebhookSignature } from '../lib/stripe'
import { encrypt } from '../lib/crypto'

const webhooks = new Hono<{ Bindings: Env }>()

// POST /webhooks/stripe — Stripe webhook handler (no auth middleware, uses signature verification)
webhooks.post('/stripe', async (c) => {
  const signature = c.req.header('stripe-signature')
  if (!signature) return c.json({ error: 'Missing signature' }, 400)

  const body = await c.req.text()
  const valid = await verifyWebhookSignature(body, signature, c.env.STRIPE_WEBHOOK_SECRET)
  if (!valid) return c.json({ error: 'Invalid signature' }, 400)

  const event = JSON.parse(body) as {
    type: string
    data: { object: Record<string, unknown> }
  }

  switch (event.type) {
    case 'checkout.session.completed':
      await handleCheckoutCompleted(c.env, event.data.object)
      break

    case 'customer.subscription.deleted':
      await handleSubscriptionDeleted(c.env, event.data.object)
      break

    case 'customer.subscription.updated':
      await handleSubscriptionUpdated(c.env, event.data.object)
      break

    case 'invoice.payment_failed':
      await handlePaymentFailed(c.env, event.data.object)
      break
  }

  return c.json({ received: true })
})

async function handleCheckoutCompleted(env: Env, session: Record<string, unknown>) {
  const metadata = session.metadata as Record<string, string> | undefined
  if (!metadata?.user_id) return

  // Handle SaaS subscription checkout
  if (metadata.type === 'saas_subscription' && metadata.plan) {
    await handleSaasCheckout(env, session, metadata)
    return
  }

  // Handle managed hosting checkout (requires tier)
  if (!metadata.tier) return

  const userId = metadata.user_id
  const tier = metadata.tier
  const region = metadata.region || 'eu-central'
  const instanceName = metadata.instance_name || `solon-${Date.now().toString(36)}`
  const subscriptionId = session.subscription as string

  const instanceId = crypto.randomUUID()

  // Create managed instance record
  await env.DB.prepare(
    `INSERT INTO managed_instances (id, user_id, name, tier, status, region, stripe_subscription_id)
     VALUES (?, ?, ?, ?, 'pending', ?, ?)`,
  )
    .bind(instanceId, userId, instanceName, tier, region, subscriptionId)
    .run()

  // Trigger provisioning
  if (env.PROVISIONER_URL && env.PROVISIONER_SECRET) {
    try {
      const payload = JSON.stringify({
        action: 'create',
        instance_id: instanceId,
        tier,
        region,
        name: instanceName,
      })

      const timestamp = Math.floor(Date.now() / 1000).toString()
      const sigKey = await crypto.subtle.importKey(
        'raw',
        new TextEncoder().encode(env.PROVISIONER_SECRET),
        { name: 'HMAC', hash: 'SHA-256' },
        false,
        ['sign'],
      )
      const sig = await crypto.subtle.sign('HMAC', sigKey, new TextEncoder().encode(`${timestamp}.${payload}`))
      const sigHex = Array.from(new Uint8Array(sig)).map(b => b.toString(16).padStart(2, '0')).join('')

      await fetch(`${env.PROVISIONER_URL}/webhook/provision`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Signature': `t=${timestamp},v1=${sigHex}`,
        },
        body: payload,
      })

      await env.DB.prepare(
        `UPDATE managed_instances SET status = 'provisioning' WHERE id = ?`,
      ).bind(instanceId).run()
    } catch (err) {
      console.error('Failed to trigger provisioning:', err)
    }
  }
}

async function handleSaasCheckout(env: Env, session: Record<string, unknown>, metadata: Record<string, string>) {
  const userId = metadata.user_id
  const plan = metadata.plan
  const subscriptionId = session.subscription as string
  const customerId = session.customer as string

  // Upsert subscription record
  const existing = await env.DB.prepare(
    'SELECT id FROM subscriptions WHERE user_id = ?',
  ).bind(userId).first<{ id: string }>()

  if (existing) {
    await env.DB.prepare(
      `UPDATE subscriptions SET plan = ?, stripe_customer_id = ?, stripe_subscription_id = ?, status = 'active', cancel_at_period_end = 0, updated_at = datetime('now') WHERE user_id = ?`,
    ).bind(plan, customerId, subscriptionId, userId).run()
  } else {
    const subId = crypto.randomUUID()
    await env.DB.prepare(
      `INSERT INTO subscriptions (id, user_id, stripe_customer_id, stripe_subscription_id, plan, status) VALUES (?, ?, ?, ?, ?, 'active')`,
    ).bind(subId, userId, customerId, subscriptionId, plan).run()
  }

  // Update user plan
  await env.DB.prepare(
    'UPDATE users SET plan = ?, updated_at = datetime(\'now\') WHERE id = ?',
  ).bind(plan, userId).run()
}

async function handleSubscriptionUpdated(env: Env, subscription: Record<string, unknown>) {
  const metadata = subscription.metadata as Record<string, string> | undefined
  if (!metadata?.user_id || metadata?.type !== 'saas_subscription') return

  const cancelAtPeriodEnd = subscription.cancel_at_period_end as boolean
  const currentPeriodEnd = subscription.current_period_end as number

  const periodEndDate = currentPeriodEnd
    ? new Date(currentPeriodEnd * 1000).toISOString()
    : null

  await env.DB.prepare(
    `UPDATE subscriptions SET cancel_at_period_end = ?, current_period_end = ?, updated_at = datetime('now') WHERE stripe_subscription_id = ?`,
  ).bind(cancelAtPeriodEnd ? 1 : 0, periodEndDate, subscription.id as string).run()
}

async function handleSubscriptionDeleted(env: Env, subscription: Record<string, unknown>) {
  const subId = subscription.id as string
  if (!subId) return

  // Check if this is a SaaS subscription
  const metadata = subscription.metadata as Record<string, string> | undefined
  if (metadata?.type === 'saas_subscription' && metadata?.user_id) {
    // Downgrade user to free plan
    await env.DB.prepare(
      'UPDATE users SET plan = ?, updated_at = datetime(\'now\') WHERE id = ?',
    ).bind('free', metadata.user_id).run()
    await env.DB.prepare(
      `UPDATE subscriptions SET plan = 'free', status = 'canceled', cancel_at_period_end = 0, updated_at = datetime('now') WHERE stripe_subscription_id = ?`,
    ).bind(subId).run()
    return
  }

  // Handle managed hosting subscription deletion
  const instance = await env.DB.prepare(
    `SELECT id FROM managed_instances WHERE stripe_subscription_id = ? AND status != 'deleted'`,
  ).bind(subId).first<ManagedInstanceRow>()

  if (!instance) return

  // Mark for deletion
  await env.DB.prepare(
    `UPDATE managed_instances SET status = 'deleting' WHERE id = ?`,
  ).bind(instance.id).run()

  // Trigger deletion
  if (env.PROVISIONER_URL && env.PROVISIONER_SECRET) {
    try {
      const payload = JSON.stringify({
        action: 'delete',
        instance_id: instance.id,
      })

      const timestamp = Math.floor(Date.now() / 1000).toString()
      const sigKey = await crypto.subtle.importKey(
        'raw',
        new TextEncoder().encode(env.PROVISIONER_SECRET),
        { name: 'HMAC', hash: 'SHA-256' },
        false,
        ['sign'],
      )
      const sig = await crypto.subtle.sign('HMAC', sigKey, new TextEncoder().encode(`${timestamp}.${payload}`))
      const sigHex = Array.from(new Uint8Array(sig)).map(b => b.toString(16).padStart(2, '0')).join('')

      await fetch(`${env.PROVISIONER_URL}/webhook/provision`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Signature': `t=${timestamp},v1=${sigHex}`,
        },
        body: payload,
      })
    } catch (err) {
      console.error('Failed to trigger deletion:', err)
    }
  }
}

async function handlePaymentFailed(env: Env, invoice: Record<string, unknown>) {
  const subId = invoice.subscription as string
  if (!subId) return

  // Suspend after payment failure
  await env.DB.prepare(
    `UPDATE managed_instances SET status = 'suspended' WHERE stripe_subscription_id = ? AND status = 'running'`,
  ).bind(subId).run()
}

// POST /webhooks/provisioner — Callback from provisioner when server is ready or failed
webhooks.post('/provisioner', async (c) => {
  const signature = c.req.header('x-signature')
  if (!signature || !c.env.PROVISIONER_SECRET) return c.json({ error: 'Missing signature' }, 400)

  const body = await c.req.text()

  // Verify HMAC
  const parts = signature.split(',').reduce<Record<string, string>>((acc, part) => {
    const [k, v] = part.split('=')
    acc[k] = v
    return acc
  }, {})
  const timestamp = parts['t']
  const sig = parts['v1']
  if (!timestamp || !sig) return c.json({ error: 'Invalid signature' }, 400)

  const sigKey = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(c.env.PROVISIONER_SECRET),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  )
  const expected = await crypto.subtle.sign('HMAC', sigKey, new TextEncoder().encode(`${timestamp}.${body}`))
  const expectedHex = Array.from(new Uint8Array(expected)).map(b => b.toString(16).padStart(2, '0')).join('')
  if (expectedHex !== sig) return c.json({ error: 'Invalid signature' }, 400)

  const payload = JSON.parse(body) as {
    instance_id: string
    status: string
    ipv4?: string
    solon_api_key?: string
    dashboard_url?: string
    error?: string
  }

  if (payload.status === 'running' && payload.ipv4) {
    // Encrypt the Solon API key before storing
    let apiKeyEnc: string | null = null
    if (payload.solon_api_key) {
      apiKeyEnc = await encrypt(payload.solon_api_key, c.env.ENCRYPTION_KEY)
    }

    await c.env.DB.prepare(
      `UPDATE managed_instances SET status = 'running', ipv4 = ?, solon_api_key_enc = ?, dashboard_url = ?, ready_at = datetime('now') WHERE id = ?`,
    ).bind(payload.ipv4, apiKeyEnc, payload.dashboard_url || `http://${payload.ipv4}:8420`, payload.instance_id).run()
  } else if (payload.status === 'failed') {
    await c.env.DB.prepare(
      `UPDATE managed_instances SET status = 'failed' WHERE id = ?`,
    ).bind(payload.instance_id).run()
  } else if (payload.status === 'deleted') {
    await c.env.DB.prepare(
      `UPDATE managed_instances SET status = 'deleted', deleted_at = datetime('now') WHERE id = ?`,
    ).bind(payload.instance_id).run()
  }

  return c.json({ received: true })
})

export default webhooks
