package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"framefleet/pkg/protocol"
)

func main() {
	entryAddr := flag.String("entry", "http://127.0.0.1:8080", "entry server base URL")
	host := flag.String("host", "127.0.0.1", "worker advertised host")
	port := flag.Int("port", 9001, "worker advertised port")
	totalSlots := flag.Int("slots", 4, "worker total slots")
	diskTotal := flag.Int64("disk-total", 1000*1000*1000, "worker disk total bytes")
	diskFree := flag.Int64("disk-free", 800*1000*1000, "worker disk free bytes")
	heartbeatInterval := flag.Duration("heartbeat-interval", 10*time.Second, "heartbeat interval")
	flag.Parse()

	address := fmt.Sprintf("%s:%d", *host, *port)
	workerID, err := registerWorker(*entryAddr, protocol.RegisterWorkerRequest{
		Address:        address,
		TotalSlots:     *totalSlots,
		SupportedTasks: []protocol.TaskType{protocol.TaskTypeProcessSegment, protocol.TaskTypeAssembleGIF},
		DiskTotalBytes: *diskTotal,
		DiskFreeBytes:  *diskFree,
	})
	if err != nil {
		log.Fatalf("register worker failed: %v", err)
	}

	log.Printf("registered worker_id=%s address=%s", workerID, address)

	ticker := time.NewTicker(*heartbeatInterval)
	defer ticker.Stop()

	for {
		if err := heartbeatWorker(*entryAddr, protocol.HeartbeatWorkerRequest{
			WorkerID:              workerID,
			TotalSlots:            *totalSlots,
			RunningProcessSegment: 0,
			RunningAssembleGIF:    0,
			RunningTasks:          []protocol.RunningTask{},
			DiskTotalBytes:        *diskTotal,
			DiskFreeBytes:         *diskFree,
			Metrics: map[protocol.TaskType]protocol.TaskRunMetric{
				protocol.TaskTypeProcessSegment: {
					CompletedCount:  0,
					TotalDurationMS: 0,
				},
				protocol.TaskTypeAssembleGIF: {
					CompletedCount:  0,
					TotalDurationMS: 0,
				},
			},
		}); err != nil {
			log.Printf("heartbeat failed: %v", err)
		} else {
			log.Printf("heartbeat sent worker_id=%s", workerID)
		}

		<-ticker.C
	}
}

func registerWorker(entryAddr string, req protocol.RegisterWorkerRequest) (string, error) {
	var resp protocol.RegisterWorkerResponse
	if err := postJSON(entryAddr+"/workers/register", req, &resp); err != nil {
		return "", err
	}
	if resp.Status != protocol.RegisterWorkerStatusSuccess && resp.Status != protocol.RegisterWorkerStatusExists {
		return "", fmt.Errorf("unexpected register status: %s", resp.Status)
	}
	if resp.WorkerID == "" {
		return "", fmt.Errorf("register response missing worker_id")
	}
	if resp.SplitPolicy.TargetSegmentDurationMS > 0 || resp.SplitPolicy.TargetSegmentSizeBytes > 0 || resp.SplitPolicy.MaxSegments > 0 {
		log.Printf("entry split_policy target_duration_ms=%d target_size_bytes=%d max_segments=%d",
			resp.SplitPolicy.TargetSegmentDurationMS,
			resp.SplitPolicy.TargetSegmentSizeBytes,
			resp.SplitPolicy.MaxSegments,
		)
	}
	return resp.WorkerID, nil
}

func heartbeatWorker(entryAddr string, req protocol.HeartbeatWorkerRequest) error {
	var resp protocol.HeartbeatWorkerResponse
	if err := postJSON(entryAddr+"/workers/heartbeat", req, &resp); err != nil {
		return err
	}
	if resp.Status != protocol.HeartbeatWorkerStatusSuccess {
		return fmt.Errorf("unexpected heartbeat status: %s", resp.Status)
	}
	return nil
}

func postJSON(url string, req any, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpResp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("http status %d", httpResp.StatusCode)
	}

	return nil
}
