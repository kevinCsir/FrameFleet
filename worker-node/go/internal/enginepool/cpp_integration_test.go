package enginepool

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"framefleet/worker-node/go/internal/engineprotocol"
)

func TestCppEngineFakeOperations(t *testing.T) {
	binaryPath := cppEngineBinaryPath(t)
	if _, err := os.Stat(binaryPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("C++ engine binary not found at %s; build it with: cmake -S worker-node/cpp -B worker-node/cpp/build && cmake --build worker-node/cpp/build", binaryPath)
		}
		t.Fatalf("stat C++ engine binary: %v", err)
	}

	pool, err := New(Config{
		Slots:      1,
		BinaryPath: binaryPath,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New pool: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start pool: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := pool.Stop(stopCtx); err != nil {
			t.Fatalf("Stop pool: %v", err)
		}
	}()

	tmp := t.TempDir()
	inputPath := filepath.Join(tmp, "input.mp4")
	writeFile(t, inputPath, []byte("fake-video-bytes"))

	t.Run("ping", func(t *testing.T) {
		resp := callEngine(t, pool, engineprotocol.Request{Operation: engineprotocol.OpPing})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
	})

	t.Run("process_internal_simple", func(t *testing.T) {
		outputPath := filepath.Join(tmp, "results", "job_123.gif")
		resp := callEngine(t, pool, engineprotocol.Request{
			Operation: engineprotocol.OpProcessInternalSimple,
			Input:     fileRef(inputPath, "input.mp4"),
			Output:    fileRef(outputPath, "job_123.gif"),
		})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
		assertFileEquals(t, outputPath, []byte("fake-video-bytes"))
	})

	t.Run("split_video", func(t *testing.T) {
		outputDir := filepath.Join(tmp, "outgoing", "job_123")
		resp := callEngine(t, pool, engineprotocol.Request{
			Operation:    engineprotocol.OpSplitVideo,
			SegmentCount: 3,
			Input:        fileRef(inputPath, "input.mp4"),
			OutputDir:    outputDir,
		})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
		if len(resp.Segments) != 3 {
			t.Fatalf("segments = %d, want 3", len(resp.Segments))
		}
		assertFileEquals(t, filepath.Join(outputDir, "segment_0.mp4"), []byte("fake-v"))
		assertFileEquals(t, filepath.Join(outputDir, "segment_1.mp4"), []byte("ideo-"))
		assertFileEquals(t, filepath.Join(outputDir, "segment_2.mp4"), []byte("bytes"))
	})

	t.Run("process_segment", func(t *testing.T) {
		outputPath := filepath.Join(tmp, "artifacts", "task_123.segment")
		resp := callEngine(t, pool, engineprotocol.Request{
			Operation: engineprotocol.OpProcessSegment,
			Input:     fileRef(inputPath, "task_123.input"),
			Output:    fileRef(outputPath, "task_123.segment"),
		})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
		assertFileEquals(t, outputPath, []byte("fake-video-bytes"))
	})

	t.Run("assemble_gif", func(t *testing.T) {
		first := filepath.Join(tmp, "artifacts", "first.segment")
		second := filepath.Join(tmp, "artifacts", "second.segment")
		outputPath := filepath.Join(tmp, "results", "assembled.gif")
		writeFile(t, first, []byte("first-"))
		writeFile(t, second, []byte("second"))

		resp := callEngine(t, pool, engineprotocol.Request{
			Operation: engineprotocol.OpAssembleGIF,
			Inputs: []engineprotocol.FileRef{
				*fileRef(first, "first.segment"),
				*fileRef(second, "second.segment"),
			},
			Output: fileRef(outputPath, "assembled.gif"),
		})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
		assertFileEquals(t, outputPath, []byte("first-second"))
	})
}

func cppEngineBinaryPath(t *testing.T) string {
	t.Helper()

	if path := os.Getenv("FRAMEFLEET_ENGINE_BINARY"); path != "" {
		return path
	}

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "cpp", "build", "framefleet-engine"))
}

func callEngine(t *testing.T, pool *Pool, req engineprotocol.Request) engineprotocol.Response {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := pool.Call(ctx, req)
	if err != nil {
		t.Fatalf("engine call failed: %v", err)
	}
	return resp
}

func fileRef(path string, name string) *engineprotocol.FileRef {
	return &engineprotocol.FileRef{
		Mode: engineprotocol.DataModeFile,
		Path: path,
		Name: name,
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func assertFileEquals(t *testing.T, path string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file %s = %q, want %q", path, got, want)
	}
}
