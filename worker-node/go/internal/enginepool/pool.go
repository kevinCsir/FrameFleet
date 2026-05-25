package enginepool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"framefleet/worker-node/go/internal/engineprotocol"
)

var ErrNoIdleEngine = errors.New("no idle engine available")

type Config struct {
	Slots              int
	BinaryPath         string
	DataDir            string
	CannyLowThreshold  int
	CannyHighThreshold int
}

type Pool struct {
	cfg     Config
	logger  *slog.Logger
	makeCmd commandFactory

	mu      sync.Mutex
	started bool
	engines []*Engine
	idle    chan *Engine
	slots   map[int]*slotState
}

type SlotSnapshot struct {
	SlotID      int    `json:"slot_id"`
	State       string `json:"state"`
	Operation   string `json:"op,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
	HeldMS      int64  `json:"held_ms,omitempty"`
	ExecutingMS int64  `json:"executing_ms,omitempty"`
}

type slotState struct {
	state       string
	operation   string
	requestID   string
	leasedAt    time.Time
	executingAt time.Time
}

const (
	slotStateIdle      = "idle"
	slotStateLeased    = "leased"
	slotStateExecuting = "executing"
)

func New(cfg Config, logger *slog.Logger) (*Pool, error) {
	if cfg.Slots <= 0 {
		return nil, fmt.Errorf("engine slots must be positive")
	}
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("engine binary path is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Pool{
		cfg:     cfg,
		logger:  logger,
		makeCmd: defaultCommandFactory,
	}, nil
}

func (p *Pool) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	p.logger.Info("engine pool start requested",
		"event", "engine_pool_start",
		"slots", p.cfg.Slots,
		"binary_path", p.cfg.BinaryPath,
		"data_dir", p.cfg.DataDir,
	)

	p.engines = make([]*Engine, 0, p.cfg.Slots)
	p.idle = make(chan *Engine, p.cfg.Slots)
	p.slots = make(map[int]*slotState, p.cfg.Slots)
	for id := 0; id < p.cfg.Slots; id++ {
		engine := newEngine(id, p.cfg, p.logger, p.makeCmd)
		if err := engine.Start(ctx); err != nil {
			_ = p.stopStartedEnginesLocked(context.Background())
			return err
		}
		p.engines = append(p.engines, engine)
		p.slots[id] = &slotState{state: slotStateIdle}
		p.idle <- engine
	}

	p.started = true
	return nil
}

func (p *Pool) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	p.logger.Info("engine pool stop requested", "event", "engine_pool_stop")

	err := p.stopStartedEnginesLocked(ctx)
	p.started = false
	p.engines = nil
	p.idle = nil
	p.slots = nil
	return err
}

func (p *Pool) Slots() int {
	return p.cfg.Slots
}

func (p *Pool) Snapshot() []SlotSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshots := make([]SlotSnapshot, 0, p.cfg.Slots)
	now := time.Now()
	for id := 0; id < p.cfg.Slots; id++ {
		state := p.slots[id]
		if state == nil {
			snapshots = append(snapshots, SlotSnapshot{SlotID: id, State: slotStateIdle})
			continue
		}

		snapshot := SlotSnapshot{
			SlotID:    id,
			State:     state.state,
			Operation: state.operation,
			RequestID: state.requestID,
		}
		if !state.leasedAt.IsZero() {
			snapshot.HeldMS = now.Sub(state.leasedAt).Milliseconds()
		}
		if !state.executingAt.IsZero() {
			snapshot.ExecutingMS = now.Sub(state.executingAt).Milliseconds()
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func (p *Pool) Acquire(ctx context.Context) (*Lease, error) {
	engine, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	p.markLeased(engine)
	return &Lease{pool: p, engine: engine}, nil
}

func (p *Pool) TryAcquire() (*Lease, error) {
	engine, err := p.tryAcquire()
	if err != nil {
		return nil, err
	}
	p.markLeased(engine)
	return &Lease{pool: p, engine: engine}, nil
}

func (p *Pool) Call(ctx context.Context, req engineprotocol.Request) (engineprotocol.Response, error) {
	engine, err := p.acquire(ctx)
	if err != nil {
		return engineprotocol.Response{}, err
	}
	defer p.release(engine)
	req = prepareRequest(req)
	p.markExecuting(engine, req)
	defer p.markLeased(engine)

	return engine.Call(ctx, req)
}

func (p *Pool) TryCall(ctx context.Context, req engineprotocol.Request) (engineprotocol.Response, error) {
	engine, err := p.tryAcquire()
	if err != nil {
		return engineprotocol.Response{}, err
	}
	defer p.release(engine)
	req = prepareRequest(req)
	p.markExecuting(engine, req)
	defer p.markLeased(engine)

	return engine.Call(ctx, req)
}

func (p *Pool) acquire(ctx context.Context) (*Engine, error) {
	p.mu.Lock()
	if !p.started || p.idle == nil {
		p.mu.Unlock()
		return nil, errors.New("engine pool is not started")
	}
	idle := p.idle
	p.mu.Unlock()

	select {
	case engine := <-idle:
		return engine, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Pool) tryAcquire() (*Engine, error) {
	p.mu.Lock()
	if !p.started || p.idle == nil {
		p.mu.Unlock()
		return nil, errors.New("engine pool is not started")
	}
	idle := p.idle
	p.mu.Unlock()

	select {
	case engine := <-idle:
		return engine, nil
	default:
		return nil, ErrNoIdleEngine
	}
}

func (p *Pool) release(engine *Engine) {
	p.mu.Lock()
	if !p.started || p.idle == nil {
		p.mu.Unlock()
		return
	}
	p.markIdleLocked(engine)
	idle := p.idle
	p.mu.Unlock()

	idle <- engine
}

func (p *Pool) markLeased(engine *Engine) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.ensureSlotStateLocked(engine.id)
	state.state = slotStateLeased
	state.operation = ""
	state.requestID = ""
	state.leasedAt = time.Now()
	state.executingAt = time.Time{}
}

func (p *Pool) markExecuting(engine *Engine, req engineprotocol.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.ensureSlotStateLocked(engine.id)
	if state.leasedAt.IsZero() {
		state.leasedAt = time.Now()
	}
	state.state = slotStateExecuting
	state.operation = string(req.Operation)
	state.requestID = req.RequestID
	state.executingAt = time.Now()
}

func (p *Pool) markIdleLocked(engine *Engine) {
	state := p.ensureSlotStateLocked(engine.id)
	state.state = slotStateIdle
	state.operation = ""
	state.requestID = ""
	state.leasedAt = time.Time{}
	state.executingAt = time.Time{}
}

func (p *Pool) ensureSlotStateLocked(id int) *slotState {
	if p.slots == nil {
		p.slots = make(map[int]*slotState, p.cfg.Slots)
	}
	state := p.slots[id]
	if state == nil {
		state = &slotState{state: slotStateIdle}
		p.slots[id] = state
	}
	return state
}

func (p *Pool) stopStartedEnginesLocked(ctx context.Context) error {
	var firstErr error
	for _, engine := range p.engines {
		if err := engine.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func nextRequestID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "req_fallback"
	}
	return "req_" + hex.EncodeToString(raw[:])
}

func prepareRequest(req engineprotocol.Request) engineprotocol.Request {
	if req.Version == 0 {
		req.Version = engineprotocol.Version
	}
	if req.RequestID == "" {
		req.RequestID = nextRequestID()
	}
	return req
}
