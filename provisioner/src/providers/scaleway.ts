import type { CloudProvider, CreateServerOpts, ProviderServerResult, ProviderServerInfo } from './types'

// Scaleway — Paris & Warsaw. EU-headquartered, GDPR native.
// API docs: https://www.scaleway.com/en/developers/api/
const SCALEWAY_API = 'https://api.scaleway.com'

export class ScalewayProvider implements CloudProvider {
  readonly name = 'scaleway' as const
  private token: string
  private projectId: string

  constructor(token: string, projectId: string) {
    this.token = token
    this.projectId = projectId
  }

  async createServer(opts: CreateServerOpts): Promise<ProviderServerResult> {
    const zone = opts.region || opts.gpu.region
    const body = {
      name: opts.name,
      commercial_type: opts.gpu.providerServerType,
      image: await this.getUbuntuImageId(zone),
      project: this.projectId,
      tags: Object.entries(opts.labels).map(([k, v]) => `${k}=${v}`),
      // Scaleway uses cloud-init via user_data on the instance
    }

    const resp = await this.request(
      `/instance/v1/zones/${zone}/servers`,
      'POST',
      body,
    ) as ScalewayCreateResponse

    const serverId = resp.server.id

    // Set user_data (cloud-init) — Scaleway requires a separate call
    await this.setUserData(zone, serverId, opts.userData)

    // Boot the server
    await this.request(
      `/instance/v1/zones/${zone}/servers/${serverId}/action`,
      'POST',
      { action: 'poweron' },
    )

    return {
      serverId,
      name: resp.server.name,
      ipv4: resp.server.public_ip?.address,
      status: resp.server.state,
    }
  }

  async deleteServer(serverId: string): Promise<void> {
    // Scaleway requires zone — we try all GPU zones
    for (const zone of ['fr-par-1', 'fr-par-2', 'pl-waw-2']) {
      try {
        // Terminate first, then delete
        await this.request(
          `/instance/v1/zones/${zone}/servers/${serverId}/action`,
          'POST',
          { action: 'terminate' },
        )
        return
      } catch {
        // Server not in this zone, try next
      }
    }
    throw new Error(`Could not find Scaleway server ${serverId} in any zone`)
  }

  async findServer(instanceId: string): Promise<ProviderServerInfo | null> {
    // Search across GPU zones
    for (const zone of ['fr-par-1', 'fr-par-2', 'pl-waw-2']) {
      const resp = await this.request(
        `/instance/v1/zones/${zone}/servers?tags=solon_instance_id=${instanceId}&project=${this.projectId}`,
      ) as { servers: ScalewayServer[] }

      const server = resp.servers[0]
      if (server) {
        return {
          serverId: server.id,
          ipv4: server.public_ip?.address ?? '',
          status: server.state,
        }
      }
    }
    return null
  }

  private async setUserData(zone: string, serverId: string, userData: string): Promise<void> {
    // Scaleway user_data is set via a separate endpoint with text/plain content type
    const resp = await fetch(
      `${SCALEWAY_API}/instance/v1/zones/${zone}/servers/${serverId}/user_data/cloud-init`,
      {
        method: 'PATCH',
        headers: {
          'X-Auth-Token': this.token,
          'Content-Type': 'text/plain',
        },
        body: userData,
      },
    )
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Scaleway user_data error ${resp.status}: ${text}`)
    }
  }

  private async getUbuntuImageId(zone: string): Promise<string> {
    const resp = await this.request(
      `/marketplace/v2/images?page_size=20`,
    ) as { images: Array<{ id: string; label: string; versions: Array<{ local_images: Array<{ id: string; zone: string }> }> }> }

    for (const img of resp.images) {
      if (img.label.toLowerCase().includes('ubuntu') && img.label.includes('24.04')) {
        for (const ver of img.versions) {
          const local = ver.local_images.find(li => li.zone === zone)
          if (local) return local.id
        }
      }
    }
    throw new Error(`Ubuntu 24.04 image not found for zone ${zone}`)
  }

  private async request(path: string, method = 'GET', body?: Record<string, unknown>): Promise<unknown> {
    const resp = await fetch(`${SCALEWAY_API}${path}`, {
      method,
      headers: {
        'X-Auth-Token': this.token,
        'Content-Type': 'application/json',
      },
      body: body ? JSON.stringify(body) : undefined,
    })

    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Scaleway API error ${resp.status}: ${text}`)
    }
    if (resp.status === 204) return null
    return resp.json()
  }
}

interface ScalewayServer {
  id: string
  name: string
  state: string
  public_ip?: { address: string }
  tags: string[]
}

interface ScalewayCreateResponse {
  server: ScalewayServer
}
