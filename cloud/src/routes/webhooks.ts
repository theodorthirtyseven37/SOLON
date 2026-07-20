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

    case 'invoice.payment_failed':
      await handlePaymentFailed(c.env, event.data.object)
      break
  }

  return c.json({ received: true })
})

async function handleCheckoutCompleted(env: Env, session: Record<string, unknown>) {
  const metadata = session.metadata as Record<string, string> | undefined
  if (!metadata?.user_id || !metadata?.tier) return

  const userId = metadata.user_id
  const tier = metadata.tier
  const region = metadata.region || 'eu-central'
  const instanceName = metadata.instance_name || `solon-${Date.now().toString(36)}`
  const subscriptionId = session.subscription as string

  const instanceId = crypto.randomUUID()

  // Create managed server instance record
  await env.DB.prepare(
    `INSERT INTO managed_instances (id, user_id, name, tier, status, region, stripe_subscription_id)
     VALUES (?, ?, ?, ?, 'pending', ?, ?)`,
  )
    .bind(instanceId, userId, instanceName, tier, region, subscriptionId)
    .run()

  // Trigger server provisioning
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
        `UPDATE managed_instances SET status = 'provisioning', updated_at = datetime('now') WHERE id = ?`,
      ).bind(instanceId).run()
    } catch (err) {
      console.error('Failed to trigger provisioning:', err)
    }
  }
}

async function handleSubscriptionDeleted(env: Env, subscription: Record<string, unknown>) {
  const subId = subscription.id as string
  if (!subId) return

  const instance = await env.DB.prepare(
    `SELECT id FROM managed_instances WHERE stripe_subscription_id = ? AND status != 'deleted'`,
  ).bind(subId).first<ManagedInstanceRow>()

  if (!instance) return

  // Mark server for deletion
  await env.DB.prepare(
    `UPDATE managed_instances SET status = 'deleting', updated_at = datetime('now') WHERE id = ?`,
  ).bind(instance.id).run()

  // Trigger server deletion
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

  // Suspend server after payment failure
  await env.DB.prepare(
    `UPDATE managed_instances SET status = 'suspended', updated_at = datetime('now') WHERE stripe_subscription_id = ? AND status = 'running'`,
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

  // Reject signatures older than 5 minutes (prevent replay attacks)
  const tsNum = parseInt(timestamp, 10)
  const now = Math.floor(Date.now() / 1000)
  if (Math.abs(now - tsNum) > 300) {
    return c.json({ error: 'Signature timestamp expired' }, 401)
  }

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
      try {
        apiKeyEnc = await encrypt(payload.solon_api_key, c.env.ENCRYPTION_KEY)
      } catch (err) {
        console.error('Failed to encrypt API key:', err)
        await c.env.DB.prepare(
          `UPDATE managed_instances SET status = 'failed', updated_at = datetime('now') WHERE id = ?`,
        ).bind(payload.instance_id).run()
        return c.json({ error: 'Encryption failed' }, 500)
      }
    }

    await c.env.DB.prepare(
      `UPDATE managed_instances SET status = 'running', ipv4 = ?, solon_api_key_enc = ?, dashboard_url = ?, ready_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`,
    ).bind(payload.ipv4, apiKeyEnc, payload.dashboard_url || `http://${payload.ipv4}:8420`, payload.instance_id).run()
  } else if (payload.status === 'failed') {
    await c.env.DB.prepare(
      `UPDATE managed_instances SET status = 'failed', updated_at = datetime('now') WHERE id = ?`,
    ).bind(payload.instance_id).run()
  } else if (payload.status === 'deleted') {
    await c.env.DB.prepare(
      `UPDATE managed_instances SET status = 'deleted', deleted_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`,
    ).bind(payload.instance_id).run()
  }

  return c.json({ received: true })
})

export default webhooks
