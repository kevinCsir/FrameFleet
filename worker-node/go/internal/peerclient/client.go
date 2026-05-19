package peerclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) UploadSegment(ctx context.Context, workerAddress string, taskID string, inputPath string) error {
	url := "http://" + strings.TrimRight(workerAddress, "/") + "/segments/" + taskID + "/upload"
	file, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, file)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var uploadResp struct {
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("segment upload returned http status %d", resp.StatusCode)
		}
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if uploadResp.Reason != "" {
			return fmt.Errorf("segment upload returned http status %d: %s", resp.StatusCode, uploadResp.Reason)
		}
		return fmt.Errorf("segment upload returned http status %d", resp.StatusCode)
	}
	if uploadResp.Status != "success" {
		if uploadResp.Reason == "" {
			uploadResp.Reason = "segment upload failed"
		}
		return errors.New(uploadResp.Reason)
	}
	return nil
}

func (c *Client) DownloadArtifact(ctx context.Context, workerAddress string, taskID string, outputPath string) error {
	url := "http://" + strings.TrimRight(workerAddress, "/") + "/artifacts/" + taskID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("artifact download returned http status %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}
	tmpPath := outputPath + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}

	return os.Rename(tmpPath, outputPath)
}
