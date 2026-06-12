package draingate

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrDraining = errors.New("drain gate is draining")

type Gate struct {
	initOnce  sync.Once
	drainOnce sync.Once

	mu       sync.Mutex
	done     chan struct{}
	draining bool
	count    int
	changed  chan struct{}
}

func New() *Gate {
	g := &Gate{}
	g.init()

	return g
}

func (g *Gate) init() {
	g.initOnce.Do(func() {
		g.done = make(chan struct{})
		g.changed = make(chan struct{})
	})
}

func (g *Gate) StartDraining() bool {
	if g == nil {
		return false
	}

	g.init()
	transitioned := false
	g.drainOnce.Do(func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		g.draining = true
		transitioned = true
		close(g.done)
	})

	return transitioned
}

func (g *Gate) Draining() bool {
	if g == nil {
		return false
	}

	g.init()
	select {
	case <-g.done:
		return true
	default:
		return false
	}
}

func (g *Gate) Done() <-chan struct{} {
	if g == nil {
		return nil
	}

	g.init()

	return g.done
}

func (g *Gate) Enter() (func(), error) {
	if g == nil {
		return func() {}, nil
	}

	g.init()
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.draining {
		return nil, ErrDraining
	}

	g.count++
	g.notifyChangeLocked()

	return sync.OnceFunc(func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		g.count--
		g.notifyChangeLocked()
	}), nil
}

func (g *Gate) Wait(ctx context.Context) error {
	if g == nil {
		return nil
	}

	g.init()
	for {
		g.mu.Lock()
		if g.count == 0 {
			g.mu.Unlock()

			return nil
		}

		changed := g.changed
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w", ctx.Err())
		case <-changed:
		}
	}
}

func (g *Gate) notifyChangeLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}
