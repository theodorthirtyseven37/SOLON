import type { CloudProvider, CreateServerOpts, ProviderServerResult, ProviderServerInfo } from './types'

// Verda (formerly DataCrunch) — Helsinki, Finland. 100% renewable energy.
// API docs: https://docs.verda.com/api
const VERDA_API = 'https://api.verda.com/v1'

export class VerdaProvider implements CloudProvider {
  readonly name = 'verda' as const
  private token: string

  constructor(token: string) {
    this.token = token
  }

  async createServer(opts: CreateServerOpts): Promise<ProviderServerResult> {
    const body = {
      hostname: opts.name,
      instance_type: opts.gpu.providerServerType,
      image: 'ubuntu-24.04',
      location: opts.region || opts.gpu.region,
      startup_script: opts.userData,
      description: `Solon managed instance ${opts.instanceId}`,
      metadata: opts.labels,
    }

    const resp = await this.request('/instances', 'POST', body) as VerdaCreateResponse
    return {
      serverId: resp.id,
      name: resp.hostname,
      ipv4: resp.ip,
      status: resp.status,
    }
  }

  async deleteServer(serverId: string): Promise<void> {
    await this.request(`/instances/${serverId}`, 'DELETE')
  }

  async findServer(instanceId: string): Promise<ProviderServerInfo | null> {
    // Verda supports filtering by metadata
    const resp = await this.request('/instances') as { instances: VerdaInstance[] }

    const server = resp.instances.find(
      (i) => i.metadata?.solon_instance_id === instanceId,
    )
    if (!server) return null

    return {
      serverId: server.id,
      ipv4: server.ip,
      status: server.status,
    }
  }

  private async request(path: string, method = 'GET', body?: Record<string, unknown>): Promise<unknown> {
    const resp = await fetch(`${VERDA_API}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${this.token}`,
        'Content-Type': 'application/json',
      },
      body: body ? JSON.stringify(body) : undefined,
    })

    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Verda API error ${resp.status}: ${text}`)
    }
    if (resp.status === 204) return null
    return resp.json()
  }
}

interface VerdaInstance {
  id: string
  hostname: string
  ip: string
  status: string
  metadata?: Record<string, string>
}

interface VerdaCreateResponse {
  id: string
  hostname: string
  ip: string
  status: string
}
