package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// dockerClient talks to the Docker Engine API over a Unix socket.
type dockerClient struct {
	client *http.Client
	host   string // Unix socket path
}

func newDockerClient(socketPath string) *dockerClient {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}
	return &dockerClient{
		client: &http.Client{Transport: transport},
		host:   socketPath,
	}
}

func (d *dockerClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return d.client.Do(req)
}

// containerCreate creates a Docker container and returns its ID.
func (d *dockerClient) containerCreate(ctx context.Context, cfg containerConfig) (string, error) {
	resp, err := d.do(ctx, "POST", "/containers/create?name="+cfg.Name, cfg.Body)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("creating container: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding container ID: %w", err)
	}
	return result.ID, nil
}

// containerStart starts a container.
func (d *dockerClient) containerStart(ctx context.Context, id string) error {
	resp, err := d.do(ctx, "POST", "/containers/"+id+"/start", nil)
	if err != nil {
		return fmt.Errorf("starting container %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil // Already running
	}
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("starting container: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// containerStop stops a container with a timeout.
func (d *dockerClient) containerStop(ctx context.Context, id string, timeoutSec int) error {
	path := fmt.Sprintf("/containers/%s/stop?t=%d", id, timeoutSec)
	resp, err := d.do(ctx, "POST", path, nil)
	if err != nil {
		return fmt.Errorf("stopping container %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil // Already stopped
	}
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stopping container: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// containerRemove removes a container (force-removes if running).
func (d *dockerClient) containerRemove(ctx context.Context, id string) error {
	resp, err := d.do(ctx, "DELETE", "/containers/"+id+"?force=true&v=true", nil)
	if err != nil {
		return fmt.Errorf("removing container %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("removing container: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// containerInspect returns the state of a container.
func (d *dockerClient) containerInspect(ctx context.Context, id string) (*containerState, error) {
	resp, err := d.do(ctx, "GET", "/containers/"+id+"/json", nil)
	if err != nil {
		return nil, fmt.Errorf("inspecting container %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("container %s not found", id)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inspecting container: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		State struct {
			Status   string `json:"Status"`
			Running  bool   `json:"Running"`
			ExitCode int    `json:"ExitCode"`
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding container state: %w", err)
	}

	return &containerState{
		Status:   result.State.Status,
		Running:  result.State.Running,
		ExitCode: result.State.ExitCode,
	}, nil
}

// containerLogs returns the logs for a container.
func (d *dockerClient) containerLogs(ctx context.Context, id string, tail int, follow bool) (io.ReadCloser, error) {
	path := fmt.Sprintf("/containers/%s/logs?stdout=true&stderr=true&tail=%d&follow=%t", id, tail, follow)
	resp, err := d.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("getting container logs: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("getting container logs: status %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// containerList lists containers matching the given label filter.
func (d *dockerClient) containerList(ctx context.Context, labelFilter string) ([]containerListEntry, error) {
	filters := fmt.Sprintf(`{"label":["%s"]}`, labelFilter)
	path := "/containers/json?all=true&filters=" + filters
	resp, err := d.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("listing containers: status %d: %s", resp.StatusCode, string(body))
	}

	var entries []containerListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decoding container list: %w", err)
	}
	return entries, nil
}

// networkCreate creates a Docker bridge network.
func (d *dockerClient) networkCreate(ctx context.Context, name string) error {
	return d.networkCreateWithOpts(ctx, name, false)
}

// networkCreateWithOpts creates a Docker bridge network with options.
// If internal is true, the network blocks all outbound internet access.
func (d *dockerClient) networkCreateWithOpts(ctx context.Context, name string, internal bool) error {
	body := map[string]any{
		"Name":     name,
		"Driver":   "bridge",
		"Internal": internal,
	}
	resp, err := d.do(ctx, "POST", "/networks/create", body)
	if err != nil {
		return fmt.Errorf("creating network %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 409 means network already exists — that's fine
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		// "already exists" in the error body is also fine
		if strings.Contains(string(respBody), "already exists") {
			return nil
		}
		return fmt.Errorf("creating network: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// containerInspectNetwork returns the IP address of a container on a specific Docker network.
func (d *dockerClient) containerInspectNetwork(ctx context.Context, id, network string) (string, error) {
	resp, err := d.do(ctx, "GET", "/containers/"+id+"/json", nil)
	if err != nil {
		return "", fmt.Errorf("inspecting container %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("inspecting container: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding container inspect: %w", err)
	}

	net, ok := result.NetworkSettings.Networks[network]
	if !ok || net.IPAddress == "" {
		return "", fmt.Errorf("container %s not connected to network %s", id, network)
	}
	return net.IPAddress, nil
}

// networkGateway returns the gateway IP for a Docker network.
func (d *dockerClient) networkGateway(ctx context.Context, name string) string {
	resp, err := d.do(ctx, "GET", "/networks/"+name, nil)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result struct {
		IPAM struct {
			Config []struct {
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	if len(result.IPAM.Config) > 0 {
		return result.IPAM.Config[0].Gateway
	}
	return ""
}

// containerExec runs a command inside a container and returns the output.
func (d *dockerClient) containerExec(ctx context.Context, id string, cmd []string, env []string) (string, error) {
	// Step 1: Create exec instance
	execBody := map[string]any{
		"AttachStdout": true,
		"AttachStderr": true,
		"Cmd":          cmd,
	}
	if len(env) > 0 {
		execBody["Env"] = env
	}

	resp, err := d.do(ctx, "POST", "/containers/"+id+"/exec", execBody)
	if err != nil {
		return "", fmt.Errorf("creating exec: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("creating exec: status %d: %s", resp.StatusCode, string(body))
	}

	var execResult struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&execResult); err != nil {
		return "", fmt.Errorf("decoding exec ID: %w", err)
	}

	// Step 2: Start exec and capture output
	startResp, err := d.do(ctx, "POST", "/exec/"+execResult.ID+"/start", map[string]any{
		"Detach": false,
		"Tty":    false,
	})
	if err != nil {
		return "", fmt.Errorf("starting exec: %w", err)
	}
	defer func() { _ = startResp.Body.Close() }()

	rawOutput, _ := io.ReadAll(startResp.Body)

	// Docker exec multiplexed stream has 8-byte frame headers when Tty=false.
	// Strip them to get clean output.
	output := stripDockerStreamHeaders(rawOutput)

	// Step 3: Check exit code
	inspResp, err := d.do(ctx, "GET", "/exec/"+execResult.ID+"/json", nil)
	if err != nil {
		return string(output), nil // Return output even if inspect fails
	}
	defer func() { _ = inspResp.Body.Close() }()

	var inspResult struct {
		ExitCode int `json:"ExitCode"`
	}
	_ = json.NewDecoder(inspResp.Body).Decode(&inspResult)

	if inspResult.ExitCode != 0 {
		return string(output), fmt.Errorf("command exited with code %d: %s", inspResult.ExitCode, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output)), nil
}

// containerStats returns resource usage stats for a container.
func (d *dockerClient) containerStats(ctx context.Context, id string) (*containerStatsResult, error) {
	// stream=false returns a single snapshot
	resp, err := d.do(ctx, "GET", "/containers/"+id+"/stats?stream=false", nil)
	if err != nil {
		return nil, fmt.Errorf("getting container stats: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("getting container stats: status %d: %s", resp.StatusCode, string(body))
	}

	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
			OnlineCPUs     int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage uint64 `json:"usage"`
			Limit uint64 `json:"limit"`
		} `json:"memory_stats"`
		Networks map[string]struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		} `json:"networks"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding container stats: %w", err)
	}

	// Calculate CPU percentage
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage)
	cpuPercent := 0.0
	if sysDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / sysDelta) * float64(raw.CPUStats.OnlineCPUs) * 100.0
	}

	// Sum network I/O across all interfaces
	var rxBytes, txBytes uint64
	for _, net := range raw.Networks {
		rxBytes += net.RxBytes
		txBytes += net.TxBytes
	}

	return &containerStatsResult{
		CPUPercent: cpuPercent,
		MemUsage:   raw.MemoryStats.Usage,
		MemLimit:   raw.MemoryStats.Limit,
		NetRxBytes: rxBytes,
		NetTxBytes: txBytes,
	}, nil
}

// imagePull pulls a Docker image from a registry. It blocks until the pull is complete.
func (d *dockerClient) imagePull(ctx context.Context, image string) error {
	// Docker API: POST /images/create?fromImage=<image>
	path := "/images/create?fromImage=" + image
	resp, err := d.do(ctx, "POST", path, nil)
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", image, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pulling image %s: status %d: %s", image, resp.StatusCode, string(body))
	}

	// Drain the response body (Docker streams pull progress as JSON lines)
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// imageExists checks whether an image is available locally.
func (d *dockerClient) imageExists(ctx context.Context, image string) bool {
	// URL-encode the image name (slashes become %2F)
	encoded := strings.ReplaceAll(image, "/", "%2F")
	resp, err := d.do(ctx, "GET", "/images/"+encoded+"/json", nil)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// --- Types for Docker API ---

type containerConfig struct {
	Name string
	Body map[string]any
}

type containerState struct {
	Status   string
	Running  bool
	ExitCode int
}

type containerStatsResult struct {
	CPUPercent float64
	MemUsage   uint64
	MemLimit   uint64
	NetRxBytes uint64
	NetTxBytes uint64
}

type containerListEntry struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

// stripDockerStreamHeaders removes Docker multiplexed stream frame headers.
// Docker exec with Tty=false wraps each output chunk with an 8-byte header:
// [stream_type(1), 0, 0, 0, size(4 big-endian)].
func stripDockerStreamHeaders(raw []byte) []byte {
	var clean bytes.Buffer
	pos := 0
	for pos+8 <= len(raw) {
		// Read frame header
		frameSize := int(binary.BigEndian.Uint32(raw[pos+4 : pos+8]))
		pos += 8
		if pos+frameSize > len(raw) {
			// Incomplete frame — append remaining
			clean.Write(raw[pos:])
			break
		}
		clean.Write(raw[pos : pos+frameSize])
		pos += frameSize
	}
	if clean.Len() == 0 {
		return raw // Fallback: return as-is if no headers detected
	}
	return clean.Bytes()
}
