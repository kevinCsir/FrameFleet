package enginepool

import (
	"context"
	"errors"
	"sync"

	"framefleet/worker-node/go/internal/engineprotocol"
)

var ErrLeaseReleased = errors.New("engine lease already released")

type Lease struct {
	pool   *Pool
	engine *Engine
	once   sync.Once

	mu       sync.Mutex
	released bool
}

func (l *Lease) Call(ctx context.Context, req engineprotocol.Request) (engineprotocol.Response, error) {
	l.mu.Lock()
	released := l.released
	l.mu.Unlock()

	if released {
		return engineprotocol.Response{}, ErrLeaseReleased
	}
	req = prepareRequest(req)
	l.pool.markExecuting(l.engine, req)
	defer l.pool.markLeased(l.engine)
	return l.engine.Call(ctx, req)
}

func (l *Lease) Release() {
	l.once.Do(func() {
		l.mu.Lock()
		l.released = true
		l.mu.Unlock()
		l.pool.release(l.engine)
	})
}
