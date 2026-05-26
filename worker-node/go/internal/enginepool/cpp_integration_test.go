package enginepool

import (
	"bytes"
	"context"
	"errors"
	"framefleet/pkg/protocol"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"framefleet/worker-node/go/internal/engineprotocol"
)

func TestCppEngineFakeOperations(t *testing.T) {
	t.Setenv("FRAMEFLEET_ENGINE_FAKE_SPLIT", "1")
	t.Setenv("FRAMEFLEET_ENGINE_FAKE_PROCESS", "1")
	t.Setenv("FRAMEFLEET_ENGINE_FAKE_ASSEMBLE", "1")

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
			Operation:               engineprotocol.OpSplitVideo,
			TargetSegmentSizeBytes:  1024 * 1024,
			TargetSegmentDurationMS: 10_000,
			MaxSegments:             3,
			Input:                   fileRef(inputPath, "input.mp4"),
			OutputDir:               outputDir,
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

	t.Run("split_video_uncapped_size_target", func(t *testing.T) {
		outputDir := filepath.Join(tmp, "outgoing", "job_uncapped")
		resp := callEngine(t, pool, engineprotocol.Request{
			Operation:              engineprotocol.OpSplitVideo,
			TargetSegmentSizeBytes: 5,
			MaxSegments:            0,
			Input:                  fileRef(inputPath, "input.mp4"),
			OutputDir:              outputDir,
		})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
		if len(resp.Segments) != 4 {
			t.Fatalf("segments = %d, want 4", len(resp.Segments))
		}
		assertFileEquals(t, filepath.Join(outputDir, "segment_0.mp4"), []byte("fake"))
		assertFileEquals(t, filepath.Join(outputDir, "segment_1.mp4"), []byte("-vid"))
		assertFileEquals(t, filepath.Join(outputDir, "segment_2.mp4"), []byte("eo-b"))
		assertFileEquals(t, filepath.Join(outputDir, "segment_3.mp4"), []byte("ytes"))
	})

	t.Run("process_segment", func(t *testing.T) {
		outputPath := filepath.Join(tmp, "artifacts", "task_123.gif")
		resp := callEngine(t, pool, engineprotocol.Request{
			Operation: engineprotocol.OpProcessSegment,
			Input:     fileRef(inputPath, "task_123.input"),
			Output:    fileRef(outputPath, "task_123.gif"),
		})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
		assertFileEquals(t, outputPath, []byte("fake-video-bytes"))
	})

	t.Run("assemble_gif", func(t *testing.T) {
		first := filepath.Join(tmp, "artifacts", "first.gif")
		second := filepath.Join(tmp, "artifacts", "second.gif")
		outputPath := filepath.Join(tmp, "results", "assembled.gif")
		writeFile(t, first, []byte("first-"))
		writeFile(t, second, []byte("second"))

		resp := callEngine(t, pool, engineprotocol.Request{
			Operation: engineprotocol.OpAssembleGIF,
			Inputs: []engineprotocol.FileRef{
				*fileRef(first, "first.gif"),
				*fileRef(second, "second.gif"),
			},
			Output: fileRef(outputPath, "assembled.gif"),
		})
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("type = %q, want completed", resp.Type)
		}
		assertFileEquals(t, outputPath, []byte("first-second"))
	})
}

func TestCppEngineRealVideoPipeline(t *testing.T) {
	binaryPath := cppEngineBinaryPath(t)
	if _, err := os.Stat(binaryPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("C++ engine binary not found at %s; build it with: cmake -S worker-node/cpp -B worker-node/cpp/build && cmake --build worker-node/cpp/build", binaryPath)
		}
		t.Fatalf("stat C++ engine binary: %v", err)
	}

	videoPath := cppTestVideoPath(t)
	if _, err := os.Stat(videoPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("test video not found at %s", videoPath)
		}
		t.Fatalf("stat test video: %v", err)
	}

	pool, err := New(Config{
		Slots:              1,
		BinaryPath:         binaryPath,
		CannyLowThreshold:  180,
		CannyHighThreshold: 360,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New pool: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	splitDir := filepath.Join(tmp, "split")
	splitResp := callEngineWithTimeout(t, pool, 10*time.Second, engineprotocol.Request{
		Operation:               engineprotocol.OpSplitVideo,
		TargetSegmentDurationMS: 1000,
		MaxSegments:             2,
		Input:                   fileRef(videoPath, filepath.Base(videoPath)),
		OutputDir:               splitDir,
	})
	if splitResp.Type != engineprotocol.ResponseTypeCompleted {
		t.Fatalf("split type = %q, reason = %q", splitResp.Type, splitResp.Reason)
	}
	if len(splitResp.Segments) == 0 || len(splitResp.Segments) > 2 {
		t.Fatalf("split segments = %d, want 1..2", len(splitResp.Segments))
	}

	inputs := make([]engineprotocol.FileRef, 0, len(splitResp.Segments))
	for _, segment := range splitResp.Segments {
		if segment.SizeBytes <= 0 {
			t.Fatalf("segment %d has size %d", segment.SegmentIndex, segment.SizeBytes)
		}
		artifactPath := filepath.Join(tmp, "artifacts", segment.Name+".gif")
		processResp := callEngineWithTimeout(t, pool, 10*time.Second, engineprotocol.Request{
			Operation: engineprotocol.OpProcessSegment,
			Input:     fileRef(segment.Path, segment.Name),
			Output:    fileRef(artifactPath, filepath.Base(artifactPath)),
		})
		if processResp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("process type = %q, reason = %q", processResp.Type, processResp.Reason)
		}
		if processResp.FrameCount <= 0 || processResp.OutputSizeBytes <= 0 {
			t.Fatalf("process returned frame_count=%d output_size_bytes=%d", processResp.FrameCount, processResp.OutputSizeBytes)
		}
		inputs = append(inputs, *fileRef(artifactPath, filepath.Base(artifactPath)))
	}

	for _, mode := range []protocol.GIFAssembleMode{
		protocol.GIFAssembleModeLocalPaletteConcat,
		protocol.GIFAssembleModeGlobalPaletteRecode,
	} {
		t.Run(string(mode), func(t *testing.T) {
			resultPath := filepath.Join(tmp, "results", string(mode)+".gif")
			assembleResp := callEngineWithTimeout(t, pool, 15*time.Second, engineprotocol.Request{
				Operation:    engineprotocol.OpAssembleGIF,
				AssembleMode: mode,
				Inputs:       inputs,
				Output:       fileRef(resultPath, "result.gif"),
			})
			if assembleResp.Type != engineprotocol.ResponseTypeCompleted {
				t.Fatalf("assemble type = %q, reason = %q", assembleResp.Type, assembleResp.Reason)
			}
			if assembleResp.FrameCount <= 0 || assembleResp.OutputSizeBytes <= 0 {
				t.Fatalf("assemble returned frame_count=%d output_size_bytes=%d", assembleResp.FrameCount, assembleResp.OutputSizeBytes)
			}
			assertGIFFile(t, resultPath)
		})
	}
}

