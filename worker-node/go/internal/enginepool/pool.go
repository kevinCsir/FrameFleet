package enginepool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"framefleet/worker-node/go/internal/engineprotocol"
)

var ErrNoIdleEngine = errors.New("no idle engine available")

type Config struct {
	Slots      int
	BinaryPath string
	DataDir    string
}

type Pool struct {
	cfg     Config
	logger  *slog.Logger
	makeCmd commandFactory

	mu      sync.Mutex
	started bool
	engines []*Engine
	idle    chan *Engine
}

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
	for id := 0; id < p.cfg.Slots; id++ {
		engine := newEngine(id, p.cfg, p.logger, p.makeCmd)
		if err := engine.Start(ctx); err != nil {
			_ = p.stopStartedEnginesLocked(context.Background())
			return err
		}
		p.engines = append(p.engines, engine)
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
	return err
}

func (p *Pool) Slots() int {
	return p.cfg.Slots
}

func (p *Pool) Acquire(ctx context.Context) (*Lease, error) {
	engine, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &Lease{pool: p, engine: engine}, nil
}

func (p *Pool) TryAcquire() (*Lease, error) {
	engine, err := p.tryAcquire()
	if err != nil {
		return nil, err
	}
	return &Lease{pool: p, engine: engine}, nil
}

func (p *Pool) Call(ctx context.Context, req engineprotocol.Request) (engineprotocol.Response, error) {
	engine, err := p.acquire(ctx)
	if err != nil {
		return engineprotocol.Response{}, err
	}
	defer p.release(engine)

	return engine.Call(ctx, req)
}

func (p *Pool) TryCall(ctx context.Context, req engineprotocol.Request) (engineprotocol.Response, error) {
	engine, err := p.tryAcquire()
	if err != nil {
		return engineprotocol.Response{}, err
	}
	defer p.release(engine)

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
	idle := p.idle
	p.mu.Unlock()

	idle <- engine
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
