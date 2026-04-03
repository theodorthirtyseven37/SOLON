// Stripe API helpers for Cloudflare Workers (uses fetch, not Node SDK)

const STRIPE_API = 'https://api.stripe.com/v1'

// Server tier definitions — each tier is a dedicated managed server sold per month
export interface ServerTier {
  name: string
  /** Price in cents. Monthly tiers: per month. Hourly tiers: per hour. */
  priceCents: number
  billing: 'monthly' | 'hourly'
  provider: 'hetzner' | 'datacrunch'
  serverType: string
  description: string
  features: string[]
  vcpu: number
  ramGb: number
  diskGb: number
  /** Max concurrent models (0 = unlimited) */
  models: number
  /** Max agent slots (0 = unlimited) */
  agents: number
  hasGpu: boolean
  gpuModel?: string
  gpuVramGb?: number
  /** Security tier bundled with server */
  security: 'basic' | 'standard' | 'full'
}

export const SERVER_TIERS: Record<string, ServerTier> = {
  starter: {
    name: 'Starter',
    priceCents: 2500,
    billing: 'monthly',
    provider: 'hetzner',
    serverType: 'cx22',
    description: 'Perfect for development and small workloads',
    features: ['2 vCPU / 4 GB RAM', '40 GB NVMe', '1 model', '2 agents', 'Tenant isolation', 'Automatic TLS', 'Weekly security scan'],
    vcpu: 2,
    ramGb: 4,
    diskGb: 40,
    models: 1,
    agents: 2,
    hasGpu: false,
    security: 'basic',
  },
  pro: {
    name: 'Pro',
    priceCents: 4900,
    billing: 'monthly',
    provider: 'hetzner',
    serverType: 'cx42',
    description: 'For production workloads with higher throughput',
    features: ['4 vCPU / 16 GB RAM', '80 GB NVMe', '5 models', '10 agents', 'Multi-model routing', 'WAF + Tenant isolation', 'Daily security scan', 'Request logging'],
    vcpu: 4,
    ramGb: 16,
    diskGb: 80,
    models: 5,
    agents: 10,
    hasGpu: false,
    security: 'standard',
  },
  gpu: {
    name: 'GPU',
    priceCents: 29900,
    billing: 'monthly',
    provider: 'hetzner',
    serverType: 'gx11',
    description: 'Dedicated GPU for local inference with NVIDIA hardware',
    features: ['8 vCPU / 32 GB RAM', 'NVIDIA L4 GPU', '160 GB NVMe', 'Unlimited models', 'Unlimited agents', 'Custom model deployment', 'WAF + Full monitoring', 'Continuous security scan'],
    vcpu: 8,
    ramGb: 32,
    diskGb: 160,
    models: 0,
    agents: 0,
    hasGpu: true,
    gpuModel: 'NVIDIA L4',
    gpuVramGb: 24,
    security: 'full',
  },
  'gpu-a100': {
    name: 'GPU A100',
    priceCents: 549,
    billing: 'hourly',
    provider: 'datacrunch',
    serverType: 'a100-80g',
    description: 'High-performance A100 GPU for large models',
    features: ['16 vCPU / 120 GB RAM', 'NVIDIA A100 80GB', '200 GB NVMe', 'Unlimited models', 'Unlimited agents', 'Full monitoring + WAF', 'Continuous security scan'],
    vcpu: 16,
    ramGb: 120,
    diskGb: 200,
    models: 0,
    agents: 0,
    hasGpu: true,
    gpuModel: 'NVIDIA A100',
    gpuVramGb: 80,
    security: 'full',
  },
  'gpu-h100': {
    name: 'GPU H100',
    priceCents: 849,
    billing: 'hourly',
    provider: 'datacrunch',
    serverType: 'h100-80g',
    description: 'Top-tier H100 GPU for the largest models',
    features: ['24 vCPU / 240 GB RAM', 'NVIDIA H100 80GB', '400 GB NVMe', 'Unlimited models', 'Unlimited agents', 'Full monitoring + WAF', 'Continuous security scan'],
    vcpu: 24,
    ramGb: 240,
    diskGb: 400,
    models: 0,
    agents: 0,
    hasGpu: true,
    gpuModel: 'NVIDIA H100',
    gpuVramGb: 80,
    security: 'full',
  },
}

/** Backwards compat alias */
export const MANAGED_TIERS = SERVER_TIERS

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

/**
 * Create a Stripe Checkout session for a managed server subscription.
 */
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
  const tierInfo = SERVER_TIERS[params.tier]
  if (!tierInfo) throw new Error(`Unknown tier: ${params.tier}`)

  const interval = tierInfo.billing === 'hourly' ? 'month' : 'month'
  // For hourly tiers, we bill monthly with estimated hours (730/mo)
  const unitAmount = tierInfo.billing === 'hourly'
    ? tierInfo.priceCents * 730
    : tierInfo.priceCents

  const data = await stripeRequest('/checkout/sessions', secretKey, 'POST', {
    'mode': 'subscription',
    'success_url': params.successUrl,
    'cancel_url': params.cancelUrl,
    'customer_email': params.userEmail,
    'line_items[0][price_data][currency]': 'usd',
    'line_items[0][price_data][product_data][name]': `Solon ${tierInfo.name} Server`,
    'line_items[0][price_data][product_data][description]': tierInfo.description,
    'line_items[0][price_data][unit_amount]': String(unitAmount),
    'line_items[0][price_data][recurring][interval]': interval,
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
