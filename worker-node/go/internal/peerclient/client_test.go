package peerclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadArtifactUsesIdleTimeoutNotTotalTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/artifacts/task_slow" {
			t.Fatalf("path = %q, want /artifacts/task_slow", r.URL.Path)
		}
		for _, chunk := range []string{"aa", "bb", "cc"} {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()

	client := New()
	client.http.Timeout = 10 * time.Millisecond
	client.transferHTTP = server.Client()

	outputPath := filepath.Join(t.TempDir(), "artifact.segment")
	if err := client.DownloadArtifact(context.Background(), strings.TrimPrefix(server.URL, "http://"), "task_slow", outputPath); err != nil {
		t.Fatalf("DownloadArtifact failed: %v", err)
	}

	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(body) != "aabbcc" {
		t.Fatalf("body = %q, want aabbcc", string(body))
	}
}

func TestDownloadArtifactReportsIdleTimeoutWhenStreamStalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("start")); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	oldTimeout := artifactDownloadIdleTimeout
	artifactDownloadIdleTimeout = 20 * time.Millisecond
	defer func() { artifactDownloadIdleTimeout = oldTimeout }()

	client := New()
	client.transferHTTP = server.Client()

	err := client.DownloadArtifact(context.Background(), strings.TrimPrefix(server.URL, "http://"), "task_stall", filepath.Join(t.TempDir(), "artifact.segment"))
	if err == nil {
		t.Fatal("DownloadArtifact succeeded, want idle timeout")
	}
	if !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("error = %v, want idle timeout", err)
	}
}
