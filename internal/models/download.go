package models

import (
	"context"
	"crypto/sha256"
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
func DownloadFromURL(ctx context.Context, url, blobsDir string, progressFn func(DownloadProgress)) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading from mirror: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mirror returned %d", resp.StatusCode)
	}

	// Extract filename from URL
	parts := strings.Split(url, "/")
	filename := parts[len(parts)-1]
	if filename == "" {
		filename = "model.gguf"
	}

	tmpPath := filepath.Join(blobsDir, ".download-"+filename)
	out, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }()

	total := resp.ContentLength

	if progressFn != nil {
		progressFn(DownloadProgress{Event: "start", File: filename, Total: total})
	}

	h := sha256.New()
	var downloaded int64
	buf := make([]byte, 256*1024) // 256KB chunks

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				_ = out.Close()
				return nil, fmt.Errorf("writing file: %w", err)
			}
			_, _ = h.Write(buf[:n])
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

	hash := fmt.Sprintf("%x", h.Sum(nil))
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
	// Use a temp dir for download, then move to blobs
	tmpDir, err := os.MkdirTemp(blobsDir, "download-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	job := hfdownloader.Job{
		Repo:    repo,
		Filters: []string{fileFilter, "gguf"}, // match the quantization AND .gguf extension
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
