package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/openclaw/solon/internal/storage"
)

// Manager manages sandbox lifecycle via Docker.
type Manager struct {
	docker      *dockerClient
	store       *storage.DB
	solonPort   int
	bridgeReady bool
	gatewayIP   string // solon-bridge gateway IP (host-accessible from containers)
}

// NewManager creates a sandbox manager. socketPath is the Docker unix socket
// (typically /var/run/docker.sock). solonPort is the port Solon listens on.
func NewManager(socketPath string, store *storage.DB, solonPort int) *Manager {
	return &Manager{
		docker:    newDockerClient(socketPath),
		store:     store,
		solonPort: solonPort,
	}
}

// EnsureNetwork creates the solon-bridge Docker network if it doesn't exist
// and caches the gateway IP for container environment variables.
func (m *Manager) EnsureNetwork(ctx context.Context) error {
	if m.bridgeReady {
		return nil
	}
	if err := m.docker.networkCreate(ctx, NetworkName); err != nil {
		return fmt.Errorf("ensuring Docker network: %w", err)
	}
	// Detect the gateway IP for this network
	if gw := m.docker.networkGateway(ctx, NetworkName); gw != "" {
		m.gatewayIP = gw
	}
	m.bridgeReady = true
	return nil
}

// EnsureTierNetworks creates the tier-specific Docker networks if they don't exist.
// solon-tier1 is internal (no outbound), solon-tier2 is a regular bridge.
func (m *Manager) EnsureTierNetworks(ctx context.Context) error {
	// Tier 1: internal network — kernel-level outbound block
	if err := m.docker.networkCreateWithOpts(ctx, NetworkTier1, true); err != nil {
		return fmt.Errorf("creating tier-1 network: %w", err)
	}
	// Tier 2-3: regular bridge with outbound access
	if err := m.docker.networkCreate(ctx, NetworkTier2); err != nil {
		return fmt.Errorf("creating tier-2 network: %w", err)
	}
	return nil
}

// EnsureSandboxImage ensures the Playwright-ready sandbox image is available.
// It first tries to pull from GHCR, then falls back to building locally.
func (m *Manager) EnsureSandboxImage(ctx context.Context) error {
	// Check if image already exists locally
	if m.docker.imageExists(ctx, SandboxImage) {
		return nil
	}

	// Try pulling pre-built image from GHCR
	log.Println("[sandbox] Pulling sandbox image from GHCR...")
	if err := m.docker.imagePull(ctx, GHCRSandboxImage); err == nil {
		// Tag as local name for compatibility
		tagResp, tagErr := m.docker.do(ctx, "POST",
			"/images/"+strings.ReplaceAll(GHCRSandboxImage, "/", "%2F")+"/tag?repo=solon/sandbox&tag=latest", nil)
		if tagErr == nil {
			_ = tagResp.Body.Close()
		}
		log.Println("[sandbox] Image pulled from GHCR: solon/sandbox:latest")
		return nil
	}
	log.Println("[sandbox] GHCR pull failed, building locally...")

	// Fallback: build locally
	return m.buildSandboxImageLocally(ctx)
}

// buildSandboxImageLocally builds the sandbox image from scratch using Docker exec + commit.
func (m *Manager) buildSandboxImageLocally(ctx context.Context) error {
	log.Println("[sandbox] Building sandbox image with Chromium + Playwright (this takes ~60s)...")

	tmpID, err := m.docker.containerCreate(ctx, containerConfig{
		Name: "solon-sandbox-build-tmp",
		Body: map[string]any{
			"Image": DefaultImage,
			"Cmd":   []string{"sleep", "infinity"},
		},
	})
	if err != nil {
		return fmt.Errorf("creating build container: %w", err)
	}

	cleanup := func() {
		_ = m.docker.containerStop(ctx, tmpID, 2)
		_ = m.docker.containerRemove(ctx, tmpID)
	}

	if err := m.docker.containerStart(ctx, tmpID); err != nil {
		cleanup()
		return fmt.Errorf("starting build container: %w", err)
	}

	installCmd := []string{"sh", "-c",
		"apt-get update && apt-get install -y --no-install-recommends " +
			"chromium fonts-liberation libgbm1 libnss3 libxss1 libasound2 " +
			"ca-certificates curl && " +
			"rm -rf /var/lib/apt/lists/*",
	}
	if _, err := m.docker.containerExec(ctx, tmpID, installCmd, nil); err != nil {
		cleanup()
		return fmt.Errorf("installing chromium dependencies: %w", err)
	}

	_, err = m.docker.containerExec(ctx, tmpID,
		[]string{"npm", "install", "-g", "playwright"},
		nil,
	)
	if err != nil {
		cleanup()
		return fmt.Errorf("installing playwright: %w", err)
	}

	_, _ = m.docker.containerExec(ctx, tmpID,
		[]string{"sh", "-c", "echo 'export PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium' >> /etc/profile.d/playwright.sh"},
		nil,
	)

	commitResp, err := m.docker.do(ctx, "POST",
		"/commit?container="+tmpID+"&repo=solon/sandbox&tag=latest&pause=true",
		nil,
	)
	if err != nil {
		cleanup()
		return fmt.Errorf("committing sandbox image: %w", err)
	}
	_ = commitResp.Body.Close()

	cleanup()
	log.Println("[sandbox] Image built: solon/sandbox:latest")
	return nil
}

