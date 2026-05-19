package entryclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"framefleet/pkg/protocol"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) RegisterWorker(ctx context.Context, req protocol.RegisterWorkerRequest) (protocol.RegisterWorkerResponse, error) {
	var resp protocol.RegisterWorkerResponse
	if err := c.postJSON(ctx, "/workers/register", req, &resp); err != nil {
		return protocol.RegisterWorkerResponse{}, err
	}
	if resp.Status != protocol.RegisterWorkerStatusSuccess && resp.Status != protocol.RegisterWorkerStatusExists {
		return resp, fmt.Errorf("unexpected register status: %s", resp.Status)
	}
	if resp.WorkerID == "" {
		return resp, fmt.Errorf("register response missing worker_id")
	}
	return resp, nil
}

func (c *Client) HeartbeatWorker(ctx context.Context, req protocol.HeartbeatWorkerRequest) (protocol.HeartbeatWorkerResponse, error) {
	var resp protocol.HeartbeatWorkerResponse
	if err := c.postJSON(ctx, "/workers/heartbeat", req, &resp); err != nil {
		return protocol.HeartbeatWorkerResponse{}, err
	}
	if resp.Status != protocol.HeartbeatWorkerStatusSuccess {
		return resp, fmt.Errorf("unexpected heartbeat status: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) CreateJob(ctx context.Context, req protocol.CreateJobRequest) (protocol.CreateJobResponse, error) {
	var resp protocol.CreateJobResponse
	if err := c.postJSON(ctx, "/jobs", req, &resp); err != nil {
		return protocol.CreateJobResponse{}, err
	}
	if resp.Status != protocol.CreateJobStatusSuccess && resp.Status != protocol.CreateJobStatusAlreadyExists {
		return resp, fmt.Errorf("unexpected create job status: %s", resp.Status)
	}
	if resp.JobID == "" {
		return resp, fmt.Errorf("create job response missing job_id")
	}
	return resp, nil
}

func (c *Client) AcceptTask(ctx context.Context, taskID string, req protocol.TaskAcceptedRequest) (protocol.TaskUpdateResponse, error) {
	var resp protocol.TaskUpdateResponse
	if err := c.postJSON(ctx, "/tasks/"+taskID+"/accepted", req, &resp); err != nil {
		return protocol.TaskUpdateResponse{}, err
	}
	if resp.Status != protocol.TaskUpdateStatusSuccess {
		return resp, fmt.Errorf("unexpected task accepted status: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) CompleteTask(ctx context.Context, taskID string, req protocol.TaskCompletedRequest) (protocol.TaskUpdateResponse, error) {
	var resp protocol.TaskUpdateResponse
	if err := c.postJSON(ctx, "/tasks/"+taskID+"/completed", req, &resp); err != nil {
		return protocol.TaskUpdateResponse{}, err
	}
	if resp.Status != protocol.TaskUpdateStatusSuccess {
		return resp, fmt.Errorf("unexpected task completed status: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) FailTask(ctx context.Context, taskID string, req protocol.TaskFailedRequest) (protocol.TaskUpdateResponse, error) {
	var resp protocol.TaskUpdateResponse
	if err := c.postJSON(ctx, "/tasks/"+taskID+"/failed", req, &resp); err != nil {
		return protocol.TaskUpdateResponse{}, err
	}
	if resp.Status != protocol.TaskUpdateStatusSuccess {
		return resp, fmt.Errorf("unexpected task failed status: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) ReportAssembled(ctx context.Context, jobID string, req protocol.JobAssembledRequest) (protocol.JobAssembledResponse, error) {
	var resp protocol.JobAssembledResponse
	if err := c.postJSON(ctx, "/jobs/"+jobID+"/assembled", req, &resp); err != nil {
		return protocol.JobAssembledResponse{}, err
	}
	if resp.Status != protocol.JobResultUpdateStatusSuccess {
		return resp, fmt.Errorf("unexpected job assembled status: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) postJSON(ctx context.Context, path string, req any, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	if err := json.NewDecoder(httpResp.Body).Decode(resp); err != nil {
		return err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("entry returned http status %d", httpResp.StatusCode)
	}
	return nil
}
