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
	"sync/atomic"
	"time"
)

var artifactDownloadIdleTimeout = 30 * time.Second

type Client struct {
	http         *http.Client
	transferHTTP *http.Client
}

func New() *Client {
	return &Client{
		http:         &http.Client{Timeout: 30 * time.Second},
		transferHTTP: &http.Client{},
	}
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
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	client := c.transferHTTP
	if client == nil {
		client = &http.Client{}
	}
	var idleExpired atomic.Bool
	idleTimer := time.AfterFunc(artifactDownloadIdleTimeout, func() {
		idleExpired.Store(true)
		cancel()
	})
	defer idleTimer.Stop()

	resp, err := client.Do(req)
	if err != nil {
		if idleExpired.Load() {
			return fmt.Errorf("artifact download idle timeout after %s", artifactDownloadIdleTimeout)
		}
		return err
	}
	defer resp.Body.Close()
	resetIdleTimer(idleTimer, &idleExpired, artifactDownloadIdleTimeout)

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
	_, copyErr := copyWithIdleTimeout(file, resp.Body, idleTimer, &idleExpired, artifactDownloadIdleTimeout)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		if idleExpired.Load() {
			return fmt.Errorf("artifact download idle timeout after %s", artifactDownloadIdleTimeout)
		}
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}

	return os.Rename(tmpPath, outputPath)
}

func copyWithIdleTimeout(dst io.Writer, src io.Reader, timer *time.Timer, idleExpired *atomic.Bool, timeout time.Duration) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			resetIdleTimer(timer, idleExpired, timeout)
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nw != nr {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}

func resetIdleTimer(timer *time.Timer, idleExpired *atomic.Bool, timeout time.Duration) {
	idleExpired.Store(false)
	timer.Stop()
	timer.Reset(timeout)
}
