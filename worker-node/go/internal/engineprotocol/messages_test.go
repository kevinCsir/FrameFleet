package engineprotocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestExamples(t *testing.T) {
	for _, name := range []string{
		"ping.request.json",
		"process_internal_simple.request.json",
		"split_video.request.json",
		"process_segment.request.json",
		"assemble_gif.request.json",
	} {
		t.Run(name, func(t *testing.T) {
			var req Request
			readExample(t, name, &req)

			if req.Version != Version {
				t.Fatalf("version = %d, want %d", req.Version, Version)
			}
			if req.RequestID == "" {
				t.Fatalf("request_id is required")
			}
			if req.Operation == "" {
				t.Fatalf("op is required")
			}
		})
	}
}

func TestResponseExamples(t *testing.T) {
	for _, name := range []string{
		"ping.completed.json",
		"process_internal_simple.completed.json",
		"split_video.completed.json",
		"process_segment.completed.json",
		"assemble_gif.completed.json",
		"failed.json",
	} {
		t.Run(name, func(t *testing.T) {
			var resp Response
			readExample(t, name, &resp)

			if resp.Version != Version {
				t.Fatalf("version = %d, want %d", resp.Version, Version)
			}
			if resp.RequestID == "" {
				t.Fatalf("request_id is required")
			}
			if resp.Type != ResponseTypeCompleted && resp.Type != ResponseTypeFailed {
				t.Fatalf("unexpected response type %q", resp.Type)
			}
			if resp.Type == ResponseTypeFailed && resp.Reason == "" {
				t.Fatalf("failed response reason is required")
			}
		})
	}
}

func TestOperationSpecificExamples(t *testing.T) {
	var split Request
	readExample(t, "split_video.request.json", &split)
	if split.Operation != OpSplitVideo || split.Input == nil || split.OutputDir == "" || split.SegmentCount <= 0 {
		t.Fatalf("invalid split_video request: %+v", split)
	}

	var process Request
	readExample(t, "process_segment.request.json", &process)
	if process.Operation != OpProcessSegment || process.Input == nil || process.Output == nil || process.TaskID == "" {
		t.Fatalf("invalid process_segment request: %+v", process)
	}

	var assemble Request
	readExample(t, "assemble_gif.request.json", &assemble)
	if assemble.Operation != OpAssembleGIF || len(assemble.Inputs) == 0 || assemble.Output == nil {
		t.Fatalf("invalid assemble_gif request: %+v", assemble)
	}

	var splitResp Response
	readExample(t, "split_video.completed.json", &splitResp)
	if len(splitResp.Segments) == 0 {
		t.Fatalf("split_video completed response requires segments")
	}
}

func readExample(t *testing.T, name string, target any) {
	t.Helper()

	if strings.Contains(name, string(filepath.Separator)) {
		t.Fatalf("example name must be a file name, got %q", name)
	}

	path := filepath.Join("..", "..", "..", "protocol", "examples", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read example %s: %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("unmarshal example %s: %v", path, err)
	}
}
