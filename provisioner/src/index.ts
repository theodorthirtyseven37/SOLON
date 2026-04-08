import { Hono } from 'hono'
import type { Env, ProvisionRequest, CallbackPayload } from './types'
import { LEGACY_TIER_MAP } from './types'
import { verifyHMAC, signHMAC } from './hmac'
import { generateCloudInit } from './cloud-init'
import { resolveProvider, getTierById, getGpuVendor, GPU_TIERS } from './providers'
import type { GpuTier } from './providers'

const app = new Hono<{ Bindings: Env }>()

// Health check
app.get('/health', (c) => c.json({ status: 'ok', service: 'solon-provisioner' }))

// List available GPU tiers (for dashboard to display options)
app.get('/api/tiers', (c) => {
  const tiers = GPU_TIERS.map((t) => ({
    id: t.id,
    name: t.name,
    provider: t.provider,
    gpuId: t.gpuId,
    gpuCount: t.gpuCount,
    region: t.region,
    priceMonthly: t.priceMonthly,
    priceHourly: t.priceHourly,
    billingModel: t.billingModel,
    fitsModels: t.fitsModels,
  }))
  return c.json({ tiers })
})

// POST /webhook/provision — Receive provisioning requests from cloud API
app.post('/webhook/provision', async (c) => {
  const signatureHeader = c.req.header('x-signature')
  if (!signatureHeader) {
    return c.json({ error: 'Missing X-Signature header' }, 400)
  }

  const body = await c.req.text()

  const valid = await verifyHMAC(body, signatureHeader, c.env.PROVISIONER_SECRET)
  if (!valid) {
    return c.json({ error: 'Invalid signature' }, 401)
  }

  const request = JSON.parse(body) as ProvisionRequest

  if (request.action === 'create') {
    c.executionCtx.waitUntil(handleCreate(c.env, request))
    return c.json({ received: true, action: 'create', instance_id: request.instance_id })
  }

  if (request.action === 'delete') {
    c.executionCtx.waitUntil(handleDelete(c.env, request))
    return c.json({ received: true, action: 'delete', instance_id: request.instance_id })
  }

  return c.json({ error: `Unknown action: ${request.action}` }, 400)
})

/** Resolve which GPU tier to use from the request. */
function resolveTier(request: ProvisionRequest): GpuTier {
  // New-style: explicit gpu_tier
  if (request.gpu_tier) {
    const tier = getTierById(request.gpu_tier)
    if (!tier) throw new Error(`Unknown GPU tier: ${request.gpu_tier}`)
    return tier
  }

  // Legacy: map old tier names to GPU tiers
  if (request.tier) {
    const mappedId = LEGACY_TIER_MAP[request.tier]
    if (!mappedId) throw new Error(`Unknown legacy tier: ${request.tier}`)
    const tier = getTierById(mappedId)
    if (!tier) throw new Error(`Legacy tier mapped to unknown GPU tier: ${mappedId}`)
    return tier
  }

  // Default: cheapest option (Hetzner RTX 4000)
  const tier = getTierById('hetzner-rtx4000')
  if (!tier) throw new Error('Default tier not found')
  return tier
}

async function handleCreate(env: Env, request: ProvisionRequest): Promise<void> {
  const gpuTier = resolveTier(request)
  const region = request.region || gpuTier.region
  const rawName = request.name ?? `solon-${request.instance_id.slice(0, 8)}`
  const name = rawName.replace(/[^a-z0-9-]/gi, '').slice(0, 63).toLowerCase()

  const gpuVendor = getGpuVendor(gpuTier)
  const provider = resolveProvider(gpuTier.provider, env)

  const userData = generateCloudInit(env, {
    instanceId: request.instance_id,
    tier: gpuTier.id,
    gpuVendor,
    callbackSecret: env.CLOUD_API_CALLBACK_SECRET,
  })

  try {
    const result = await provider.createServer({
      name: `solon-managed-${name}`,
      instanceId: request.instance_id,
      gpu: gpuTier,
      region,
      userData,
      labels: {
        service: 'solon-managed',
        solon_instance_id: request.instance_id,
        gpu_tier: gpuTier.id,
        provider: gpuTier.provider,
      },
    })

    console.log(
      `Created ${gpuTier.provider} server ${result.serverId} (${result.name}) ` +
      `for instance ${request.instance_id} [tier=${gpuTier.id}]`,
    )
  } catch (err) {
    console.error(
      `Failed to create ${gpuTier.provider} server for instance ${request.instance_id}:`,
      err,
    )

    await sendCallback(env, {
      instance_id: request.instance_id,
      status: 'failed',
      provider: gpuTier.provider,
      gpu_tier: gpuTier.id,
      error: err instanceof Error ? err.message : 'Unknown error creating server',
    })
  }
}

async function handleDelete(env: Env, request: ProvisionRequest): Promise<void> {
  // Try to find the server across all configured providers
  const providerNames = ['hetzner', 'verda', 'scaleway'] as const
  for (const provName of providerNames) {
    try {
      const provider = resolveProvider(provName, env)
      const server = await provider.findServer(request.instance_id)
      if (server) {
        await provider.deleteServer(server.serverId)
        console.log(
          `Deleted ${provName} server ${server.serverId} for instance ${request.instance_id}`,
        )
        await sendCallback(env, {
          instance_id: request.instance_id,
          status: 'deleted',
          provider: provName,
        })
        return
      }
    } catch {
      // Provider not configured or server not found here, try next
    }
  }

  // No server found on any provider — mark as deleted (idempotent)
  console.log(`No server found for instance ${request.instance_id} on any provider, marking deleted`)
  await sendCallback(env, {
    instance_id: request.instance_id,
    status: 'deleted',
  })
}

async function sendCallback(env: Env, payload: CallbackPayload): Promise<void> {
  const body = JSON.stringify(payload)
  const signature = await signHMAC(body, env.CLOUD_API_CALLBACK_SECRET)

  const resp = await fetch(`${env.CLOUD_API_URL}/api/webhooks/provisioner`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Signature': signature,
    },
    body,
  })

  if (!resp.ok) {
    console.error(`Callback failed: ${resp.status} ${await resp.text()}`)
  }
}

export default app
