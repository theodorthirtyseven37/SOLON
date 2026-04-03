// Stripe API helpers for Cloudflare Workers (uses fetch, not Node SDK)

const STRIPE_API = 'https://api.stripe.com/v1'

// Managed hosting tier pricing
export const MANAGED_TIERS: Record<string, { name: string; price: number; serverType: string }> = {
  starter: { name: 'Starter', price: 2900, serverType: 'cx22' },
  pro: { name: 'Pro', price: 5900, serverType: 'cx42' },
  gpu: { name: 'GPU', price: 34900, serverType: 'gx11' },
}

// SaaS subscription tier Stripe price IDs (set via env or hardcoded after Stripe product creation)
// These are created in Stripe dashboard or via API and stored here for checkout
export interface SaasTierConfig {
  name: string
  priceCents: number
  interval: 'month'
  features: string[]
}

export const SAAS_TIERS: Record<string, SaasTierConfig> = {
  pro: {
    name: 'Solon Pro',
    priceCents: 1900,
    interval: 'month',
    features: ['10K API requests/mo', '5 models', 'Daily security scans', 'Email support'],
  },
  team: {
    name: 'Solon Team',
    priceCents: 4900,
    interval: 'month',
    features: ['50K API requests/mo', 'Unlimited models', 'Continuous scanning', '25 team members', 'Priority support'],
  },
}

async function stripeRequest(
  path: string,
  secretKey: string,
  method: string = 'GET',
  body?: Record<string, string>,
): Promise<unknown> {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${secretKey}`,
  }

  let bodyStr: string | undefined
  if (body) {
    headers['Content-Type'] = 'application/x-www-form-urlencoded'
    bodyStr = new URLSearchParams(body).toString()
  }

  const resp = await fetch(`${STRIPE_API}${path}`, {
    method,
    headers,
    body: bodyStr,
  })

  const data = await resp.json() as Record<string, unknown>
  if (!resp.ok) {
    const err = data.error as Record<string, string> | undefined
    throw new Error(err?.message || `Stripe error: ${resp.status}`)
  }
  return data
}

export async function createCheckoutSession(
  secretKey: string,
  params: {
    userId: string
    userEmail: string
    tier: string
    region: string
    instanceName: string
    successUrl: string
    cancelUrl: string
  },
): Promise<{ id: string; url: string }> {
  const tierInfo = MANAGED_TIERS[params.tier]
  if (!tierInfo) throw new Error(`Unknown tier: ${params.tier}`)

  const data = await stripeRequest('/checkout/sessions', secretKey, 'POST', {
    'mode': 'subscription',
    'success_url': params.successUrl,
    'cancel_url': params.cancelUrl,
    'customer_email': params.userEmail,
    'line_items[0][price_data][currency]': 'usd',
    'line_items[0][price_data][product_data][name]': `Solon Managed — ${tierInfo.name}`,
    'line_items[0][price_data][product_data][description]': `Managed Solon server (${params.tier})`,
    'line_items[0][price_data][unit_amount]': String(tierInfo.price),
    'line_items[0][price_data][recurring][interval]': 'month',
    'line_items[0][quantity]': '1',
    'metadata[user_id]': params.userId,
    'metadata[tier]': params.tier,
    'metadata[region]': params.region,
    'metadata[instance_name]': params.instanceName,
    'subscription_data[metadata][user_id]': params.userId,
    'subscription_data[metadata][tier]': params.tier,
  }) as { id: string; url: string }

  return { id: data.id, url: data.url }
}

export async function createPortalSession(
  secretKey: string,
  customerId: string,
  returnUrl: string,
): Promise<{ url: string }> {
  const data = await stripeRequest('/billing_portal/sessions', secretKey, 'POST', {
    customer: customerId,
    return_url: returnUrl,
  }) as { url: string }
  return { url: data.url }
}

/**
 * Create a Stripe Checkout session for SaaS plan subscription (Pro/Team).
 * Uses inline price_data so no pre-created Stripe products are required.
 */
export async function createSubscriptionCheckout(
  secretKey: string,
  params: {
    userId: string
    userEmail: string
    plan: string
    customerId?: string
    successUrl: string
    cancelUrl: string
  },
): Promise<{ id: string; url: string }> {
  const tierInfo = SAAS_TIERS[params.plan]
  if (!tierInfo) throw new Error(`Unknown SaaS plan: ${params.plan}`)

  const body: Record<string, string> = {
    'mode': 'subscription',
    'success_url': params.successUrl,
    'cancel_url': params.cancelUrl,
    'line_items[0][price_data][currency]': 'usd',
    'line_items[0][price_data][product_data][name]': tierInfo.name,
    'line_items[0][price_data][product_data][description]': tierInfo.features.join(' · '),
    'line_items[0][price_data][unit_amount]': String(tierInfo.priceCents),
    'line_items[0][price_data][recurring][interval]': tierInfo.interval,
    'line_items[0][quantity]': '1',
    'metadata[user_id]': params.userId,
    'metadata[plan]': params.plan,
    'metadata[type]': 'saas_subscription',
    'subscription_data[metadata][user_id]': params.userId,
    'subscription_data[metadata][plan]': params.plan,
    'subscription_data[metadata][type]': 'saas_subscription',
  }

  if (params.customerId) {
    body['customer'] = params.customerId
  } else {
    body['customer_email'] = params.userEmail
  }

  const data = await stripeRequest('/checkout/sessions', secretKey, 'POST', body) as { id: string; url: string }
  return { id: data.id, url: data.url }
}

/**
 * Report metered usage to Stripe for a subscription item.
 * Used for overage billing on API requests, scans, and bandwidth.
 */
export async function reportUsage(
  secretKey: string,
  subscriptionItemId: string,
  quantity: number,
  timestamp?: number,
): Promise<void> {
  const body: Record<string, string> = {
    quantity: String(quantity),
    action: 'increment',
  }
  if (timestamp) {
    body['timestamp'] = String(timestamp)
  }

  await stripeRequest(
    `/subscription_items/${subscriptionItemId}/usage_records`,
    secretKey,
    'POST',
    body,
  )
}

/**
 * Retrieve a Stripe subscription by ID.
 */
export async function getSubscription(
  secretKey: string,
  subscriptionId: string,
): Promise<Record<string, unknown>> {
  return await stripeRequest(`/subscriptions/${subscriptionId}`, secretKey) as Record<string, unknown>
}

/**
 * Cancel a Stripe subscription at period end.
 */
export async function cancelSubscription(
  secretKey: string,
  subscriptionId: string,
): Promise<void> {
  await stripeRequest(`/subscriptions/${subscriptionId}`, secretKey, 'POST', {
    cancel_at_period_end: 'true',
  })
}

// Verify Stripe webhook signature (crypto.subtle compatible)
export async function verifyWebhookSignature(
  payload: string,
  signature: string,
  secret: string,
): Promise<boolean> {
  const parts = signature.split(',').reduce<Record<string, string>>((acc, part) => {
    const [k, v] = part.split('=')
    acc[k] = v
    return acc
  }, {})

  const timestamp = parts['t']
  const sig = parts['v1']
  if (!timestamp || !sig) return false

  // Check timestamp tolerance (5 minutes)
  const ts = parseInt(timestamp, 10)
  if (Math.abs(Date.now() / 1000 - ts) > 300) return false

  const signedPayload = `${timestamp}.${payload}`
  const key = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  )
  const expected = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(signedPayload))

  const expectedHex = Array.from(new Uint8Array(expected))
    .map(b => b.toString(16).padStart(2, '0'))
    .join('')

  return expectedHex === sig
}
