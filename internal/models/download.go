package models

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodaay/HuggingFaceModelDownloader/pkg/hfdownloader"
)

// DownloadProgress reports download progress to the caller.
type DownloadProgress struct {
	Event      string  `json:"event"`      // "start", "progress", "done", "error"
	File       string  `json:"file"`       // filename being downloaded
	Downloaded int64   `json:"downloaded"` // bytes downloaded so far
	Total      int64   `json:"total"`      // total bytes
	Percent    float64 `json:"percent"`    // 0-100
	Message    string  `json:"message"`    // additional info
}

// DownloadResult contains information about a completed download.
type DownloadResult struct {
	Filename string // original filename
	RelPath  string // relative path under blobs dir, e.g. "blobs/sha256-abc123.gguf"
	Size     int64
	SHA256   string
}

// DownloadFromURL downloads a GGUF file from a direct URL (e.g. R2 mirror).
// Supports HTTP range-based resume: if a partial .download file exists, it
// continues from where it left off. Retries up to 3 times on connection errors.
func DownloadFromURL(ctx context.Context, url, blobsDir string, progressFn func(DownloadProgress)) (*DownloadResult, error) {
	// Extract filename from URL
	parts := strings.Split(url, "/")
	filename := parts[len(parts)-1]
	if filename == "" {
		filename = "model.gguf"
	}

	tmpPath := filepath.Join(blobsDir, ".download-"+filename)

	const maxRetries = 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := downloadWithResume(ctx, url, filename, tmpPath, blobsDir, progressFn)
		if err == nil {
			return result, nil
		}

		// Only retry on connection/read errors, not on HTTP errors or context cancellation
		if ctx.Err() != nil {
			_ = os.Remove(tmpPath)
			return nil, ctx.Err()
		}
		if attempt < maxRetries && isRetryableError(err) {
			if progressFn != nil {
				progressFn(DownloadProgress{
					Event:   "progress",
					File:    filename,
					Message: fmt.Sprintf("connection error, retrying (%d/%d)...", attempt+1, maxRetries),
				})
			}
			continue
		}
		_ = os.Remove(tmpPath)
		return nil, err
	}

	_ = os.Remove(tmpPath)
	return nil, fmt.Errorf("download failed after %d retries", maxRetries)
}

// isRetryableError returns true for network/connection errors that are safe to retry.
func isRetryableError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "reading response:") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "unexpected EOF")
}

