//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Cleanup struct {
	cleanup         []func(ctx context.Context) error
	priorityCleanup []func(ctx context.Context) error
	error           error
	once            sync.Once

	hasRun            atomic.Bool
	contextForLateAdd cleanupContextFunc
	mu                sync.Mutex
}

type cleanupContextFunc func(context.Context) (context.Context, context.CancelFunc)

func NewCleanup() *Cleanup {
	return &Cleanup{contextForLateAdd: utils.WithoutCancelPreservingDeadline}
}

func withoutCancelDiscardingDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithoutCancel(ctx), func() {}
}

func (c *Cleanup) AddNoContext(ctx context.Context, f func() error) {
	c.Add(ctx, func(_ context.Context) error { return f() })
}

func (c *Cleanup) Add(ctx context.Context, f func(ctx context.Context) error) {
	c.mu.Lock()
	if c.hasRun.Load() == true {
		contextForLateAdd := c.contextForLateAdd
		if contextForLateAdd == nil {
			contextForLateAdd = utils.WithoutCancelPreservingDeadline
		}
		c.mu.Unlock()

		c.runLateAdd(ctx, f, contextForLateAdd, "failed to run function after cleanup has run")

		return
	}

	c.cleanup = append(c.cleanup, f)
	c.mu.Unlock()
}

func (c *Cleanup) AddPriority(ctx context.Context, f func(ctx context.Context) error) {
	c.mu.Lock()
	if c.hasRun.Load() == true {
		contextForLateAdd := c.contextForLateAdd
		if contextForLateAdd == nil {
			contextForLateAdd = utils.WithoutCancelPreservingDeadline
		}
		c.mu.Unlock()

		c.runLateAdd(ctx, f, contextForLateAdd, "failed to run priority function after cleanup has run")

		return
	}

	c.priorityCleanup = append(c.priorityCleanup, f)
	c.mu.Unlock()
}

func (c *Cleanup) Run(ctx context.Context) error {
	return c.runWithContext(ctx, utils.WithoutCancelPreservingDeadline)
}

func (c *Cleanup) RunRollback(ctx context.Context) error {
	return c.runWithContext(ctx, withoutCancelDiscardingDeadline)
}

func (c *Cleanup) runWithContext(ctx context.Context, contextForRun cleanupContextFunc) error {
	c.once.Do(func() {
		c.mu.Lock()
		c.contextForLateAdd = contextForRun
		c.mu.Unlock()

		cleanupCtx, cleanupCancel := contextForRun(ctx)
		defer cleanupCancel()

		c.run(cleanupCtx)
	})

	return c.error
}

func (c *Cleanup) runLateAdd(ctx context.Context, f func(context.Context) error, contextForRun cleanupContextFunc, logMessage string) {
	cleanupCtx, cleanupCancel := contextForRun(ctx)
	defer cleanupCancel()

	err := f(cleanupCtx)
	if err != nil {
		logger.L().Error(ctx, logMessage, zap.Error(err))
	}
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