// Available returns true if Docker is accessible.
func (m *Manager) Available(ctx context.Context) bool {
	resp, err := m.docker.do(ctx, "GET", "/_ping", nil)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)

// Create creates a new sandbox. It creates a dedicated API key, a Docker
// container on the solon-bridge network, and records the sandbox in the database.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Sandbox, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("sandbox name is required")
	}
	if len(req.Name) < 2 || len(req.Name) > 63 || !validName.MatchString(req.Name) {
		return nil, fmt.Errorf("sandbox name must be 2-63 lowercase chars, numbers, hyphens (no leading/trailing hyphen)")
	}
	// Resolve tier: explicit tier takes precedence, then map from policy
	tier := req.Tier
	if tier > 0 {
		if !ValidTier(tier) {
			return nil, fmt.Errorf("invalid tier %d: must be 1-4", tier)
		}
	} else if req.Policy != "" {
		if !ValidPolicy(req.Policy) {
			return nil, fmt.Errorf("invalid policy %q: must be one of full, api-only, inference-only, custom", req.Policy)
		}
		tier = PolicyToTier(req.Policy)
	} else {
		req.Policy = "api-only"
		tier = Tier2Standard
	}

	// Ensure tier-specific networks exist
	if err := m.EnsureNetwork(ctx); err != nil {
		return nil, err
	}
	if err := m.EnsureTierNetworks(ctx); err != nil {
		return nil, err
	}

	// Resolve tier config
	tierCfg := TierConfigs[tier]

	// Build the Playwright-ready image for Tier 2+
	if tier >= Tier2Standard {
		if err := m.EnsureSandboxImage(ctx); err != nil {
			return nil, fmt.Errorf("preparing sandbox image: %w", err)
		}
	}

	// Create a dedicated API key for this sandbox
	key, err := m.store.CreateKeyWithOptions(storage.CreateKeyOptions{
		Name:  fmt.Sprintf("sandbox-%s", req.Name),
		Scope: "user",
	})
	if err != nil {
		return nil, fmt.Errorf("creating sandbox API key: %w", err)
	}

	image := req.Image
	if image == "" {
		image = tierCfg.Image
	}

	sandboxID := uuid.New().String()
	containerName := "openclaw-sandbox-" + req.Name

	// Build environment variables — use the bridge gateway IP so containers
	// can reach Solon on the host (host.docker.internal is unreliable on Linux)
	solonHost := m.gatewayIP
	if solonHost == "" {
		solonHost = "host.docker.internal"
	}
	env := []string{
		fmt.Sprintf("SOLON_API_KEY=%s", key.Raw),
		fmt.Sprintf("SOLON_ENDPOINT=http://%s:%d", solonHost, m.solonPort),
		"NODE_ENV=production",
	}
	for k, v := range req.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Build host config from tier
	hostConfig := map[string]any{
		"NetworkMode": tierCfg.Network,
		"CapDrop":     []string{"ALL"},
		"CapAdd":      tierCfg.CapAdd,
		"SecurityOpt": []string{"no-new-privileges"},
		"ExtraHosts":  []string{"host.docker.internal:host-gateway"},
	}

	if tierCfg.MemoryMB > 0 {
		hostConfig["Memory"] = tierCfg.MemoryMB * 1024 * 1024
	}

	if tierCfg.Persistent {
		volumeName := fmt.Sprintf("solon-sandbox-%s", req.Name)
		hostConfig["Binds"] = []string{volumeName + ":/data"}
	}

	// Create container
	cfg := containerConfig{
		Name: containerName,
		Body: map[string]any{
			"Image": image,
			"Env":   env,
			"Labels": map[string]string{
				LabelManaged:   "true",
				LabelSandboxID: sandboxID,
				LabelPolicy:    req.Policy,
			},
			"Cmd":          []string{"sleep", "infinity"},
			"AttachStdout": true,
			"AttachStderr": true,
			"HostConfig":   hostConfig,
		},
	}

	containerID, err := m.docker.containerCreate(ctx, cfg)
	if err != nil {
		_ = m.store.RevokeKey(key.ID)
		return nil, fmt.Errorf("creating Docker container: %w", err)
	}

	// Store sandbox config as JSON
	var configJSON *string
	sandboxCfg := &Config{
		Env:   req.Env,
		Image: image,
		Tier:  tier,
	}
	cfgBytes, err := json.Marshal(sandboxCfg)
	if err == nil {
		s := string(cfgBytes)
		configJSON = &s
	}

	// Save to database
	if err := m.store.CreateSandbox(sandboxID, req.Name, containerID, req.Policy, tier, key.ID, configJSON); err != nil {
		_ = m.docker.containerRemove(ctx, containerID)
		_ = m.store.RevokeKey(key.ID)
		return nil, fmt.Errorf("saving sandbox to database: %w", err)
	}

	now := time.Now()
	return &Sandbox{
		ID:          sandboxID,
		Name:        req.Name,
		ContainerID: containerID,
		Status:      StatusCreated,
		Policy:      req.Policy,
		Tier:        tier,
		APIKeyID:    key.ID,
		Config:      sandboxCfg,
		CreatedAt:   now,
	}, nil
}

