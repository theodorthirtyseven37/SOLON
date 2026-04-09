export interface Env {
  HETZNER_API_TOKEN: string
  PROVISIONER_SECRET: string
  CLOUD_API_URL: string
  CLOUD_API_CALLBACK_SECRET: string
  SSH_PUBLIC_KEY?: string
  ENVIRONMENT: string
}

export interface ProvisionRequest {
  action: 'create' | 'delete'
  instance_id: string
  tier?: string
  region?: string
  name?: string
}

export interface HetznerServerResponse {
  server: {
    id: number
    name: string
    status: string
    public_net: {
      ipv4: { ip: string }
      ipv6: { ip: string }
    }
    server_type: { name: string }
  }
}

export interface HetznerSSHKeyResponse {
  ssh_keys: Array<{ id: number; name: string }>
}

export interface CallbackPayload {
  instance_id: string
  status: 'running' | 'failed' | 'deleted'
  ipv4?: string
  solon_api_key?: string
  dashboard_url?: string
  error?: string
}

/** Maps tier names to Hetzner server types */
export const TIER_SERVER_TYPES: Record<string, string> = {
  starter: 'cx23',
  pro: 'cx43',
  gpu: 'gx11',
}

/** Maps region names to Hetzner locations */
export const REGION_LOCATIONS: Record<string, string> = {
  'eu-central': 'nbg1',
  'eu-west': 'nbg1',
  'eu-north': 'hel1',
  'us-east': 'ash',
  'us-west': 'hil',
  'asia': 'sin',
}
