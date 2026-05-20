package enginepool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"framefleet/worker-node/go/internal/engineprotocol"
)

type commandFactory func(context.Context, Config, int) *exec.Cmd

type Engine struct {
	id      int
	cfg     Config
	logger  *slog.Logger
	makeCmd commandFactory

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	started bool
}

func newEngine(id int, cfg Config, logger *slog.Logger, makeCmd commandFactory) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	if makeCmd == nil {
		makeCmd = defaultCommandFactory
	}
	return &Engine{id: id, cfg: cfg, logger: logger, makeCmd: makeCmd}
}

func defaultCommandFactory(ctx context.Context, cfg Config, id int) *exec.Cmd {
	cmd := exec.CommandContext(ctx, cfg.BinaryPath)
	cmd.Env = append(os.Environ(),
		"FRAMEFLEET_CANNY_LOW_THRESHOLD="+strconv.Itoa(cfg.CannyLowThreshold),
		"FRAMEFLEET_CANNY_HIGH_THRESHOLD="+strconv.Itoa(cfg.CannyHighThreshold),
	)
	return cmd
}

func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.started {
		return nil
	}

	cmd := e.makeCmd(ctx, e.cfg, e.id)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create engine stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create engine stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create engine stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start engine process: %w", err)
	}

	e.cmd = cmd
	e.stdin = stdin
	e.stdout = bufio.NewReader(stdout)
	e.started = true

	go e.logStderr(stderr)

	e.logger.Info("engine process started",
		"event", "engine_process_started",
		"engine_id", e.id,
		"pid", cmd.Process.Pid,
	)

	return nil
}

func (e *Engine) Call(ctx context.Context, req engineprotocol.Request) (engineprotocol.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.started {
		return engineprotocol.Response{}, errors.New("engine is not started")
	}

	if req.Version == 0 {
		req.Version = engineprotocol.Version
	}
	if req.RequestID == "" {
		req.RequestID = nextRequestID()
	}

	body, err := json.Marshal(req)
	if err != nil {
		return engineprotocol.Response{}, fmt.Errorf("marshal engine request: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return engineprotocol.Response{}, err
	}

	if err := writeLine(e.stdin, body); err != nil {
		return engineprotocol.Response{}, err
	}

	line, err := readLine(e.stdout)
	if err != nil {
		return engineprotocol.Response{}, err
	}

	var resp engineprotocol.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return engineprotocol.Response{}, fmt.Errorf("unmarshal engine response: %w", err)
	}
	if resp.RequestID != req.RequestID {
		return engineprotocol.Response{}, fmt.Errorf("engine response request_id mismatch: got %q want %q", resp.RequestID, req.RequestID)
	}
	if resp.Version != engineprotocol.Version {
		return engineprotocol.Response{}, fmt.Errorf("engine response version mismatch: got %d want %d", resp.Version, engineprotocol.Version)
	}
	if err := ctx.Err(); err != nil {
		return engineprotocol.Response{}, err
	}

	return resp, nil
}

func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.started {
		return nil
	}

	if e.stdin != nil {
		_ = e.stdin.Close()
	}

	done := make(chan error, 1)
	go func() {
		done <- e.cmd.Wait()
	}()

	select {
	case err := <-done:
		e.started = false
		if err != nil {
			return fmt.Errorf("wait engine process: %w", err)
		}
		e.logger.Info("engine process stopped", "event", "engine_process_stopped", "engine_id", e.id)
		return nil
	case <-ctx.Done():
		if e.cmd != nil && e.cmd.Process != nil {
			_ = e.cmd.Process.Kill()
		}
		e.started = false
		return ctx.Err()
	}
}

func (e *Engine) logStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		e.logger.Info("engine stderr",
			"event", "engine_stderr",
			"engine_id", e.id,
			"message", scanner.Text(),
		)
	}
	if err := scanner.Err(); err != nil {
		e.logger.Warn("engine stderr read failed",
			"event", "engine_stderr_read_failed",
			"engine_id", e.id,
			"error", err,
		)
	}
}

func writeLine(writer io.Writer, body []byte) error {
	if _, err := writer.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write engine request: %w", err)
	}
	return nil
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read engine response: %w", err)
	}
	return line, nil
}
