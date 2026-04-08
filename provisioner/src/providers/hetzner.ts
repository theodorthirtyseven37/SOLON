import type { CloudProvider, CreateServerOpts, ProviderServerResult, ProviderServerInfo } from './types'

const HETZNER_API = 'https://api.hetzner.cloud/v1'

export class HetznerProvider implements CloudProvider {
  readonly name = 'hetzner' as const
  private token: string

  constructor(token: string) {
    this.token = token
  }

  async createServer(opts: CreateServerOpts): Promise<ProviderServerResult> {
    const body = {
      name: opts.name,
      server_type: opts.gpu.providerServerType,
      image: 'ubuntu-24.04',
      location: opts.region || opts.gpu.region,
      user_data: opts.userData,
      labels: opts.labels,
      public_net: { enable_ipv4: true, enable_ipv6: true },
      start_after_create: true,
    }

    const resp = await this.request('/servers', 'POST', body) as HetznerCreateResponse
    return {
      serverId: String(resp.server.id),
      name: resp.server.name,
      ipv4: resp.server.public_net?.ipv4?.ip,
      status: resp.server.status,
    }
  }

  async deleteServer(serverId: string): Promise<void> {
    await this.request(`/servers/${serverId}`, 'DELETE')
  }

  async findServer(instanceId: string): Promise<ProviderServerInfo | null> {
    const resp = await this.request(
      `/servers?label_selector=solon_instance_id=${instanceId}`,
    ) as { servers: HetznerServer[] }

    const server = resp.servers[0]
    if (!server) return null

    return {
      serverId: String(server.id),
      ipv4: server.public_net.ipv4.ip,
      status: server.status,
    }
  }

  private async request(path: string, method = 'GET', body?: Record<string, unknown>): Promise<unknown> {
    const resp = await fetch(`${HETZNER_API}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${this.token}`,
        'Content-Type': 'application/json',
      },
      body: body ? JSON.stringify(body) : undefined,
    })

    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Hetzner API error ${resp.status}: ${text}`)
    }
    if (resp.status === 204) return null
    return resp.json()
  }
}

// Hetzner-specific response types
interface HetznerServer {
  id: number
  name: string
  status: string
  public_net: { ipv4: { ip: string }; ipv6: { ip: string } }
  server_type: { name: string }
}

interface HetznerCreateResponse {
  server: HetznerServer
}
