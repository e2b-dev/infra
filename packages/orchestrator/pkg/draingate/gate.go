package draingate

import (
	"context"
	"errors"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var ErrDraining = errors.New("drain gate is draining")

type Gate struct {
	initOnce  sync.Once
	drainOnce sync.Once

	mu       sync.Mutex
	done     chan struct{}
	draining bool
	wg       sync.WaitGroup
}

func New() *Gate {
	g := &Gate{}
	g.init()

	return g
}

func (g *Gate) init() {
	g.initOnce.Do(func() {
		g.done = make(chan struct{})
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

	g.wg.Add(1)

	return sync.OnceFunc(g.wg.Done), nil
}

// Wait blocks until every entry admitted by Enter has been released, or ctx is
// done.
//
// Wait must be called only after StartDraining. Once draining, Enter admits no
// new entries, so the admission count can never rise from zero while Wait is in
// progress — the one ordering constraint sync.WaitGroup imposes (a positive Add
// from a zero counter must happen-before Wait).
func (g *Gate) Wait(ctx context.Context) error {
	if g == nil {
		return nil
	}

	g.init()

	return utils.WaitGroupWait(ctx, &g.wg)
}