// downloadWithResume performs a single download attempt with range-resume support.
func downloadWithResume(ctx context.Context, url, filename, tmpPath, blobsDir string, progressFn func(DownloadProgress)) (*DownloadResult, error) {
	// Check for existing partial download
	var existingSize int64
	if info, err := os.Stat(tmpPath); err == nil {
		existingSize = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading from mirror: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case 200:
		// Full response — start from scratch
		existingSize = 0
	case 206:
		// Partial content — resume supported
	case 416:
		// Range not satisfiable — file might already be complete
		// Fall through to hash verification below
		existingSize = 0
	default:
		return nil, fmt.Errorf("mirror returned %d", resp.StatusCode)
	}

	var total int64
	switch resp.StatusCode {
	case 200:
		total = resp.ContentLength
	case 206:
		total = existingSize + resp.ContentLength
	}

	if progressFn != nil {
		msg := "downloading from Solon mirror"
		if existingSize > 0 {
			msg = fmt.Sprintf("resuming download from %.1f MB", float64(existingSize)/1e6)
		}
		progressFn(DownloadProgress{Event: "start", File: filename, Total: total, Message: msg})
	}

	// Open file for writing (append if resuming, create if new)
	var out *os.File
	if existingSize > 0 && resp.StatusCode == 206 {
		out, err = os.OpenFile(tmpPath, os.O_WRONLY|os.O_APPEND, 0644)
	} else {
		out, err = os.Create(tmpPath)
		existingSize = 0
	}
	if err != nil {
		return nil, fmt.Errorf("opening temp file: %w", err)
	}

	downloaded := existingSize
	buf := make([]byte, 256*1024) // 256KB chunks

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				_ = out.Close()
				return nil, fmt.Errorf("writing file: %w", writeErr)
			}
			downloaded += int64(n)

			if progressFn != nil && total > 0 {
				progressFn(DownloadProgress{
					Event:      "progress",
					File:       filename,
					Downloaded: downloaded,
					Total:      total,
					Percent:    float64(downloaded) / float64(total) * 100,
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = out.Close()
			return nil, fmt.Errorf("reading response: %w", readErr)
		}
	}
	_ = out.Close()

	// Compute SHA256 of the complete file
	hash, err := fileSHA256(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("computing SHA256: %w", err)
	}

	blobName := fmt.Sprintf("sha256-%s.gguf", hash[:16])
	blobPath := filepath.Join(blobsDir, blobName)
	_ = os.Remove(blobPath)

	if err := os.Rename(tmpPath, blobPath); err != nil {
		if err := copyFile(tmpPath, blobPath); err != nil {
			return nil, fmt.Errorf("moving file to blobs: %w", err)
		}
	}

	if progressFn != nil {
		progressFn(DownloadProgress{Event: "done", File: filename, Message: "download complete"})
	}

	info, _ := os.Stat(blobPath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	return &DownloadResult{
		Filename: filename,
		RelPath:  filepath.Join("blobs", blobName),
		Size:     size,
		SHA256:   hash,
	}, nil
}

// DownloadModel downloads a GGUF model from HuggingFace.
func DownloadModel(ctx context.Context, repo, fileFilter, blobsDir string, progressFn func(DownloadProgress)) (*DownloadResult, error) {
	// Fast path: when a quantization filter is given, resolve the exact .gguf
	// file via the HF tree API and fetch just that one file by direct URL. The
	// hfdownloader library only applies its filename filter to nodes it detects
	// as LFS, and that detection is unreliable for some GGUF repos — when it
	// fails the library downloads every quant in the repo (and findGGUF then
	// keeps an arbitrary one). Resolving the single file ourselves avoids that.
	// On any ambiguity (zero or multiple matches, network failure) we fall back
	// to the library path below, preserving previous behavior.
	if fileFilter != "" {
		if url, err := resolveGGUFURL(ctx, repo, fileFilter); err == nil {
			return DownloadFromURL(ctx, url, blobsDir, progressFn)
		}
	}

	// Use a temp dir for download, then move to blobs
	tmpDir, err := os.MkdirTemp(blobsDir, "download-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Combine filter + extension into a single glob-style pattern.
	// The HF downloader treats multiple filters as OR, but we need AND.
	combinedFilter := fileFilter + ".gguf"
	job := hfdownloader.Job{
		Repo:    repo,
		Filters: []string{combinedFilter},
	}

	cfg := hfdownloader.Settings{
		OutputDir:          tmpDir,
		Concurrency:        8,
		MaxActiveDownloads: 2,
		Verify:             "size",
		Retries:            4,
		BackoffInitial:     "400ms",
		BackoffMax:         "10s",
		StaleTimeout:       "5m",
		MultipartThreshold: "256MiB",
	}

	if progressFn != nil {
		progress := func(e hfdownloader.ProgressEvent) {
			switch e.Event {
			case "file_start":
				progressFn(DownloadProgress{
					Event: "start",
					File:  e.Path,
					Total: e.Total,
				})
			case "file_progress":
				pct := float64(0)
				if e.Total > 0 {
					pct = float64(e.Downloaded) / float64(e.Total) * 100
				}
				progressFn(DownloadProgress{
					Event:      "progress",
					File:       e.Path,
					Downloaded: e.Downloaded,
					Total:      e.Total,
					Percent:    pct,
				})
			case "file_done":
				progressFn(DownloadProgress{
					Event:   "done",
					File:    e.Path,
					Message: "download complete",
				})
			case "error":
				progressFn(DownloadProgress{
					Event:   "error",
					Message: e.Message,
				})
			}
		}

		if err := hfdownloader.Download(ctx, job, cfg, progress); err != nil {
			return nil, fmt.Errorf("downloading from HuggingFace: %w", err)
		}
	} else {
		if err := hfdownloader.Download(ctx, job, cfg, nil); err != nil {
			return nil, fmt.Errorf("downloading from HuggingFace: %w", err)
		}
	}

	// Find the downloaded GGUF file
	ggufPath, err := findGGUF(tmpDir)
	if err != nil {
		return nil, err
	}

	// Compute SHA256
	hash, err := fileSHA256(ggufPath)
	if err != nil {
		return nil, fmt.Errorf("computing SHA256: %w", err)
	}

	// Get file info
	info, err := os.Stat(ggufPath)
	if err != nil {
		return nil, fmt.Errorf("stat GGUF file: %w", err)
	}

	// Move to blobs dir with sha256-based name
	blobName := fmt.Sprintf("sha256-%s.gguf", hash[:16])
	blobPath := filepath.Join(blobsDir, blobName)

	// If blob already exists (same model re-pulled), remove it first
	_ = os.Remove(blobPath)

	if err := os.Rename(ggufPath, blobPath); err != nil {
		// Cross-device rename fallback: copy then delete
		if err := copyFile(ggufPath, blobPath); err != nil {
			return nil, fmt.Errorf("moving GGUF to blobs: %w", err)
		}
		_ = os.Remove(ggufPath)
	}

	return &DownloadResult{
		Filename: filepath.Base(ggufPath),
		RelPath:  filepath.Join("blobs", blobName),
		Size:     info.Size(),
		SHA256:   hash,
	}, nil
}

// hfBaseURL is the HuggingFace base URL. It is a variable so tests can point
// the tree/resolve calls at a local server.
var hfBaseURL = "https://huggingface.co"

// hfTreeNode is the subset of the HuggingFace tree API response we need.
type hfTreeNode struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// resolveGGUFURL queries the HuggingFace tree API for repo and returns the
// direct resolve URL of the single .gguf file matching quant (e.g. "Q8_0").
// It returns an error when zero or more than one file matches, so the caller
// can fall back to the multi-file downloader.
func resolveGGUFURL(ctx context.Context, repo, quant string) (string, error) {
	treeURL := fmt.Sprintf("%s/api/models/%s/tree/main", hfBaseURL, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, treeURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tree API for %s returned %s", repo, resp.Status)
	}

	var nodes []hfTreeNode
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return "", err
	}
	paths := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Type == "file" {
			paths = append(paths, n.Path)
		}
	}

	match, ok := selectGGUFByQuant(paths, quant)
	if !ok {
		return "", fmt.Errorf("no unique .gguf file for quant %q in %s", quant, repo)
	}
	return fmt.Sprintf("%s/%s/resolve/main/%s", hfBaseURL, repo, match), nil
}

// selectGGUFByQuant picks the single .gguf path whose filename matches quant.
// Matching is case-insensitive and substring-based; if several files contain
// the quant token it narrows to those whose name (minus extension) ends with
// it. It returns ok=false unless exactly one file matches, signalling the
// caller to fall back rather than guess (e.g. split or multi-part GGUFs).
func selectGGUFByQuant(paths []string, quant string) (string, bool) {
	q := strings.ToLower(quant)
	var contains, suffix []string
	for _, p := range paths {
		base := strings.ToLower(filepath.Base(p))
		if !strings.HasSuffix(base, ".gguf") || !strings.Contains(base, q) {
			continue
		}
		contains = append(contains, p)
		if strings.HasSuffix(strings.TrimSuffix(base, ".gguf"), q) {
			suffix = append(suffix, p)
		}
	}
	if len(suffix) == 1 {
		return suffix[0], true
	}
	if len(contains) == 1 {
		return contains[0], true
	}
	return "", false
}

// findGGUF recursively searches for a .gguf file in the given directory.
func findGGUF(dir string) (string, error) {
	var found string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".gguf") {
			if found == "" || info.Size() > 0 {
				found = path
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("searching for GGUF: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("no .gguf file found in download — check model repo and filter")
	}
	return found, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
