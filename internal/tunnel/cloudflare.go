package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// urlPattern matches the Cloudflare quick tunnel URL from cloudflared output.
var urlPattern = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// Cloudflare implements the Tunnel interface using Cloudflare Tunnel (cloudflared).
// Supports both quick tunnels (ephemeral) and named tunnels (persistent).
// Automatically downloads cloudflared if not found.
type Cloudflare struct {
	port       int
	url        string
	enabled    bool
	persistent bool
	cmd        *exec.Cmd
	creds      *CredentialStore
	progressFn func(string) // optional progress callback for UI
	mu         sync.Mutex
}

// NewCloudflare creates a new Cloudflare tunnel manager.
func NewCloudflare(port int, creds *CredentialStore) *Cloudflare {
	return &Cloudflare{port: port, creds: creds}
}

// SetProgressFn sets a callback for reporting progress (e.g., cloudflared download).
func (c *Cloudflare) SetProgressFn(fn func(string)) {
	c.progressFn = fn
}

// getCloudflared finds or auto-downloads cloudflared.
func (c *Cloudflare) getCloudflared(ctx context.Context) (string, error) {
	path, err := EnsureCloudflared(ctx, c.progressFn)
	if err != nil {
		return "", fmt.Errorf("cloudflared not available: %w", err)
	}
	return path, nil
}

// Setup performs one-time setup: runs cloudflared login, creates a named tunnel, and stores credentials.
func (c *Cloudflare) Setup(ctx context.Context) error {
	path, err := c.getCloudflared(ctx)
	if err != nil {
		return err
	}

	if c.creds == nil {
		return fmt.Errorf("no credential store configured")
	}

	// Step 1: cloudflared tunnel login — opens browser for Cloudflare auth
	fmt.Println("Opening browser for Cloudflare authentication...")
	fmt.Println("If a browser doesn't open, visit the URL printed below.")
	fmt.Println()

	loginCmd := exec.CommandContext(ctx, path, "tunnel", "login")
	loginCmd.Env = append(loginCmd.Environ(), "TUNNEL_ORIGIN_CERT="+c.creds.CloudflaredCredPath())
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr

	if err := loginCmd.Run(); err != nil {
		return fmt.Errorf("cloudflared login failed: %w", err)
	}

	fmt.Println()
	fmt.Println("Authenticated with Cloudflare. Creating named tunnel...")

	// Step 2: Create named tunnel
	tunnelName := "solon"
	createCmd := exec.CommandContext(ctx, path,
		"tunnel", "--origincert", c.creds.CloudflaredCredPath(),
		"--credentials-file", c.creds.TunnelCredPath(tunnelName),
		"create", tunnelName,
	)

	output, err := createCmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		if contains(outputStr, "already exists") {
			fmt.Printf("Tunnel %q already exists. Reusing existing tunnel.\n", tunnelName)
		} else {
			return fmt.Errorf("creating tunnel: %s — %w", outputStr, err)
		}
	}

	// Step 3: Get tunnel ID from cloudflared
	listCmd := exec.CommandContext(ctx, path,
		"tunnel", "--origincert", c.creds.CloudflaredCredPath(),
		"list", "-o", "json",
	)
	listOutput, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("listing tunnels: %w", err)
	}

	tunnelID, err := parseTunnelID(listOutput, tunnelName)
	if err != nil {
		return fmt.Errorf("finding tunnel ID: %w", err)
	}

	// Step 4: Save credentials
	persistentURL := fmt.Sprintf("https://%s.cfargotunnel.com", tunnelID)
	creds := &Credentials{
		TunnelID:   tunnelID,
		TunnelName: tunnelName,
		URL:        persistentURL,
	}
	if err := c.creds.Save(creds); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Println()
	fmt.Printf("Tunnel setup complete!\n")
	fmt.Printf("Persistent URL: %s\n", persistentURL)
	fmt.Println()
	fmt.Println("You can also configure a custom domain in the Cloudflare dashboard.")
	fmt.Println("Start Solon with --tunnel to enable the tunnel on startup.")

	return nil
}

func (c *Cloudflare) Enable(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.enabled {
		return fmt.Errorf("tunnel already enabled")
	}

	path, err := c.getCloudflared(ctx)
	if err != nil {
		return err
	}

	// Check for stored credentials (named tunnel)
	if c.creds != nil && c.creds.Exists() {
		return c.enableNamed(ctx, path)
	}

	// Fall back to quick tunnel
	return c.enableQuick(ctx, path)
}

// enableNamed starts the tunnel using stored credentials (persistent URL).
func (c *Cloudflare) enableNamed(ctx context.Context, cloudflaredPath string) error {
	creds, err := c.creds.Load()
	if err != nil {
		return fmt.Errorf("loading tunnel credentials: %w", err)
	}
	if creds == nil {
		return fmt.Errorf("no tunnel credentials found — run 'solon tunnel setup' first")
	}

	c.cmd = exec.CommandContext(ctx, cloudflaredPath,
		"tunnel",
		"--origincert", c.creds.CloudflaredCredPath(),
		"--credentials-file", c.creds.TunnelCredPath(creds.TunnelName),
		"--url", fmt.Sprintf("http://localhost:%d", c.port),
		"run", creds.TunnelID,
	)

	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("starting cloudflared: %w", err)
	}

	c.enabled = true
	c.persistent = true
	c.url = creds.URL

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// drain stderr
		}
	}()

	return nil
}

// enableQuick starts an ephemeral quick tunnel (URL changes on every restart).
func (c *Cloudflare) enableQuick(ctx context.Context, cloudflaredPath string) error {
	slog.Info("starting ephemeral tunnel (run 'solon tunnel setup' for a persistent URL)")

	c.cmd = exec.CommandContext(ctx, cloudflaredPath,
		"tunnel", "--url", fmt.Sprintf("http://localhost:%d", c.port),
	)

	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("starting cloudflared: %w", err)
	}

	c.enabled = true
	c.persistent = false

	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if match := urlPattern.FindString(line); match != "" {
				select {
				case urlCh <- match:
				default:
				}
			}
		}
	}()

	select {
	case url := <-urlCh:
		c.url = url
	case <-time.After(15 * time.Second):
		c.url = "(tunnel starting — URL not yet available)"
	}

	return nil
}

func (c *Cloudflare) Disable(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.enabled {
		return nil
	}

	if c.cmd != nil && c.cmd.Process != nil {
		if err := c.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("stopping cloudflared: %w", err)
		}
	}

	c.enabled = false
	c.url = ""
	c.cmd = nil
	c.persistent = false
	return nil
}

func (c *Cloudflare) Status(ctx context.Context) (*TunnelStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return &TunnelStatus{
		Enabled:    c.enabled,
		URL:        c.url,
		Provider:   "cloudflare",
		Persistent: c.persistent,
	}, nil
}

func (c *Cloudflare) URL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.url
}

func (c *Cloudflare) IsPersistent() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.persistent
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
