import { Hono } from 'hono'
import type { Env, ProvisionRequest, CallbackPayload } from './types'
import { TIER_SERVER_TYPES, REGION_LOCATIONS } from './types'
import { verifyHMAC, signHMAC } from './hmac'
import { createServer, deleteServer, findServerByInstanceId } from './hetzner'
import { generateCloudInit } from './cloud-init'

const app = new Hono<{ Bindings: Env }>()

// Health check
app.get('/health', (c) => c.json({ status: 'ok', service: 'solon-provisioner' }))

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
    // Acknowledge immediately, do provisioning in background
    c.executionCtx.waitUntil(handleCreate(c.env, request))
    return c.json({ received: true, action: 'create', instance_id: request.instance_id })
  }

  if (request.action === 'delete') {
    c.executionCtx.waitUntil(handleDelete(c.env, request))
    return c.json({ received: true, action: 'delete', instance_id: request.instance_id })
  }

  return c.json({ error: `Unknown action: ${request.action}` }, 400)
})

async function handleCreate(env: Env, request: ProvisionRequest): Promise<void> {
  console.log(`handleCreate called for instance ${request.instance_id}`)

  const tier = request.tier ?? 'starter'
  const region = request.region ?? 'eu-central'
  const rawName = request.name ?? `solon-${request.instance_id.slice(0, 8)}`
  const name = rawName.replace(/[^a-z0-9-]/gi, '').slice(0, 63).toLowerCase()

  if (!TIER_SERVER_TYPES[tier]) {
    console.error(`Invalid tier: ${tier}`)
    await sendCallback(env, { instance_id: request.instance_id, status: 'failed', error: `Invalid tier: ${tier}` })
    return
  }
  if (!REGION_LOCATIONS[region]) {
    console.error(`Invalid region: ${region}`)
    await sendCallback(env, { instance_id: request.instance_id, status: 'failed', error: `Invalid region: ${region}` })
    return
  }

  const serverType = TIER_SERVER_TYPES[tier]
  const location = REGION_LOCATIONS[region]
  console.log(`Creating server: type=${serverType} location=${location} name=solon-managed-${name}`)

  const userData = generateCloudInit(env, {
    instanceId: request.instance_id,
    tier,
    callbackSecret: env.CLOUD_API_CALLBACK_SECRET,
  })

  // Resolve SSH key ID if configured (for debugging access)
  let sshKeyNames: string[] | undefined
  if (env.SSH_PUBLIC_KEY) {
    try {
      const keysResp = await fetch('https://api.hetzner.cloud/v1/ssh_keys', {
        headers: { Authorization: `Bearer ${env.HETZNER_API_TOKEN}` },
      })
      const keysData = (await keysResp.json()) as { ssh_keys: Array<{ id: number; name: string }> }
      if (keysData.ssh_keys.length > 0) {
        sshKeyNames = keysData.ssh_keys.map((k) => k.name)
        console.log(`Using SSH keys: ${sshKeyNames.join(', ')}`)
      }
    } catch (e) {
      console.warn('Could not fetch SSH keys:', e)
    }
  }

  try {
    const result = await createServer(env.HETZNER_API_TOKEN, {
      name: `solon-managed-${name}`,
      serverType,
      location,
      userData,
      sshKeyNames,
      labels: {
        service: 'solon-managed',
        solon_instance_id: request.instance_id,
        tier,
      },
    })

    console.log(
      `Created Hetzner server ${result.server.id} (${result.server.name}) for instance ${request.instance_id}`,
    )
  } catch (err) {
    console.error(`Failed to create server for instance ${request.instance_id}:`, err)

    // Callback with failure
    await sendCallback(env, {
      instance_id: request.instance_id,
      status: 'failed',
      error: err instanceof Error ? err.message : 'Unknown error creating server',
    })
  }
}

async function handleDelete(env: Env, request: ProvisionRequest): Promise<void> {
  try {
    const server = await findServerByInstanceId(env.HETZNER_API_TOKEN, request.instance_id)
    if (!server) {
      console.log(`No Hetzner server found for instance ${request.instance_id}, marking as deleted`)
      await sendCallback(env, {
        instance_id: request.instance_id,
        status: 'deleted',
      })
      return
    }

    await deleteServer(env.HETZNER_API_TOKEN, server.id)
    console.log(`Deleted Hetzner server ${server.id} for instance ${request.instance_id}`)

    await sendCallback(env, {
      instance_id: request.instance_id,
      status: 'deleted',
    })
  } catch (err) {
    console.error(`Failed to delete server for instance ${request.instance_id}:`, err)
    // Still try to mark as deleted in the cloud API
    await sendCallback(env, {
      instance_id: request.instance_id,
      status: 'failed',
      error: err instanceof Error ? err.message : 'Unknown error deleting server',
    })
  }
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