func TestCppEngineUncappedSplitHonorsSizeTargetWithinTolerance(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not found: %v", err)
	}

	binaryPath := cppEngineBinaryPath(t)
	if _, err := os.Stat(binaryPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("C++ engine binary not found at %s; build it with: cmake -S worker-node/cpp -B worker-node/cpp/build && cmake --build worker-node/cpp/build", binaryPath)
		}
		t.Fatalf("stat C++ engine binary: %v", err)
	}

	tmp := t.TempDir()
	videoPath := filepath.Join(tmp, "long.mp4")
	generateSegmentableTestVideo(t, videoPath)

	info, err := os.Stat(videoPath)
	if err != nil {
		t.Fatalf("stat generated video: %v", err)
	}
	targetSizeBytes := info.Size() / 4
	if targetSizeBytes <= 0 {
		t.Fatalf("generated video too small: %d", info.Size())
	}

	pool, err := New(Config{
		Slots:      1,
		BinaryPath: binaryPath,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New pool: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	splitDir := filepath.Join(tmp, "split")
	splitResp := callEngineWithTimeout(t, pool, 10*time.Second, engineprotocol.Request{
		Operation:              engineprotocol.OpSplitVideo,
		TargetSegmentSizeBytes: targetSizeBytes,
		MaxSegments:            0,
		Input:                  fileRef(videoPath, filepath.Base(videoPath)),
		OutputDir:              splitDir,
	})
	if splitResp.Type != engineprotocol.ResponseTypeCompleted {
		t.Fatalf("split type = %q, reason = %q", splitResp.Type, splitResp.Reason)
	}
	if len(splitResp.Segments) < 4 {
		t.Fatalf("split segments = %d, want at least 4", len(splitResp.Segments))
	}

	maxAllowedSize := targetSizeBytes * 2
	for _, segment := range splitResp.Segments {
		if segment.SizeBytes <= 0 {
			t.Fatalf("segment %d has size %d", segment.SegmentIndex, segment.SizeBytes)
		}
		if segment.SizeBytes > maxAllowedSize {
			t.Fatalf("segment %d size = %d, want <= %d (target %d)",
				segment.SegmentIndex, segment.SizeBytes, maxAllowedSize, targetSizeBytes)
		}
	}
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

func cppTestVideoPath(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "cpp", "testdata", "videos", "canny_source_short.mp4"))
}

func callEngine(t *testing.T, pool *Pool, req engineprotocol.Request) engineprotocol.Response {
	t.Helper()
	return callEngineWithTimeout(t, pool, 2*time.Second, req)
}

func callEngineWithTimeout(t *testing.T, pool *Pool, timeout time.Duration, req engineprotocol.Request) engineprotocol.Response {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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

func generateSegmentableTestVideo(t *testing.T, outputPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		t.Fatalf("mkdir video parent: %v", err)
	}
	cmd := exec.Command(
		"ffmpeg",
		"-hide_banner",
		"-v", "error",
		"-nostdin",
		"-y",
		"-f", "lavfi",
		"-i", "testsrc2=size=320x240:rate=15:duration=12",
		"-c:v", "libx264",
		"-g", "15",
		"-keyint_min", "15",
		"-sc_threshold", "0",
		"-pix_fmt", "yuv420p",
		outputPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate test video failed: %v\n%s", err, output)
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

func assertGIFFile(t *testing.T, path string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gif file %s: %v", path, err)
	}
	if len(got) < 6 {
		t.Fatalf("gif file %s is too small: %d bytes", path, len(got))
	}
	header := string(got[:6])
	if header != "GIF87a" && header != "GIF89a" {
		t.Fatalf("file %s has header %q, want GIF87a or GIF89a", path, header)
	}
}