// Start starts a sandbox container.
func (m *Manager) Start(ctx context.Context, id string) error {
	sb, err := m.store.GetSandbox(id)
	if err != nil {
		return fmt.Errorf("sandbox not found: %w", err)
	}
	if sb.ContainerID == "" {
		return fmt.Errorf("sandbox %s has no container", id)
	}

	if err := m.docker.containerStart(ctx, sb.ContainerID); err != nil {
		return fmt.Errorf("starting sandbox: %w", err)
	}

	if err := m.store.UpdateSandboxStatus(id, StatusRunning); err != nil {
		log.Printf("warning: failed to update sandbox status: %v", err)
	}
	return nil
}

// Stop stops a sandbox container.
func (m *Manager) Stop(ctx context.Context, id string) error {
	sb, err := m.store.GetSandbox(id)
	if err != nil {
		return fmt.Errorf("sandbox not found: %w", err)
	}
	if sb.ContainerID == "" {
		return fmt.Errorf("sandbox %s has no container", id)
	}

	if err := m.docker.containerStop(ctx, sb.ContainerID, 10); err != nil {
		return fmt.Errorf("stopping sandbox: %w", err)
	}

	if err := m.store.UpdateSandboxStatus(id, StatusStopped); err != nil {
		log.Printf("warning: failed to update sandbox status: %v", err)
	}
	return nil
}

// Remove removes a sandbox container and revokes its API key.
func (m *Manager) Remove(ctx context.Context, id string) error {
	sb, err := m.store.GetSandbox(id)
	if err != nil {
		return fmt.Errorf("sandbox not found: %w", err)
	}

	// Remove container (force)
	if sb.ContainerID != "" {
		if err := m.docker.containerRemove(ctx, sb.ContainerID); err != nil {
			log.Printf("warning: failed to remove container %s: %v", sb.ContainerID, err)
		}
	}

	// Revoke sandbox API key
	if sb.APIKeyID != "" {
		if err := m.store.RevokeKey(sb.APIKeyID); err != nil {
			log.Printf("warning: failed to revoke key %s: %v", sb.APIKeyID, err)
		}
	}

	// Delete from database
	if err := m.store.DeleteSandbox(id); err != nil {
		return fmt.Errorf("deleting sandbox from database: %w", err)
	}
	return nil
}

