import type { Env } from './types'

/**
 * Generate a cloud-init user_data script that:
 * 1. Installs Docker
 * 2. Downloads and installs the Solon binary
 * 3. Configures and starts Solon as a systemd service
 * 4. Generates an admin API key
 * 5. Calls back to the cloud API with the server's IP and API key
 *
 * If any step fails, it calls back with status=failed.
 */
export function generateCloudInit(
  env: Env,
  opts: {
    instanceId: string
    tier: string
    callbackSecret: string
  },
): string {
  const callbackUrl = `${env.CLOUD_API_URL}/api/webhooks/provisioner`
  const solon_github_repo = 'theodorthirtyseven37/SOLON'

  return `#!/bin/bash
set -euo pipefail
exec > /var/log/solon-provision.log 2>&1
echo "[$(date)] Starting Solon provisioning for instance ${opts.instanceId}"

INSTANCE_ID="${opts.instanceId}"
TIER="${opts.tier}"
CALLBACK_URL="${callbackUrl}"
CALLBACK_SECRET="${opts.callbackSecret}"
GITHUB_REPO="${solon_github_repo}"

# --- Callback helper ---
send_callback() {
  local status="$1"
  local ipv4="\${2:-}"
  local api_key="\${3:-}"
  local error_msg="\${4:-}"
  local dashboard_url=""
  if [ -n "$ipv4" ]; then
    dashboard_url="http://$ipv4:8420"
  fi

  local payload
  payload=$(cat <<CBJSON
{"instance_id":"$INSTANCE_ID","status":"$status","ipv4":"$ipv4","solon_api_key":"$api_key","dashboard_url":"$dashboard_url","error":"$error_msg"}
CBJSON
)

  local timestamp
  timestamp=$(date +%s)
  local sig
  sig=$(echo -n "$timestamp.$payload" | openssl dgst -sha256 -hmac "$CALLBACK_SECRET" -hex | awk '{print $NF}')

  curl -sf -X POST "$CALLBACK_URL" \\
    -H "Content-Type: application/json" \\
    -H "X-Signature: t=$timestamp,v1=$sig" \\
    -d "$payload" || echo "[$(date)] WARNING: Callback failed"
}

# On failure, report back
trap 'send_callback "failed" "" "" "Provisioning script failed at line $LINENO"' ERR

# --- System updates ---
echo "[$(date)] Updating system packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get upgrade -yqq

# --- Install Docker ---
echo "[$(date)] Installing Docker"
apt-get install -yqq ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
apt-get update -qq
apt-get install -yqq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker

# --- Basic hardening ---
echo "[$(date)] Applying basic hardening"
apt-get install -yqq ufw fail2ban
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp    # SSH
ufw allow 8420/tcp  # Solon API/Dashboard
ufw allow from 172.16.0.0/12 to any port 8420 proto tcp  # Docker containers
ufw --force enable
systemctl enable --now fail2ban

# --- Create solon user ---
echo "[$(date)] Creating solon user"
groupadd --system solon || true
useradd --system --gid solon --shell /usr/sbin/nologin --home-dir /var/lib/solon --no-create-home solon || true
usermod -aG docker solon

# --- Create directories ---
mkdir -p /opt/solon/bin /etc/solon /var/lib/solon/logs
chown -R solon:solon /opt/solon /etc/solon /var/lib/solon

# --- Download Solon binary ---
echo "[$(date)] Downloading Solon binary"
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH_SUFFIX="amd64" ;;
  aarch64) ARCH_SUFFIX="arm64" ;;
  *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

RELEASE_TAG=$(curl -sf "https://api.github.com/repos/$GITHUB_REPO/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
if [ -z "$RELEASE_TAG" ]; then
  echo "[$(date)] Failed to get latest release tag"
  send_callback "failed" "" "" "Failed to get latest Solon release"
  exit 1
fi

curl -sfL "https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/solon-linux-$ARCH_SUFFIX" -o /opt/solon/bin/solon
chmod 755 /opt/solon/bin/solon

# --- Write Solon config ---
echo "[$(date)] Writing Solon configuration"
cat > /etc/solon/config.yaml <<SOLONCFG
server:
  port: 8420
  host: "0.0.0.0"

data:
  dir: "/var/lib/solon"

logging:
  level: "info"
  dir: "/var/lib/solon/logs"

openclaw:
  enabled: true
SOLONCFG
chown solon:solon /etc/solon/config.yaml
chmod 640 /etc/solon/config.yaml

# --- Write managed instance marker ---
cat > /etc/solon/managed.yaml <<MARKER
managed: true
provisioned_at: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
tier: "$TIER"
instance_id: "$INSTANCE_ID"
MARKER
chown solon:solon /etc/solon/managed.yaml

# --- Create systemd service ---
echo "[$(date)] Creating systemd service"
cat > /etc/systemd/system/solon.service <<SVCFILE
[Unit]
Description=Solon AI Platform
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=solon
Group=solon
ExecStart=/opt/solon/bin/solon serve
WorkingDirectory=/var/lib/solon
Restart=on-failure
RestartSec=10
StartLimitIntervalSec=300
StartLimitBurst=5

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/solon /etc/solon

LimitNOFILE=65536
LimitNPROC=4096

Environment=NODE_ENV=production
Environment=SOLON_CONFIG=/etc/solon/config.yaml

StandardOutput=journal
StandardError=journal
SyslogIdentifier=solon

[Install]
WantedBy=multi-user.target
SVCFILE

systemctl daemon-reload
systemctl enable --now solon

# --- Wait for Solon to be ready ---
echo "[$(date)] Waiting for Solon to start"
for i in $(seq 1 60); do
  if curl -sf http://127.0.0.1:8420/api/v1/health > /dev/null 2>&1; then
    echo "[$(date)] Solon is healthy after $i attempts"
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "[$(date)] Solon failed to start"
    send_callback "failed" "" "" "Solon health check timed out after 120s"
    exit 1
  fi
  sleep 2
done

# --- Generate admin API key ---
echo "[$(date)] Generating admin API key"
API_KEY_OUTPUT=$(sudo -u solon /opt/solon/bin/solon keys create --name managed-admin --scope admin 2>&1)
API_KEY=$(echo "$API_KEY_OUTPUT" | grep -oP 'sol_sk_live_[a-zA-Z0-9_]+' || true)
if [ -z "$API_KEY" ]; then
  echo "[$(date)] ERROR: Could not extract API key from output: $API_KEY_OUTPUT"
  send_callback "failed" "" "" "Failed to extract API key from Solon keys command"
  exit 1
fi

# Save key locally for debugging
if [ -n "$API_KEY" ]; then
  echo "$API_KEY" > /etc/solon/initial-key
  chmod 600 /etc/solon/initial-key
fi

# --- Docker network for sandboxes ---
echo "[$(date)] Setting up Docker network"
docker network create solon-bridge 2>/dev/null || true
docker pull node:22-slim

# --- Get public IPv4 ---
PUBLIC_IP=$(curl -sf https://api.ipify.org || curl -sf http://169.254.169.254/hetzner/v1/metadata/public-ipv4 || hostname -I | awk '{print $1}')

# --- Callback: server is ready ---
echo "[$(date)] Provisioning complete. Sending callback."
send_callback "running" "$PUBLIC_IP" "$API_KEY"

echo "[$(date)] Provisioning finished successfully"
`
}
