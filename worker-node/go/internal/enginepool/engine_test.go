package enginepool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"framefleet/worker-node/go/internal/engineprotocol"
)

func TestPoolCall(t *testing.T) {
	pool := newTestPool(t, 2)
	startPool(t, pool)
	defer stopPool(t, pool)

	resp, err := pool.Call(context.Background(), engineprotocol.Request{Operation: engineprotocol.OpPing})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	if resp.Type != engineprotocol.ResponseTypeCompleted {
		t.Fatalf("response type = %q, want %q", resp.Type, engineprotocol.ResponseTypeCompleted)
	}
}

func TestPoolCallWaitsForIdleEngine(t *testing.T) {
	pool := newTestPool(t, 1)
	startPool(t, pool)
	defer stopPool(t, pool)

	var firstDone atomic.Bool
	go func() {
		_, err := pool.Call(context.Background(), engineprotocol.Request{
			RequestID: "sleep",
			Operation: engineprotocol.OpPing,
		})
		if err != nil {
			t.Errorf("first call failed: %v", err)
		}
		firstDone.Store(true)
	}()

	time.Sleep(50 * time.Millisecond)
	if firstDone.Load() {
		t.Fatalf("first call completed before helper delay")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := pool.Call(ctx, engineprotocol.Request{Operation: engineprotocol.OpPing})
	if err != nil {
		t.Fatalf("second blocking call failed: %v", err)
	}
	if resp.Type != engineprotocol.ResponseTypeCompleted {
		t.Fatalf("response type = %q", resp.Type)
	}
}

func TestPoolTryCallReturnsNoIdleEngine(t *testing.T) {
	pool := newTestPool(t, 1)
	startPool(t, pool)
	defer stopPool(t, pool)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := pool.Call(context.Background(), engineprotocol.Request{
			RequestID: "sleep",
			Operation: engineprotocol.OpPing,
		})
		if err != nil {
			t.Errorf("blocking call failed: %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	_, err := pool.TryCall(context.Background(), engineprotocol.Request{Operation: engineprotocol.OpPing})
	if !errors.Is(err, ErrNoIdleEngine) {
		t.Fatalf("TryCall error = %v, want ErrNoIdleEngine", err)
	}

	<-done
}

func TestPoolCallDrainsResponseAfterContextCancel(t *testing.T) {
	pool := newTestPool(t, 1)
	startPool(t, pool)
	defer stopPool(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := pool.Call(ctx, engineprotocol.Request{
		RequestID: "sleep",
		Operation: engineprotocol.OpPing,
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first call error = %v, want context deadline exceeded", err)
	}

	resp, err := pool.Call(context.Background(), engineprotocol.Request{
		RequestID: "after_sleep",
		Operation: engineprotocol.OpPing,
	})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if resp.RequestID != "after_sleep" {
		t.Fatalf("second response request_id = %q, want after_sleep", resp.RequestID)
	}
}

func newTestPool(t *testing.T, slots int) *Pool {
	t.Helper()

	pool, err := New(Config{
		Slots:      slots,
		BinaryPath: os.Args[0],
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New pool: %v", err)
	}
	pool.makeCmd = func(ctx context.Context, cfg Config, id int) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestEngineHelperProcess", "--")
		cmd.Env = append(os.Environ(), "FRAMEFLEET_ENGINE_HELPER=1")
		return cmd
	}
	return pool
}

func startPool(t *testing.T, pool *Pool) {
	t.Helper()

	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("Start pool: %v", err)
	}
}

func stopPool(t *testing.T, pool *Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Stop(ctx); err != nil {
		t.Fatalf("Stop pool: %v", err)
	}
}

func TestEngineHelperProcess(t *testing.T) {
	if os.Getenv("FRAMEFLEET_ENGINE_HELPER") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req engineprotocol.Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = encoder.Encode(engineprotocol.Response{
				Version:   engineprotocol.Version,
				Type:      engineprotocol.ResponseTypeFailed,
				Reason:    err.Error(),
				Retryable: false,
			})
			continue
		}
		if req.RequestID == "sleep" {
			time.Sleep(150 * time.Millisecond)
		}
		_ = encoder.Encode(engineprotocol.Response{
			Version:   engineprotocol.Version,
			RequestID: req.RequestID,
			Type:      engineprotocol.ResponseTypeCompleted,
		})
	}

	os.Exit(0)
}

func TestPoolLease(t *testing.T) {
	pool := newTestPool(t, 1)
	startPool(t, pool)
	defer stopPool(t, pool)

	lease, err := pool.TryAcquire()
	if err != nil {
		t.Fatalf("TryAcquire failed: %v", err)
	}

	if _, err := pool.TryCall(context.Background(), engineprotocol.Request{Operation: engineprotocol.OpPing}); !errors.Is(err, ErrNoIdleEngine) {
		t.Fatalf("TryCall while leased error = %v, want ErrNoIdleEngine", err)
	}

	for i := 0; i < 2; i++ {
		resp, err := lease.Call(context.Background(), engineprotocol.Request{Operation: engineprotocol.OpPing})
		if err != nil {
			t.Fatalf("lease Call %d failed: %v", i, err)
		}
		if resp.Type != engineprotocol.ResponseTypeCompleted {
			t.Fatalf("lease response type = %q", resp.Type)
		}
	}

	lease.Release()
	if _, err := pool.TryCall(context.Background(), engineprotocol.Request{Operation: engineprotocol.OpPing}); err != nil {
		t.Fatalf("TryCall after lease release failed: %v", err)
	}

	if _, err := lease.Call(context.Background(), engineprotocol.Request{Operation: engineprotocol.OpPing}); !errors.Is(err, ErrLeaseReleased) {
		t.Fatalf("lease Call after release error = %v, want ErrLeaseReleased", err)
	}
}