// Get returns a sandbox by ID, with live status from Docker.
func (m *Manager) Get(ctx context.Context, id string) (*Sandbox, error) {
	sb, err := m.store.GetSandbox(id)
	if err != nil {
		return nil, fmt.Errorf("sandbox not found: %w", err)
	}

	result := dbSandboxToSandbox(sb)

	// Refresh status from Docker
	if sb.ContainerID != "" {
		if state, err := m.docker.containerInspect(ctx, sb.ContainerID); err == nil {
			if state.Running {
				result.Status = StatusRunning
			} else {
				result.Status = StatusStopped
			}
		}
	}

	return result, nil
}

// List returns all sandboxes with live status.
func (m *Manager) List(ctx context.Context) ([]*Sandbox, error) {
	dbSandboxes, err := m.store.ListSandboxes()
	if err != nil {
		return nil, fmt.Errorf("listing sandboxes: %w", err)
	}

	// Build a map of container statuses from Docker
	statuses := make(map[string]string)
	containers, err := m.docker.containerList(ctx, LabelManaged+"=true")
	if err == nil {
		for _, c := range containers {
			if sid, ok := c.Labels[LabelSandboxID]; ok {
				statuses[sid] = c.State
			}
		}
	}

	sandboxes := make([]*Sandbox, 0, len(dbSandboxes))
	for _, db := range dbSandboxes {
		sb := dbSandboxToSandbox(db)
		// Refresh status from Docker
		if dockerState, ok := statuses[sb.ID]; ok {
			if dockerState == "running" {
				sb.Status = StatusRunning
			} else {
				sb.Status = StatusStopped
			}
		}
		sandboxes = append(sandboxes, sb)
	}

	return sandboxes, nil
}

// Logs returns log output for a sandbox container.
func (m *Manager) Logs(ctx context.Context, id string, tail int) (io.ReadCloser, error) {
	sb, err := m.store.GetSandbox(id)
	if err != nil {
		return nil, fmt.Errorf("sandbox not found: %w", err)
	}
	if sb.ContainerID == "" {
		return nil, fmt.Errorf("sandbox %s has no container", id)
	}

	return m.docker.containerLogs(ctx, sb.ContainerID, tail, false)
}

// OpenClawStatus describes the state of OpenClaw in a sandbox.
type OpenClawStatus struct {
	SandboxID   string `json:"sandbox_id"`
	SandboxName string `json:"sandbox_name"`
	Installed   bool   `json:"installed"`
	GatewayPID  string `json:"gateway_pid,omitempty"`
	GatewayPort int    `json:"gateway_port"`
	Running     bool   `json:"running"`
}

