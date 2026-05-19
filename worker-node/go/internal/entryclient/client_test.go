package entryclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"framefleet/pkg/protocol"
)

func TestRegisterWorker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workers/register" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(protocol.RegisterWorkerResponse{
			Status:   protocol.RegisterWorkerStatusSuccess,
			WorkerID: "wrk_123",
			SplitPolicy: protocol.SplitPolicy{
				TargetSegmentDurationMS: 10_000,
			},
		})
	}))
	defer server.Close()

	client := New(server.URL)
	resp, err := client.RegisterWorker(context.Background(), protocol.RegisterWorkerRequest{
		Address:        "127.0.0.1:9001",
		TotalSlots:     4,
		SupportedTasks: []protocol.TaskType{protocol.TaskTypeProcessSegment},
	})
	if err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}
	if resp.WorkerID != "wrk_123" {
		t.Fatalf("worker_id = %q", resp.WorkerID)
	}
	if resp.SplitPolicy.TargetSegmentDurationMS != 10_000 {
		t.Fatalf("split duration = %d", resp.SplitPolicy.TargetSegmentDurationMS)
	}
}

func TestHeartbeatWorker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workers/heartbeat" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(protocol.HeartbeatWorkerResponse{
			Status: protocol.HeartbeatWorkerStatusSuccess,
		})
	}))
	defer server.Close()

	client := New(server.URL)
	if _, err := client.HeartbeatWorker(context.Background(), protocol.HeartbeatWorkerRequest{
		WorkerID:       "wrk_123",
		TotalSlots:     4,
		DiskTotalBytes: 100,
		DiskFreeBytes:  80,
	}); err != nil {
		t.Fatalf("HeartbeatWorker failed: %v", err)
	}
}
