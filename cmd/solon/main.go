package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/openclaw/solon/internal/gateway"
	"github.com/openclaw/solon/internal/guardrails"
	"github.com/openclaw/solon/internal/inference"
	"github.com/openclaw/solon/internal/inference/backends"
	"github.com/openclaw/solon/internal/logging"
	"github.com/openclaw/solon/internal/models"
	"github.com/openclaw/solon/internal/relay"
	"github.com/openclaw/solon/internal/sandbox"
	"github.com/openclaw/solon/internal/storage"
	"github.com/openclaw/solon/internal/tunnel"
	"github.com/openclaw/solon/internal/update"
)

var version = "dev"

func main() {
	// Initialize structured logging (JSON when SOLON_LOG_JSON=1, text otherwise)
	logging.Init(os.Getenv("SOLON_LOG_JSON") == "1")

	root := &cobra.Command{
		Use:   "solon",
		Short: "Self-hosted AI runtime with secure web API",
		Long:  "Solon — Your AI. Your rules.\n\nRun AI models locally and access them securely from the web via API keys.",
	}

	root.AddCommand(
		serveCmd(),
		modelsCmd(),
		keysCmd(),
		providersCmd(),
		sandboxesCmd(),
		openclawCmd(),
		tunnelCmd(),
		statusCmd(),
		versionCmd(),
		updateCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	var port int
	var enableTunnel bool
	var enableRemote bool
	var preload string
	var memBudget int64

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Solon daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open("")
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer func() { _ = db.Close() }()

			// Auto-create admin key on first run
			hasKeys, err := db.HasKeys()
			if err != nil {
				return fmt.Errorf("checking for keys: %w", err)
			}
			if !hasKeys {
				key, err := db.CreateKey("default-admin", "admin")
				if err != nil {
					return fmt.Errorf("creating initial admin key: %w", err)
				}
				fmt.Println()
				fmt.Println("  First run detected — admin API key created:")
				fmt.Println()
				fmt.Printf("  %s\n", key.Raw)
				fmt.Println()
				fmt.Println("  Save this key — it won't be shown again.")
				fmt.Println()
				fmt.Println("  Test it with:")
				fmt.Printf("  curl http://localhost:%d/v1/models -H \"Authorization: Bearer %s\"\n", port, key.Raw)
				fmt.Println()
				fmt.Println("  Get started by pulling a model:")
				fmt.Println("  solon models pull llama3.2:3b")
				fmt.Println()
			}

			var preloadModels []string
			if preload != "" {
				for _, m := range strings.Split(preload, ",") {
					m = strings.TrimSpace(m)
					if m != "" {
						preloadModels = append(preloadModels, m)
					}
				}
			}

			// Load external API providers from database
			var providers []backends.Provider
			dbProviders, err := db.LoadProviders()
			if err != nil {
				slog.Warn("could not load providers", "error", err)
			} else {
				for _, p := range dbProviders {
					providers = append(providers, backends.Provider{
						Name:    p.Name,
						BaseURL: p.BaseURL,
						APIKey:  p.APIKey,
					})
				}
				if len(providers) > 0 {
					slog.Info("loaded external providers", "count", len(providers))
				}
			}

			engine, err := inference.NewEngineWithOptions(inference.EngineOptions{
				MemoryBudgetMB: memBudget,
				Preload:        preloadModels,
				Providers:      providers,
			})
			if err != nil {
				return fmt.Errorf("starting inference engine: %w", err)
			}
			defer func() { _ = engine.Close() }()

			// Initialize tunnel with credential store
			creds, _ := tunnel.DefaultCredentialStore()
			t := tunnel.NewCloudflare(port, creds)

			// Load guardrails config and policies
			grCfg := guardrails.LoadConfig(guardrails.ConfigPath())
			policies := guardrails.NewPolicyStore(guardrails.PoliciesDir())

			// Optional: refresh catalog from remote on startup
			go models.RefreshCatalogFromRemote("https://getsolon.dev/catalog.json")

			// Initialize sandbox manager (optional — requires Docker)
			var sandboxMgr *sandbox.Manager
			dockerSocket := "/var/run/docker.sock"
			sandboxMgr = sandbox.NewManager(dockerSocket, db, port)
			if sandboxMgr.Available(cmd.Context()) {
				slog.Info("Docker detected, sandbox management enabled")
				if err := sandboxMgr.EnsureNetwork(cmd.Context()); err != nil {
					slog.Warn("could not create sandbox network", "error", err)
				}
			} else {
				slog.Info("Docker not detected, sandbox management disabled")
				sandboxMgr = nil
			}

			gw, err := gateway.New(gateway.Config{
				Port:       port,
				Version:    version,
				Engine:     engine,
				Store:      db,
				Tunnel:     t,
				Guardrails: grCfg,
				Policies:   policies,
				Sandboxes:  sandboxMgr,
			})
			if err != nil {
				return fmt.Errorf("creating gateway: %w", err)
			}

			fmt.Printf("Solon is running at http://localhost:%d\n", port)
			fmt.Printf("Dashboard: http://localhost:%d\n", port)

			// Background version check
			go func() {
				result, err := update.CheckLatestCached(version)
				if err == nil && result.UpdateAvail {
					fmt.Printf("\n  Update available: %s → %s (run 'solon update' to upgrade)\n\n", result.CurrentVersion, result.LatestVersion)
				}
			}()

			if enableTunnel {
				if t.IsPersistent() || (creds != nil && creds.Exists()) {
					fmt.Println("Starting persistent tunnel...")
				} else {
					fmt.Println("Starting tunnel (ephemeral — run 'solon tunnel setup' for a persistent URL)...")
				}
				if err := t.Enable(cmd.Context()); err != nil {
					fmt.Printf("Warning: tunnel failed to start: %v\n", err)
				} else {
					fmt.Printf("Tunnel: %s\n", t.URL())
				}
			}

			if enableRemote {
				instanceID, err := relay.EnsureRegistered()
				if err != nil {
					fmt.Printf("Warning: remote access setup failed: %v\n", err)
				} else {
					rc := relay.NewClient(instanceID, port, version)
					gw.SetRelay(rc)
					go func() {
						if err := rc.Start(cmd.Context()); err != nil {
							slog.Error("relay error", "error", err)
						}
					}()
					// Wait briefly for connection
					time.Sleep(2 * time.Second)
					if rc.Connected() {
						fmt.Printf("Remote: %s\n", rc.RemoteURL())
					} else {
						fmt.Println("Remote: connecting...")
					}
				}
			}

			return gw.ListenAndServe()
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 8420, "Port to listen on")
	cmd.Flags().BoolVar(&enableTunnel, "tunnel", false, "Enable Cloudflare tunnel on startup")
	cmd.Flags().BoolVar(&enableRemote, "remote", false, "Enable remote access via Solon Relay")
	cmd.Flags().StringVar(&preload, "preload", "", "Comma-separated models to preload (e.g. llama3.2:3b,mistral:7b)")
	cmd.Flags().Int64Var(&memBudget, "memory-budget", 0, "Memory budget in MB (0 = auto, 80% system RAM)")
	return cmd
}

func modelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage AI models",
	}

	cmd.AddCommand(
		modelsPullCmd(),
		modelsListCmd(),
		modelsRemoveCmd(),
		modelsInfoCmd(),
		modelsAddCmd(),
		modelsKnownCmd(),
	)

	return cmd
}

func modelsPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull [model]",
		Short: "Download a model",
		Long:  "Download a model by name (e.g., 'llama3.2:3b') or direct HuggingFace reference (e.g., 'bartowski/Llama-3.2-3B-Instruct-GGUF').",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			engine, err := inference.NewEngine()
			if err != nil {
				return fmt.Errorf("starting engine: %w", err)
			}
			defer func() { _ = engine.Close() }()

			fmt.Printf("Pulling model %s...\n", name)

			progressFn := func(p models.DownloadProgress) {
				switch p.Event {
				case "start":
					fmt.Printf("  Downloading: %s\n", p.File)
				case "progress":
					fmt.Printf("\r  %.1f%% (%d / %d MB)",
						p.Percent,
						p.Downloaded/(1024*1024),
						p.Total/(1024*1024))
				case "done":
					fmt.Printf("\r  Download complete!                    \n")
				case "error":
					fmt.Printf("\n  Error: %s\n", p.Message)
				}
			}

			if err := engine.PullModel(cmd.Context(), name, progressFn); err != nil {
				return err
			}

			fmt.Printf("Model %s pulled successfully.\n", name)
			return nil
		},
	}
}

func modelsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed models",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := inference.NewEngine()
			if err != nil {
				return fmt.Errorf("starting engine: %w", err)
			}
			defer func() { _ = engine.Close() }()

			models, err := engine.ListModels(cmd.Context())
			if err != nil {
				return err
			}

			if len(models) == 0 {
				fmt.Println("No models installed. Run 'solon models pull <model>' to get started.")
				fmt.Println()
				fmt.Println("Available models:")
				fmt.Println("  llama3.2:3b    — Llama 3.2 3B (2 GB)")
				fmt.Println("  llama3.2:8b    — Llama 3.2 8B (5 GB)")
				fmt.Println("  mistral:7b     — Mistral 7B (4 GB)")
				fmt.Println("  phi4:14b       — Phi-4 14B (8 GB)")
				fmt.Println("  qwen2.5:7b     — Qwen 2.5 7B (4 GB)")
				return nil
			}

			fmt.Printf("%-30s %-10s %-15s\n", "NAME", "SIZE", "MODIFIED")
			for _, m := range models {
				fmt.Printf("%-30s %-10s %-15s\n", m.Name, m.SizeHuman(), m.ModifiedHuman())
			}
			return nil
		},
	}
}

func modelsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [model]",
		Short: "Remove a model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := inference.NewEngine()
			if err != nil {
				return fmt.Errorf("starting engine: %w", err)
			}
			defer func() { _ = engine.Close() }()

			fmt.Printf("Removing model %s...\n", args[0])
			if err := engine.RemoveModel(cmd.Context(), args[0]); err != nil {
				return err
			}

			fmt.Printf("Model %s removed.\n", args[0])
			return nil
		},
	}
}

func modelsInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info [model]",
		Short: "Show detailed information about an installed model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := inference.NewEngine()
			if err != nil {
				return fmt.Errorf("starting engine: %w", err)
			}
			defer func() { _ = engine.Close() }()

			info, err := engine.GetModelInfo(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Name:         %s\n", info.Name)
			fmt.Printf("Size:         %s\n", info.SizeHuman())
			if info.Format != "" {
				fmt.Printf("Format:       %s\n", info.Format)
			}
			if info.Family != "" {
				fmt.Printf("Family:       %s\n", info.Family)
			}
			if info.Params != "" {
				fmt.Printf("Parameters:   %s\n", info.Params)
			}
			if info.Quantization != "" {
				fmt.Printf("Quantization: %s\n", info.Quantization)
			}
			fmt.Printf("Modified:     %s\n", info.ModifiedHuman())
			return nil
		},
	}
}

func modelsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [name] [hf-repo] [quantization]",
		Short: "Add a custom model name mapping",
		Long:  "Map a friendly name to a HuggingFace repo. Quantization defaults to Q4_K_M.",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			repo := args[1]
			quant := "Q4_K_M"
			if len(args) == 3 {
				quant = args[2]
			}

			dataDir, err := models.DataDir()
			if err != nil {
				return fmt.Errorf("getting data dir: %w", err)
			}

			reg, err := models.NewRegistry(dataDir)
			if err != nil {
				return fmt.Errorf("initializing registry: %w", err)
			}

			if err := reg.AddCustomMapping(name, models.ModelSource{
				Repo: repo,
				File: quant,
			}); err != nil {
				return fmt.Errorf("adding mapping: %w", err)
			}

			fmt.Printf("Added mapping: %s → %s (filter: %s)\n", name, repo, quant)
			return nil
		},
	}
}

func modelsKnownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "known",
		Short: "List known model names and their HuggingFace sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, err := models.DataDir()
			if err != nil {
				return fmt.Errorf("getting data dir: %w", err)
			}

			reg, err := models.NewRegistry(dataDir)
			if err != nil {
				return fmt.Errorf("initializing registry: %w", err)
			}

			known := reg.KnownModels()
			fmt.Printf("%-20s %-50s %-10s\n", "NAME", "REPO", "QUANT")
			for name, source := range known {
				fmt.Printf("%-20s %-50s %-10s\n", name, source.Repo, source.File)
			}
			return nil
		},
	}
}

func keysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys",
	}

	var keyName string
	var keyScope string
	var keyRateLimit int
	var keyTTL string
	var keyModels string
	var noTunnel bool

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyScope != "admin" && keyScope != "user" {
				return fmt.Errorf("invalid scope %q: must be 'admin' or 'user'", keyScope)
			}

			db, err := storage.Open("")
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer func() { _ = db.Close() }()

			opts := storage.CreateKeyOptions{
				Name:      keyName,
				Scope:     keyScope,
				RateLimit: keyRateLimit,
			}

			if noTunnel {
				f := false
				opts.TunnelAccess = &f
			}

			// Parse TTL
			if keyTTL != "" {
				d, err := parseTTL(keyTTL)
				if err != nil {
					return fmt.Errorf("invalid TTL %q: %w", keyTTL, err)
				}
				opts.TTL = d
			}

			// Parse model restrictions
			if keyModels != "" {
				opts.AllowedModels = strings.Split(keyModels, ",")
				for i := range opts.AllowedModels {
					opts.AllowedModels[i] = strings.TrimSpace(opts.AllowedModels[i])
				}
			}

			key, err := db.CreateKeyWithOptions(opts)
			if err != nil {
				return fmt.Errorf("creating key: %w", err)
			}

			fmt.Println("API key created successfully!")
			fmt.Println()
			fmt.Printf("  Key:   %s\n", key.Raw)
			fmt.Printf("  Name:  %s\n", key.Name)
			fmt.Printf("  Scope: %s\n", key.Scope)
			fmt.Printf("  Rate:  %d/min\n", key.RateLimit)
			if key.ExpiresAt != nil {
				fmt.Printf("  Expires: %s\n", key.ExpiresAt.Format("2006-01-02 15:04"))
			}
			if len(key.AllowedModels) > 0 {
				fmt.Printf("  Models: %s\n", strings.Join(key.AllowedModels, ", "))
			}
			if !key.TunnelAccess {
				fmt.Printf("  Tunnel: disabled\n")
			}
			fmt.Println()
			fmt.Println("Save this key — it won't be shown again.")
			return nil
		},
	}
	createCmd.Flags().StringVar(&keyName, "name", "", "Name for the API key")
	createCmd.Flags().StringVar(&keyScope, "scope", "user", "Key scope: 'admin' or 'user'")
	createCmd.Flags().IntVar(&keyRateLimit, "rate-limit", 0, "Requests per minute (0 = default 60)")
	createCmd.Flags().StringVar(&keyTTL, "ttl", "", "Time-to-live (e.g., 30d, 24h, 7d)")
	createCmd.Flags().StringVar(&keyModels, "models", "", "Comma-separated list of allowed models")
	createCmd.Flags().BoolVar(&noTunnel, "no-tunnel", false, "Disable tunnel access for this key")
	_ = createCmd.MarkFlagRequired("name")

	cmd.AddCommand(
		createCmd,
		&cobra.Command{
			Use:   "list",
			Short: "List all API keys",
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				keys, err := db.ListKeys()
				if err != nil {
					return err
				}

				if len(keys) == 0 {
					fmt.Println("No API keys. Run 'solon keys create --name <name>' to create one.")
					return nil
				}

				fmt.Printf("%-20s %-15s %-10s %-8s %-12s %-20s\n", "NAME", "PREFIX", "SCOPE", "RATE", "EXPIRES", "CREATED")
				for _, k := range keys {
					expires := "never"
					if k.ExpiresAt != nil {
						if time.Now().After(*k.ExpiresAt) {
							expires = "EXPIRED"
						} else {
							expires = k.ExpiresAt.Format("2006-01-02")
						}
					}
					fmt.Printf("%-20s %-15s %-10s %-8s %-12s %-20s\n",
						k.Name, k.Prefix+"...", k.Scope,
						fmt.Sprintf("%d/m", k.RateLimit),
						expires,
						k.CreatedAt.Format("2006-01-02 15:04"))
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "revoke [key]",
			Short: "Revoke an API key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				if err := db.RevokeKey(args[0]); err != nil {
					return fmt.Errorf("revoking key: %w", err)
				}

				fmt.Println("API key revoked.")
				return nil
			},
		},
	)

	return cmd
}

func providersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "Manage external API providers",
	}

	var apiKey string
	var baseURL string

	addCmd := &cobra.Command{
		Use:   "add [name]",
		Short: "Add an external API provider (e.g., anthropic, openai)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := bufio.NewReader(os.Stdin)
			name := ""

			// Interactive wizard if no args/flags
			if len(args) == 0 {
				fmt.Println("Add an external API provider")
				fmt.Println()
				fmt.Println("Supported providers:")
				fmt.Println("  1) anthropic")
				fmt.Println("  2) openai")
				fmt.Println("  3) other (custom)")
				fmt.Println()
				fmt.Print("Select provider [1]: ")
				choice, _ := reader.ReadString('\n')
				choice = strings.TrimSpace(choice)
				switch choice {
				case "", "1", "anthropic":
					name = "anthropic"
				case "2", "openai":
					name = "openai"
				case "3", "other":
					fmt.Print("Provider name: ")
					name, _ = reader.ReadString('\n')
					name = strings.TrimSpace(name)
					if name == "" {
						return fmt.Errorf("provider name is required")
					}
				default:
					name = strings.TrimSpace(choice)
				}
			} else {
				name = args[0]
			}

			// Interactive API key prompt if not passed via flag
			if apiKey == "" {
				fmt.Printf("Paste your %s API key: ", name)
				apiKey, _ = reader.ReadString('\n')
				apiKey = strings.TrimSpace(apiKey)
				if apiKey == "" {
					return fmt.Errorf("API key is required")
				}
			}

			// Auto-fill base URL from well-known defaults
			if baseURL == "" {
				if url, ok := storage.WellKnownProviders[name]; ok {
					baseURL = url
				} else {
					fmt.Print("Base URL: ")
					baseURL, _ = reader.ReadString('\n')
					baseURL = strings.TrimSpace(baseURL)
					if baseURL == "" {
						return fmt.Errorf("base URL is required for unknown provider %q", name)
					}
				}
			}

			db, err := storage.Open("")
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer func() { _ = db.Close() }()

			provider, err := db.CreateProvider(name, baseURL, apiKey)
			if err != nil {
				return fmt.Errorf("adding provider: %w", err)
			}

			fmt.Println()
			fmt.Printf("Provider added: %s\n", provider.Name)
			fmt.Printf("  Base URL: %s\n", provider.BaseURL)
			fmt.Printf("  API Key:  %s\n", provider.APIKey)
			fmt.Println()
			fmt.Println("Use models with the provider/ prefix:")
			switch name {
			case "anthropic":
				fmt.Println("  anthropic/claude-sonnet-4-20250514")
			case "openai":
				fmt.Println("  openai/gpt-4o")
			default:
				fmt.Printf("  %s/<model-name>\n", name)
			}
			return nil
		},
	}
	addCmd.Flags().StringVar(&apiKey, "api-key", "", "API key for the provider")
	addCmd.Flags().StringVar(&baseURL, "base-url", "", "Base URL (auto-detected for anthropic/openai)")

	cmd.AddCommand(
		addCmd,
		&cobra.Command{
			Use:   "list",
			Short: "List configured providers",
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				providers, err := db.ListProviders()
				if err != nil {
					return err
				}

				if len(providers) == 0 {
					fmt.Println("No providers configured. Run 'solon providers add <name> --api-key <key>' to add one.")
					return nil
				}

				fmt.Printf("%-15s %-40s %-12s %-20s\n", "NAME", "BASE URL", "API KEY", "CREATED")
				for _, p := range providers {
					fmt.Printf("%-15s %-40s %-12s %-20s\n",
						p.Name, p.BaseURL, p.APIKey, p.CreatedAt.Format("2006-01-02 15:04"))
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "remove [name]",
			Short: "Remove a provider",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				if err := db.DeleteProvider(args[0]); err != nil {
					return fmt.Errorf("removing provider: %w", err)
				}

				fmt.Printf("Provider %s removed.\n", args[0])
				return nil
			},
		},
	)

	return cmd
}

func openclawCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openclaw",
		Short: "Launch OpenClaw in a secure sandbox (one command)",
		Long:  "Creates a sandbox, installs OpenClaw, starts the gateway, and opens the TUI.\nRequires Docker and at least one configured provider (e.g., 'solon providers add anthropic').",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open("")
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer func() { _ = db.Close() }()

			// Check Docker
			mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
			if !mgr.Available(cmd.Context()) {
				return fmt.Errorf("docker is not running — OpenClaw requires Docker for sandboxing")
			}

			// Get provider key (prefer anthropic, fall back to first available)
			providerKey := ""
			providerName := ""
			for _, name := range []string{"anthropic", "openai"} {
				key, err := db.GetProviderKey(name)
				if err == nil && key != "" {
					providerKey = key
					providerName = name
					break
				}
			}
			if providerKey == "" {
				// Try any provider
				providers, err := db.LoadProviders()
				if err == nil && len(providers) > 0 {
					providerKey = providers[0].APIKey
					providerName = providers[0].Name
				}
			}
			if providerKey == "" {
				fmt.Println("No inference provider configured.")
				fmt.Println()
				fmt.Println("Add one first:")
				fmt.Println("  solon providers add anthropic")
				fmt.Println("  solon providers add openai")
				return fmt.Errorf("no provider configured")
			}

			fmt.Printf("Using provider: %s\n", providerName)
			fmt.Println()

			// Ensure OpenClaw is set up
			status, err := mgr.EnsureOpenClaw(cmd.Context(), providerKey)
			if err != nil {
				return fmt.Errorf("setting up OpenClaw: %w", err)
			}

			fmt.Println()
			fmt.Println("  OpenClaw is ready!")
			fmt.Printf("  Sandbox:  %s\n", status.SandboxName)
			fmt.Printf("  Gateway:  ws://127.0.0.1:%d (inside container)\n", status.GatewayPort)
			fmt.Println()
			fmt.Println("  Connecting to TUI...")
			fmt.Println()

			// Exec into the container with openclaw tui
			sb, _ := mgr.Get(cmd.Context(), status.SandboxID)
			if sb == nil || sb.ContainerID == "" {
				return fmt.Errorf("sandbox container not found")
			}

			// Attach interactively to the container with openclaw tui
			dockerCmd := osexec.Command("docker", "exec", "-it",
				"-e", fmt.Sprintf("ANTHROPIC_API_KEY=%s", providerKey),
				"-e", "OPENCLAW_GATEWAY_TOKEN=solon-openclaw-token",
				sb.ContainerID,
				"openclaw", "tui", "--token", "solon-openclaw-token",
			)
			dockerCmd.Stdin = os.Stdin
			dockerCmd.Stdout = os.Stdout
			dockerCmd.Stderr = os.Stderr
			return dockerCmd.Run()
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the OpenClaw sandbox",
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
				sandboxes, err := mgr.List(cmd.Context())
				if err != nil {
					return err
				}
				for _, sb := range sandboxes {
					if sb.Name == "openclaw" {
						if err := mgr.Stop(cmd.Context(), sb.ID); err != nil {
							return err
						}
						fmt.Println("OpenClaw sandbox stopped.")
						return nil
					}
				}
				fmt.Println("No OpenClaw sandbox found.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show OpenClaw status",
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
				sandboxes, err := mgr.List(cmd.Context())
				if err != nil {
					return err
				}
				for _, sb := range sandboxes {
					if sb.Name == "openclaw" {
						fmt.Printf("Sandbox:  %s (%s)\n", sb.Name, sb.Status)
						fmt.Printf("Policy:   %s\n", sb.Policy)
						fmt.Printf("Created:  %s\n", sb.CreatedAt.Format("2006-01-02 15:04"))
						return nil
					}
				}
				fmt.Println("OpenClaw not set up yet. Run 'solon openclaw' to start.")
				return nil
			},
		},
	)

	return cmd
}

func sandboxesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandboxes",
		Short: "Manage OpenClaw sandboxes",
	}

	var policy string
	var tier int

	createCmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open("")
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer func() { _ = db.Close() }()

			mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
			if !mgr.Available(cmd.Context()) {
				return fmt.Errorf("docker is not running — sandboxes require Docker")
			}

			req := sandbox.CreateRequest{
				Name: args[0],
				Tier: tier,
			}
			// If tier not set but policy was, use policy for backward compat
			if tier == 0 {
				req.Policy = policy
			}

			sb, err := mgr.Create(cmd.Context(), req)
			if err != nil {
				return err
			}

			tierName := "Standard"
			if tc, ok := sandbox.TierConfigs[sb.Tier]; ok {
				tierName = tc.Name
			}

			fmt.Printf("Sandbox created: %s\n", sb.Name)
			fmt.Printf("  ID:     %s\n", sb.ID)
			fmt.Printf("  Tier:   %d (%s)\n", sb.Tier, tierName)
			fmt.Printf("  Policy: %s\n", sb.Policy)
			fmt.Printf("  Status: %s\n", sb.Status)
			fmt.Println()
			fmt.Println("Start it with:")
			fmt.Printf("  solon sandboxes start %s\n", sb.ID)
			return nil
		},
	}
	createCmd.Flags().IntVarP(&tier, "tier", "t", 0, "Security tier: 1=locked, 2=standard, 3=advanced, 4=maximum (default 2)")
	createCmd.Flags().StringVar(&policy, "policy", "api-only", "Network policy (deprecated, use --tier instead)")

	cmd.AddCommand(
		createCmd,
		&cobra.Command{
			Use:   "list",
			Short: "List all sandboxes",
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
				sandboxes, err := mgr.List(cmd.Context())
				if err != nil {
					return err
				}

				if len(sandboxes) == 0 {
					fmt.Println("No sandboxes. Run 'solon sandboxes create <name>' to create one.")
					return nil
				}

				fmt.Printf("%-20s %-10s %-6s %-15s %-20s\n", "NAME", "STATUS", "TIER", "POLICY", "CREATED")
				for _, sb := range sandboxes {
					tierLabel := fmt.Sprintf("%d", sb.Tier)
					if tc, ok := sandbox.TierConfigs[sb.Tier]; ok {
						tierLabel = fmt.Sprintf("%d-%s", sb.Tier, tc.Name)
					}
					fmt.Printf("%-20s %-10s %-6s %-15s %-20s\n",
						sb.Name, sb.Status, tierLabel, sb.Policy, sb.CreatedAt.Format("2006-01-02 15:04"))
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "start [id]",
			Short: "Start a sandbox",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
				if err := mgr.Start(cmd.Context(), args[0]); err != nil {
					return err
				}
				fmt.Println("Sandbox started.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "stop [id]",
			Short: "Stop a sandbox",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
				if err := mgr.Stop(cmd.Context(), args[0]); err != nil {
					return err
				}
				fmt.Println("Sandbox stopped.")
				return nil
			},
		},
		&cobra.Command{
			Use:   "remove [id]",
			Short: "Remove a sandbox and revoke its API key",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				db, err := storage.Open("")
				if err != nil {
					return fmt.Errorf("opening database: %w", err)
				}
				defer func() { _ = db.Close() }()

				mgr := sandbox.NewManager("/var/run/docker.sock", db, 8420)
				if err := mgr.Remove(cmd.Context(), args[0]); err != nil {
					return err
				}
				fmt.Println("Sandbox removed.")
				return nil
			},
		},
	)

	return cmd
}

func tunnelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage secure tunnel",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "setup",
			Short: "Set up a persistent named tunnel (one-time setup)",
			Long:  "Walks through Cloudflare authentication, creates a named tunnel, and stores credentials for persistent URLs that survive restarts.",
			RunE: func(cmd *cobra.Command, args []string) error {
				creds, err := tunnel.DefaultCredentialStore()
				if err != nil {
					return fmt.Errorf("initializing credential store: %w", err)
				}

				t := tunnel.NewCloudflare(8420, creds)
				return t.Setup(cmd.Context())
			},
		},
		&cobra.Command{
			Use:   "enable",
			Short: "Enable secure tunnel to expose API to the internet",
			RunE: func(cmd *cobra.Command, args []string) error {
				creds, _ := tunnel.DefaultCredentialStore()
				t := tunnel.NewCloudflare(8420, creds)
				if err := t.Enable(cmd.Context()); err != nil {
					return fmt.Errorf("enabling tunnel: %w", err)
				}
				fmt.Printf("Tunnel enabled: %s\n", t.URL())
				if t.IsPersistent() {
					fmt.Println("(persistent — URL survives restarts)")
				} else {
					fmt.Println("(ephemeral — run 'solon tunnel setup' for a persistent URL)")
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Disable secure tunnel",
			RunE: func(cmd *cobra.Command, args []string) error {
				creds, _ := tunnel.DefaultCredentialStore()
				t := tunnel.NewCloudflare(8420, creds)
				return t.Disable(cmd.Context())
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show tunnel status",
			RunE: func(cmd *cobra.Command, args []string) error {
				creds, _ := tunnel.DefaultCredentialStore()
				t := tunnel.NewCloudflare(8420, creds)

				// Check for stored credentials
				if creds != nil && creds.Exists() {
					stored, _ := creds.Load()
					if stored != nil {
						fmt.Println("Tunnel: configured (persistent)")
						fmt.Printf("URL:    %s\n", stored.URL)
						fmt.Printf("ID:     %s\n", stored.TunnelID)
						return nil
					}
				}

				status, err := t.Status(cmd.Context())
				if err != nil {
					return err
				}
				if status.Enabled {
					fmt.Printf("Tunnel: enabled\n")
					fmt.Printf("URL:    %s\n", status.URL)
				} else {
					fmt.Println("Tunnel: not configured")
					fmt.Println("Run 'solon tunnel setup' to set up a persistent tunnel.")
				}
				return nil
			},
		},
	)

	return cmd
}

func statusCmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Solon daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := fmt.Sprintf("http://localhost:%d/api/v1/health", port)

			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				fmt.Println("Status:  stopped")
				fmt.Printf("Port:    %d\n", port)
				return nil
			}
			defer func() { _ = resp.Body.Close() }()

			var health struct {
				Status  string `json:"status"`
				Version string `json:"version"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&health)

			fmt.Println("Status:  running")
			fmt.Printf("Port:    %d\n", port)
			fmt.Printf("Version: %s\n", health.Version)
			fmt.Printf("URL:     http://localhost:%d\n", port)
			return nil
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 8420, "Port to check")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show Solon version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("solon version %s\n", version)
		},
	}
}

func updateCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update Solon to the latest version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Current version: %s\n", version)
			fmt.Println("Checking for updates...")

			result, err := update.CheckLatest(version)
			if err != nil {
				return fmt.Errorf("checking for updates: %w", err)
			}

			if !result.UpdateAvail && !force {
				fmt.Printf("Already at latest version (%s).\n", result.CurrentVersion)
				return nil
			}

			fmt.Printf("New version available: %s\n", result.LatestVersion)

			if err := update.DoUpdate(version); err != nil {
				return fmt.Errorf("updating: %w", err)
			}

			fmt.Printf("Solon updated to %s successfully!\n", result.LatestVersion)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force update even if already at latest version")
	return cmd
}

// parseTTL parses a human-friendly duration string like "30d", "24h", "7d".
func parseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty TTL")
	}

	// Handle day suffix
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}

	// Fall back to standard Go duration parsing
	return time.ParseDuration(s)
}
