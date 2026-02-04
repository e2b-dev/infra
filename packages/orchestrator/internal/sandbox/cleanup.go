package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Cleanup struct {
	cleanup         []func(ctx context.Context) error
	priorityCleanup []func(ctx context.Context) error
	error           error
	once            sync.Once

	hasRun atomic.Bool
	mu     sync.Mutex
}

func NewCleanup() *Cleanup {
	return &Cleanup{}
}

func (c *Cleanup) AddNoContext(ctx context.Context, f func() error) {
	c.Add(ctx, func(_ context.Context) error { return f() })
}

func (c *Cleanup) Add(ctx context.Context, f func(ctx context.Context) error) {
	if c.hasRun.Load() == true {
		err := f(context.WithoutCancel(ctx))
		if err != nil {
			logger.L().Error(ctx, "failed to run function after cleanup has run", zap.Error(err))
		}

		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanup = append(c.cleanup, f)
}

func (c *Cleanup) AddPriority(ctx context.Context, f func(ctx context.Context) error) {
	if c.hasRun.Load() == true {
		err := f(context.WithoutCancel(ctx))
		if err != nil {
			logger.L().Error(ctx, "failed to run priority function after cleanup has run", zap.Error(err))
		}

		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.priorityCleanup = append(c.priorityCleanup, f)
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
		err := c.priorityCleanup[i](ctx)
		if err != nil {
			errs = append(errs, err)
		}
	}

	for i := len(c.cleanup) - 1; i >= 0; i-- {
		err := c.cleanup[i](ctx)
		if err != nil {
			errs = append(errs, err)
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
			files.SandboxCacheRootfsLinkPath(config.StorageConfig),
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
