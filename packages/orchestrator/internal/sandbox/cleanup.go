package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"go.uber.org/zap"
)

type cleaner struct {
	name string
	fn   func(context.Context) error
}
type Cleanup struct {
	cleanup         []cleaner
	priorityCleanup []cleaner
	error           error
	once            sync.Once

	hasRun atomic.Bool
	mu     sync.Mutex
}

func NewCleanup() *Cleanup {
	return &Cleanup{}
}

func (c *Cleanup) AddNoContext(ctx context.Context, name string, f func() error) {
	c.Add(ctx, name, func(_ context.Context) error { return f() })
}

func (c *Cleanup) Add(ctx context.Context, name string, f func(ctx context.Context) error) {
	if c.hasRun.Load() == true {
		logger.L().Error(ctx, "Add called after cleanup has run, ignoring function", zap.String("name", name))

		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanup = append(c.cleanup, cleaner{name: name, fn: f})
}

func (c *Cleanup) AddPriority(ctx context.Context, name string, f func(ctx context.Context) error) {
	if c.hasRun.Load() == true {
		logger.L().Error(ctx, "AddPriority called after cleanup has run, ignoring function")

		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.priorityCleanup = append(c.priorityCleanup, cleaner{name: name, fn: f})
}

func (c *Cleanup) Run(ctx context.Context) error {
	c.once.Do(func() {
		c.run(context.WithoutCancel(ctx))
	})

	return c.error
}

func (c *Cleanup) run(ctx context.Context) {
	c.hasRun.Store(true)

	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error

	for i := len(c.priorityCleanup) - 1; i >= 0; i-- {
		clnr := c.priorityCleanup[i]
		zap.L().Info("running cleaner", zap.String("name", clnr.name))
		err := clnr.fn(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("cleaner %q failed: %w", clnr.name, err))
		}
	}

	for i := len(c.cleanup) - 1; i >= 0; i-- {
		clnr := c.cleanup[i]
		zap.L().Info("running cleaner", zap.String("name", clnr.name))
		err := clnr.fn(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("cleaner %q failed: %w", clnr.name, err))
		}
	}

	c.error = errors.Join(errs...)
}

func cleanupFiles(config cfg.BuilderConfig, files *storage.SandboxFiles) func(context.Context) error {
	return func(context.Context) error {
		var errs []error

		for _, p := range []string{
			files.SandboxFirecrackerSocketPath(),
			files.SandboxUffdSocketPath(),
			files.SandboxCacheRootfsLinkPath(config),
		} {
			err := os.RemoveAll(p)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to delete '%s': %w", p, err))
			}
		}

		if len(errs) == 0 {
			return nil
		}

		return fmt.Errorf("failed to cleanup files: %w", errors.Join(errs...))
	}
}