// EnsureOpenClaw makes sure a sandbox exists with OpenClaw installed and the
// gateway running as the container's main process.
func (m *Manager) EnsureOpenClaw(ctx context.Context, providerKey string) (*OpenClawStatus, error) {
	const sandboxName = "openclaw"
	const gatewayPort = 18789
	const imageTag = "solon/openclaw:latest"

	if err := m.EnsureNetwork(ctx); err != nil {
		return nil, err
	}

	solonHost := m.gatewayIP
	if solonHost == "" {
		solonHost = "host.docker.internal"
	}

	status := &OpenClawStatus{
		SandboxName: sandboxName,
		GatewayPort: gatewayPort,
	}

	// Check if we already have a running openclaw container
	containers, _ := m.docker.containerList(ctx, LabelManaged+"=true")
	for _, c := range containers {
		if c.Labels[LabelPolicy] == "openclaw-gateway" && c.State == "running" {
			status.SandboxID = c.Labels[LabelSandboxID]
			status.Installed = true
			status.Running = true
			return status, nil
		}
	}

	// Step 1: Build the OpenClaw image if it doesn't exist.
	// Based on solon/sandbox:latest (Playwright-ready) with OpenClaw installed.
	log.Println("[openclaw] Preparing OpenClaw image...")

	// Ensure the base sandbox image exists first (Chromium + Playwright)
	if err := m.EnsureSandboxImage(ctx); err != nil {
		log.Printf("[openclaw] Warning: sandbox image build failed, falling back to %s: %v", DefaultImage, err)
	}

	// Check if OpenClaw image already exists locally
	if !m.docker.imageExists(ctx, imageTag) {
		// Try pulling pre-built image from GHCR
		log.Println("[openclaw] Pulling OpenClaw image from GHCR...")
		if err := m.docker.imagePull(ctx, GHCROpenClawImage); err == nil {
			// Tag as local name
			tagResp, tagErr := m.docker.do(ctx, "POST",
				"/images/"+strings.ReplaceAll(GHCROpenClawImage, "/", "%2F")+"/tag?repo=solon/openclaw&tag=latest", nil)
			if tagErr == nil {
				_ = tagResp.Body.Close()
			}
			log.Println("[openclaw] Image pulled from GHCR: solon/openclaw:latest")
		} else {
			log.Printf("[openclaw] GHCR pull failed (%v), building locally...", err)

			// Fallback: build locally
			baseImage := SandboxImage
			if !m.docker.imageExists(ctx, SandboxImage) {
				baseImage = DefaultImage
			}

			tmpID, err := m.docker.containerCreate(ctx, containerConfig{
				Name: "openclaw-build-tmp",
				Body: map[string]any{
					"Image": baseImage,
					"Cmd":   []string{"sleep", "infinity"},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("creating build container: %w", err)
			}

			if err := m.docker.containerStart(ctx, tmpID); err != nil {
				_ = m.docker.containerRemove(ctx, tmpID)
				return nil, fmt.Errorf("starting build container: %w", err)
			}

			_, err = m.docker.containerExec(ctx, tmpID, []string{"npm", "install", "-g", "openclaw"}, nil)
			if err != nil {
				_ = m.docker.containerRemove(ctx, tmpID)
				return nil, fmt.Errorf("installing openclaw: %w", err)
			}

			openclawCfg := `{"gateway":{"auth":{"mode":"token","token":"solon-openclaw-token"}}}`
			_, _ = m.docker.containerExec(ctx, tmpID,
				[]string{"sh", "-c", "mkdir -p /root/.openclaw && printf '%s' '" + openclawCfg + "' > /root/.openclaw/openclaw.json"},
				nil,
			)

			commitResp, err := m.docker.do(ctx, "POST",
				fmt.Sprintf("/commit?container=%s&repo=solon/openclaw&tag=latest&pause=true", tmpID),
				nil,
			)
			if err != nil {
				_ = m.docker.containerRemove(ctx, tmpID)
				return nil, fmt.Errorf("committing openclaw image: %w", err)
			}
			_ = commitResp.Body.Close()

			_ = m.docker.containerStop(ctx, tmpID, 2)
			_ = m.docker.containerRemove(ctx, tmpID)
			log.Println("[openclaw] Image built locally: solon/openclaw:latest")
		}
	} else {
		log.Println("[openclaw] Image exists, skipping build")
	}

	// Step 2: Remove any existing openclaw sandbox container
	for _, c := range containers {
		if c.Labels[LabelPolicy] == "openclaw-gateway" {
			_ = m.docker.containerRemove(ctx, c.ID)
		}
	}
	// Also remove by name
	_ = m.docker.containerRemove(ctx, "openclaw-sandbox-openclaw")

	// Step 3: Find or create the sandbox DB record
	var sandboxID string
	dbSandboxes, _ := m.store.ListSandboxes()
	for _, s := range dbSandboxes {
		if s.Name == sandboxName {
			sandboxID = s.ID
			break
		}
	}
	if sandboxID == "" {
		sandboxID = uuid.New().String()
		_ = m.store.CreateSandbox(sandboxID, sandboxName, "", "full", Tier4Maximum, "", nil)
	}
	status.SandboxID = sandboxID

	// Step 4: Create and start the container with gateway as main process
	log.Println("[openclaw] Starting OpenClaw gateway...")

	containerID, err := m.docker.containerCreate(ctx, containerConfig{
		Name: "openclaw-sandbox-openclaw",
		Body: map[string]any{
			"Image": imageTag,
			"Env": []string{
				fmt.Sprintf("ANTHROPIC_API_KEY=%s", providerKey),
				"OPENCLAW_GATEWAY_TOKEN=solon-openclaw-token",
				"OPENCLAW_NO_RESPAWN=1",
				fmt.Sprintf("SOLON_ENDPOINT=http://%s:%d", solonHost, m.solonPort),
				"NODE_ENV=production",
			},
			"Cmd": []string{
				"sh", "-c",
				fmt.Sprintf(
					// Start gateway on loopback + expose a simple HTTP API for
					// sending messages via 'openclaw agent' (handles auth internally)
					"cat > /tmp/agent-api.mjs << 'API'\n"+
						"import { createServer } from 'http';\n"+
						"import { spawn } from 'child_process';\n"+
						"const PORT = %d;\n"+
						"const server = createServer((req, res) => {\n"+
						"  if (req.method === 'POST' && req.url === '/send') {\n"+
						"    let body = '';\n"+
						"    req.on('data', c => body += c);\n"+
						"    req.on('end', () => {\n"+
						"      try {\n"+
						"        const { message } = JSON.parse(body);\n"+
						"        const escaped = message.replace(/\\\"/g, '\\\\\\\"');\n"+
						"        const proc = spawn('openclaw',\n"+
						"          ['agent', '--agent', 'main', '--message', escaped, '--json', '--timeout', '120'],\n"+
						"          { env: { ...process.env, HOME: '/root' }, timeout: 130000 }\n"+
						"        );\n"+
						"        res.writeHead(200, {\n"+
						"          'Content-Type': 'text/event-stream',\n"+
						"          'Cache-Control': 'no-cache',\n"+
						"          'Connection': 'keep-alive',\n"+
						"          'Access-Control-Allow-Origin': '*'\n"+
						"        });\n"+
						"        proc.stdout.on('data', d => {\n"+
						"          const lines = d.toString().split('\\n').filter(l => l.trim());\n"+
						"          for (const line of lines) {\n"+
						"            res.write('data: ' + line + '\\n\\n');\n"+
						"          }\n"+
						"        });\n"+
						"        proc.stderr.on('data', d => {\n"+
						"          res.write('event: error\\ndata: ' + JSON.stringify({ error: d.toString() }) + '\\n\\n');\n"+
						"        });\n"+
						"        proc.on('close', code => {\n"+
						"          res.write('event: done\\ndata: ' + JSON.stringify({ exit_code: code }) + '\\n\\n');\n"+
						"          res.end();\n"+
						"        });\n"+
						"        proc.on('error', e => {\n"+
						"          res.write('event: error\\ndata: ' + JSON.stringify({ error: e.message }) + '\\n\\n');\n"+
						"          res.end();\n"+
						"        });\n"+
						"        req.on('close', () => { if (!proc.killed) proc.kill(); });\n"+
						"      } catch (e) {\n"+
						"        res.writeHead(500, { 'Content-Type': 'application/json' });\n"+
						"        res.end(JSON.stringify({ error: e.message || 'agent error' }));\n"+
						"      }\n"+
						"    });\n"+
						"  } else if (req.method === 'GET' && req.url === '/health') {\n"+
						"    res.writeHead(200, { 'Content-Type': 'application/json' });\n"+
						"    res.end('{\"ok\":true}');\n"+
						"  } else if (req.method === 'OPTIONS') {\n"+
						"    res.writeHead(204, { 'Access-Control-Allow-Origin': '*', 'Access-Control-Allow-Methods': 'POST', 'Access-Control-Allow-Headers': 'Content-Type' });\n"+
						"    res.end();\n"+
						"  } else {\n"+
						"    res.writeHead(404);\n"+
						"    res.end();\n"+
						"  }\n"+
						"});\n"+
						"server.listen(PORT, '0.0.0.0', () => console.log('[agent-api] listening on 0.0.0.0:' + PORT));\n"+
						"API\n"+
						// Start gateway on loopback (openclaw agent connects locally)
						"openclaw gateway --port %d --bind loopback --allow-unconfigured --auth none &"+
						" sleep 5; "+
						"exec node /tmp/agent-api.mjs",
					gatewayPort+1, gatewayPort,
				),
			},
			"Labels": map[string]string{
				LabelManaged:   "true",
				LabelSandboxID: sandboxID,
				LabelPolicy:    "openclaw-gateway",
			},
			"HostConfig": map[string]any{
				"NetworkMode": NetworkName,
				"CapDrop":     []string{"ALL"},
				"CapAdd":      []string{"NET_BIND_SERVICE"},
				"SecurityOpt": []string{"no-new-privileges"},
				"ExtraHosts":  []string{"host.docker.internal:host-gateway"},
				"Binds":       []string{"openclaw-data:/root/.openclaw"},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating gateway container: %w", err)
	}

	_ = m.store.UpdateSandboxContainer(sandboxID, containerID)

	if err := m.docker.containerStart(ctx, containerID); err != nil {
		return nil, fmt.Errorf("starting gateway container: %w", err)
	}

	// Wait for gateway to be ready
	log.Println("[openclaw] Waiting for gateway...")
	for i := 0; i < 10; i++ {
		time.Sleep(2 * time.Second)
		state, err := m.docker.containerInspect(ctx, containerID)
		if err == nil && state.Running {
			break
		}
	}

	_ = m.store.UpdateSandboxStatus(sandboxID, StatusRunning)

	status.Installed = true
	status.Running = true
	log.Println("[openclaw] Ready")
	return status, nil
}

// ContainerIP returns the IP address of a running sandbox's container on its network.
func (m *Manager) ContainerIP(ctx context.Context, sandboxID string) (string, error) {
	sb, err := m.store.GetSandbox(sandboxID)
	if err != nil {
		return "", fmt.Errorf("sandbox not found: %w", err)
	}
	if sb.ContainerID == "" {
		return "", fmt.Errorf("sandbox %s has no container", sandboxID)
	}

	// Try tier-2 network first (most sandboxes), fall back to legacy bridge
	for _, net := range []string{NetworkTier2, NetworkTier1, NetworkName} {
		ip, err := m.docker.containerInspectNetwork(ctx, sb.ContainerID, net)
		if err == nil && ip != "" {
			return ip, nil
		}
	}
	return "", fmt.Errorf("sandbox %s: no network IP found", sandboxID)
}

// OpenClawContainerIP returns the IP address of the running OpenClaw gateway container
// on the solon-bridge network. Returns empty string and error if not found.
func (m *Manager) OpenClawContainerIP(ctx context.Context) (string, error) {
	containers, err := m.docker.containerList(ctx, LabelManaged+"=true")
	if err != nil {
		return "", fmt.Errorf("listing containers: %w", err)
	}

	for _, c := range containers {
		if c.Labels[LabelPolicy] == "openclaw-gateway" && c.State == "running" {
			ip, err := m.docker.containerInspectNetwork(ctx, c.ID, NetworkName)
			if err != nil {
				return "", err
			}
			return ip, nil
		}
	}

	return "", fmt.Errorf("no running OpenClaw gateway container found")
}

// Stats returns resource usage for a sandbox.
func (m *Manager) Stats(ctx context.Context, id string) (*SandboxStats, error) {
	sb, err := m.store.GetSandbox(id)
	if err != nil {
		return nil, fmt.Errorf("sandbox not found: %w", err)
	}
	if sb.ContainerID == "" {
		return nil, fmt.Errorf("sandbox %s has no container", id)
	}

	raw, err := m.docker.containerStats(ctx, sb.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("getting stats: %w", err)
	}

	const mb = 1024.0 * 1024.0
	memPercent := 0.0
	if raw.MemLimit > 0 {
		memPercent = float64(raw.MemUsage) / float64(raw.MemLimit) * 100
	}

	return &SandboxStats{
		CPUPercent: raw.CPUPercent,
		MemUsageMB: float64(raw.MemUsage) / mb,
		MemLimitMB: float64(raw.MemLimit) / mb,
		MemPercent: memPercent,
		NetRxMB:    float64(raw.NetRxBytes) / mb,
		NetTxMB:    float64(raw.NetTxBytes) / mb,
	}, nil
}

func dbSandboxToSandbox(db *storage.SandboxRecord) *Sandbox {
	sb := &Sandbox{
		ID:          db.ID,
		Name:        db.Name,
		ContainerID: db.ContainerID,
		Status:      db.Status,
		Policy:      db.Policy,
		Tier:        db.Tier,
		APIKeyID:    db.APIKeyID,
		CreatedAt:   db.CreatedAt,
		StartedAt:   db.StartedAt,
		StoppedAt:   db.StoppedAt,
	}
	if db.Config != "" {
		var cfg Config
		if err := json.Unmarshal([]byte(db.Config), &cfg); err == nil {
			sb.Config = &cfg
		}
	}
	return sb
}
