package utils

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/utmstack/MigrationTool/config"
)

func Download(url, destPath string, insecure bool) error {
	var lastErr error

	backoffs := []time.Duration{
		30 * time.Second,
		60 * time.Second,
		120 * time.Second,
	}

	if insecure && Logger != nil {
		Logger.Info("WARNING: TLS certificate validation is disabled for download")
	}

	for attempt := 0; attempt < config.DownloadMaxRetries; attempt++ {
		if attempt > 0 {
			if Logger != nil {
				Logger.Info("Download attempt %d/%d, waiting %v before retry",
					attempt+1, config.DownloadMaxRetries, backoffs[attempt-1])
			}
			time.Sleep(backoffs[attempt-1])
		}

		err := downloadOnce(url, destPath, insecure)
		if err == nil {
			return nil
		}

		lastErr = err
		if Logger != nil {
			Logger.ErrorF("Download attempt %d failed: %v", attempt+1, err)
		}
	}

	return fmt.Errorf("download failed after %d attempts: %w", config.DownloadMaxRetries, lastErr)
}

func downloadOnce(url, destPath string, insecure bool) error {
	// Create HTTP client with optional insecure TLS
	transport := &http.Transport{}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Minute, // Large file, may take a while
	}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	// Create destination file
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer out.Close()

	// Copy response body to file
	written, err := io.Copy(out, resp.Body)
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Sync to disk to ensure data is persisted
	if err := out.Sync(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("failed to sync file to disk: %w", err)
	}

	// Validate minimum size
	if written < config.MinBinarySize {
		os.Remove(destPath)
		return fmt.Errorf("downloaded file too small (%d bytes, expected >%d)", written, config.MinBinarySize)
	}

	return nil
}
