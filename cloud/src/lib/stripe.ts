// Stripe API helpers for Cloudflare Workers (uses fetch, not Node SDK)

const STRIPE_API = 'https://api.stripe.com/v1'

// GPU tier pricing — maps gpu_tier IDs to Stripe checkout config.
// Monthly tiers use fixed subscription pricing. Hourly tiers use metered billing.
export const GPU_TIER_PRICING: Record<string, {
  name: string
  description: string
  /** Price in cents. For monthly: total/mo. For hourly: per-hour rate. */
  price: number
  billingModel: 'monthly' | 'hourly'
  provider: string
}> = {
  // Hetzner — monthly billing, budget entry
  'hetzner-rtx4000': {
    name: 'GPU Starter — RTX 4000 SFF (20 GB)',
    description: 'Hetzner, Falkenstein DE. Good for 7-14B models.',
    price: 20000, // ~$200/mo
    billingModel: 'monthly',
    provider: 'hetzner',
  },
  // Scaleway — hourly billing
  'scaleway-l4': {
    name: 'GPU L4 (24 GB)',
    description: 'Scaleway, Paris FR. Good for 7-14B models.',
    price: 75, // $0.75/hr
    billingModel: 'hourly',
    provider: 'scaleway',
  },
  'scaleway-l40s': {
    name: 'GPU L40S (48 GB)',
    description: 'Scaleway, Paris FR. Good for up to 70B Q4 models.',
    price: 140, // $1.40/hr
    billingModel: 'hourly',
    provider: 'scaleway',
  },
  'scaleway-h100': {
    name: 'GPU H100 (80 GB)',
    description: 'Scaleway, Paris FR. High-end inference.',
    price: 273, // $2.73/hr
    billingModel: 'hourly',
    provider: 'scaleway',
  },
  // Verda (ex-DataCrunch) — hourly billing, Finland
  'verda-a100-80': {
    name: 'GPU A100 80GB',
    description: 'Verda, Helsinki FI. 100% renewable energy.',
    price: 189, // $1.89/hr
    billingModel: 'hourly',
    provider: 'verda',
  },
  'verda-h100': {
    name: 'GPU H100 (80 GB)',
    description: 'Verda, Helsinki FI. 100% renewable energy.',
    price: 229, // $2.29/hr
    billingModel: 'hourly',
    provider: 'verda',
  },
  'verda-h200': {
    name: 'GPU H200 (141 GB)',
    description: 'Verda, Helsinki FI. Largest single-GPU VRAM.',
    price: 329, // $3.29/hr
    billingModel: 'hourly',
    provider: 'verda',
  },
}

// Legacy tiers kept for backwards compatibility
export const MANAGED_TIERS: Record<string, { name: string; price: number; serverType: string }> = {
  starter: { name: 'Starter', price: 2900, serverType: 'cx22' },
  pro: { name: 'Pro', price: 5900, serverType: 'cx42' },
  gpu: { name: 'GPU', price: 34900, serverType: 'gx11' },
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
    tier: string       // legacy tier OR gpu_tier ID
    gpuTier?: string   // explicit gpu_tier ID (preferred)
    region: string
    instanceName: string
    successUrl: string
    cancelUrl: string
  },
): Promise<{ id: string; url: string }> {
  // Prefer new gpu_tier, fall back to legacy tier
  const gpuTierId = params.gpuTier
  const gpuTierInfo = gpuTierId ? GPU_TIER_PRICING[gpuTierId] : undefined

  if (gpuTierInfo) {
    // New GPU tier checkout
    const isMonthly = gpuTierInfo.billingModel === 'monthly'
    const checkoutParams: Record<string, string> = {
      'mode': 'subscription',
      'success_url': params.successUrl,
      'cancel_url': params.cancelUrl,
      'customer_email': params.userEmail,
      'line_items[0][price_data][currency]': 'usd',
      'line_items[0][price_data][product_data][name]': `Solon GPU — ${gpuTierInfo.name}`,
      'line_items[0][price_data][product_data][description]': gpuTierInfo.description,
      'line_items[0][price_data][unit_amount]': String(gpuTierInfo.price),
      'line_items[0][price_data][recurring][interval]': isMonthly ? 'month' : 'hour',
      'line_items[0][quantity]': '1',
      'metadata[user_id]': params.userId,
      'metadata[gpu_tier]': gpuTierId!,
      'metadata[provider]': gpuTierInfo.provider,
      'metadata[region]': params.region,
      'metadata[instance_name]': params.instanceName,
      'subscription_data[metadata][user_id]': params.userId,
      'subscription_data[metadata][gpu_tier]': gpuTierId!,
      'subscription_data[metadata][provider]': gpuTierInfo.provider,
    }

    // Hourly billing uses metered usage reporting
    if (!isMonthly) {
      checkoutParams['line_items[0][price_data][recurring][interval]'] = 'month'
      checkoutParams['line_items[0][price_data][recurring][usage_type]'] = 'metered'
    }

    const data = await stripeRequest('/checkout/sessions', secretKey, 'POST', checkoutParams) as { id: string; url: string }
    return { id: data.id, url: data.url }
  }

  // Legacy tier fallback
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
